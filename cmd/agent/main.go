package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mriverodorta/homelab-inventory-agent/internal/buffer"
	"github.com/mriverodorta/homelab-inventory-agent/internal/collectors/platform"
	"github.com/mriverodorta/homelab-inventory-agent/internal/config"
	"github.com/mriverodorta/homelab-inventory-agent/internal/identity"
	agentruntime "github.com/mriverodorta/homelab-inventory-agent/internal/runtime"
	"github.com/mriverodorta/homelab-inventory-agent/internal/transport"
)

var version = "0.1.0-dev"

func run(ctx context.Context, args []string) error {
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
	collector := platform.New(deviceIdentity.OpaqueID, configuration.Filesystems, configuration.StorageHealth.SMARTDevices)
	agent, err := agentruntime.New(agentruntime.Options{
		Config: configuration, Version: version, Capabilities: collector.Capabilities(),
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
	return agent.Run(ctx)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
