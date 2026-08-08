package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/mriverodorta/homelab-inventory-agent/internal/agentupdate"
	"github.com/mriverodorta/homelab-inventory-agent/internal/buffer"
	containercollector "github.com/mriverodorta/homelab-inventory-agent/internal/collectors/containers"
	"github.com/mriverodorta/homelab-inventory-agent/internal/collectors/platform"
	"github.com/mriverodorta/homelab-inventory-agent/internal/config"
	"github.com/mriverodorta/homelab-inventory-agent/internal/identity"
	"github.com/mriverodorta/homelab-inventory-agent/internal/inventoryscan"
	agentruntime "github.com/mriverodorta/homelab-inventory-agent/internal/runtime"
	"github.com/mriverodorta/homelab-inventory-agent/internal/transport"
	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

var version = "0.1.0-dev"

var effectiveUserID = os.Geteuid

var runAgentUpdate = agentupdate.Run

func defaultConfigPath() string {
	if runtime.GOOS == "freebsd" {
		return "/usr/local/etc/homelab-inventory-agent/config.json"
	}
	return "/etc/homelab-inventory-agent/config.json"
}

func runUpdate(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("homelab-inventory-agent update", flag.ContinueOnError)
	flags.SetOutput(output)
	configPath := flags.String("config", defaultConfigPath(), "configuration file")
	requestedVersion := flags.String("version", "", "install an exact served version")
	checkOnly := flags.Bool("check", false, "check for an update without installing it")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("update does not accept positional arguments")
	}
	result, err := runAgentUpdate(ctx, agentupdate.Options{
		ConfigPath: *configPath, CurrentVersion: version, Requested: *requestedVersion,
		CheckOnly: *checkOnly, EffectiveUserID: effectiveUserID,
		Output: func(format string, values ...any) { _, _ = fmt.Fprintf(output, format, values...) },
	})
	if err != nil {
		return err
	}
	if result.Updated {
		return nil
	}
	if result.UpdateAvailable {
		_, _ = fmt.Fprintf(output, "Agent update available: %s -> %s\n", result.CurrentVersion, result.TargetVersion)
	} else {
		_, _ = fmt.Fprintf(output, "Agent %s is already current.\n", result.CurrentVersion)
	}
	return nil
}

func runInventory(ctx context.Context, args []string, input io.Reader, output io.Writer, scanner inventoryscan.Scanner) error {
	flags := flag.NewFlagSet("homelab-inventory-agent inventory", flag.ContinueOnError)
	flags.SetOutput(output)
	configPath := flags.String("config", defaultConfigPath(), "configuration file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("inventory does not accept positional arguments")
	}
	if effectiveUserID() != 0 {
		return fmt.Errorf("hardware inventory requires elevated privileges; run sudo homelab-inventory-agent inventory")
	}
	configuration, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if scanner == nil {
		scanner = inventoryscan.NewScanner()
	}
	snapshot, err := scanner.Collect(ctx, configuration.Host)
	if err != nil {
		return err
	}
	defer inventoryscan.Clear(&snapshot)
	confirmed, err := inventoryscan.Confirm(input, output, snapshot)
	if err != nil {
		return err
	}
	if !confirmed {
		_, _ = fmt.Fprintln(output, "Hardware snapshot was not sent.")
		return nil
	}
	if err := inventoryscan.Send(ctx, inventoryscan.DefaultSocketPath, snapshot); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(output, "Hardware snapshot sent for review.")
	return nil
}

func run(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "inventory" {
		return runInventory(ctx, args[1:], os.Stdin, os.Stdout, nil)
	}
	if len(args) > 0 && args[0] == "update" {
		return runUpdate(ctx, args[1:], os.Stdout)
	}
	flags := flag.NewFlagSet("homelab-inventory-agent", flag.ContinueOnError)
	showVersion := flags.Bool("version", false, "print the agent version and exit")
	configPath := flags.String("config", "/etc/homelab-inventory-agent/config.json", "configuration file")
	enrollmentCode := flags.String("enrollment-code", "", "one-time enrollment code")
	enrollmentCodeFile := flags.String("enrollment-code-file", "", "read the one-time enrollment code from a private file")
	once := flags.Bool("once", false, "collect and deliver one heartbeat, then exit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println(version)
		return nil
	}
	if *enrollmentCode != "" && *enrollmentCodeFile != "" {
		return fmt.Errorf("provide only one enrollment-code source")
	}
	if *enrollmentCodeFile != "" {
		file, err := os.Open(*enrollmentCodeFile)
		if err != nil {
			return fmt.Errorf("open enrollment code file: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(file, 513))
		closeErr := file.Close()
		if readErr != nil {
			return fmt.Errorf("read enrollment code file: %w", readErr)
		}
		if closeErr != nil {
			return closeErr
		}
		if len(body) == 0 || len(body) > 512 {
			return fmt.Errorf("enrollment code file must contain 1 to 512 bytes")
		}
		*enrollmentCode = strings.TrimSpace(string(body))
	}

	configuration, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	deviceIdentity, err := identity.LoadOrCreate(filepath.Join(configuration.StateDirectory, "identity.json"))
	if err != nil {
		return err
	}
	queue, err := buffer.Open(filepath.Join(configuration.StateDirectory, "queue"), buffer.Limits{Samples: 60, Bytes: 10 << 20})
	if err != nil {
		return err
	}
	client, err := transport.New(configuration.Endpoint, nil)
	if err != nil {
		return err
	}
	var containers *containercollector.Collector
	if configuration.Containers.Mode != "disabled" {
		containers, err = containercollector.New(containercollector.Options{
			Mode:     configuration.Containers.Mode,
			Runtime:  configuration.Containers.Runtime,
			Endpoint: configuration.Containers.Endpoint,
		})
		if err != nil {
			return fmt.Errorf("configure container collector: %w", err)
		}
	}
	collector := platform.New(deviceIdentity.OpaqueID, platform.Options{
		Filesystems:  configuration.Filesystems,
		SMARTDevices: configuration.StorageHealth.SMARTDevices,
		Containers:   containers,
	})
	capabilities := collector.Capabilities()
	capabilities["agent.native-update"] = protocol.Capability{
		State:  protocol.Available,
		Detail: "manual root-authorized atomic update with rollback",
	}
	agent, err := agentruntime.New(agentruntime.Options{
		Config: configuration, Version: version, Capabilities: capabilities,
		Identity: deviceIdentity, Queue: queue, Client: client, Collector: collector,
		OnError: func(err error) { log.Printf("agent cycle failed: %v", err) },
	})
	if err != nil {
		return err
	}
	if deviceIdentity.DeviceID() == 0 {
		if *enrollmentCode == "" {
			return fmt.Errorf("agent is not activated; provide -enrollment-code once")
		}
		if err := agent.Activate(ctx, *enrollmentCode); err != nil {
			return fmt.Errorf("activate agent: %w", err)
		}
	}
	if *once {
		return agent.RunOnce(ctx)
	}
	daemonContext, cancel := context.WithCancel(ctx)
	defer cancel()
	inventoryServer := &inventoryscan.Server{
		SocketPath: inventoryscan.DefaultSocketPath,
		Host:       configuration.Host,
		OpaqueID:   deviceIdentity.OpaqueID,
		Submit:     agent.SubmitHardwareSnapshot,
	}
	result := make(chan error, 2)
	go func() { result <- inventoryServer.ListenAndServe(daemonContext) }()
	go func() { result <- agent.Run(daemonContext) }()
	err = <-result
	cancel()
	return err
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
