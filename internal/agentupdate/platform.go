package agentupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

type commandRunner interface {
	Run(context.Context, string, ...string) error
	Output(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, arguments ...string) error {
	command := exec.CommandContext(ctx, name, arguments...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func (execRunner) Output(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, arguments...).Output()
}

type platform struct {
	os           string
	arch         string
	binaryPath   string
	servicePath  string
	binaryAsset  string
	serviceAsset string
	stop         []string
	reload       []string
	start        []string
	health       []string
	serviceMode  os.FileMode
}

func defaultPlatform() (platform, error) {
	arch := runtime.GOARCH
	switch runtime.GOOS {
	case "linux":
		if arch != "amd64" && arch != "arm64" {
			return platform{}, fmt.Errorf("unsupported Linux architecture %s", arch)
		}
		return platform{
			os: "linux", arch: arch,
			binaryPath: "/usr/local/sbin/homelab-inventory-agent", servicePath: "/etc/systemd/system/homelab-inventory-agent.service",
			binaryAsset: "homelab-inventory-agent-linux-" + arch, serviceAsset: "homelab-inventory-agent.service",
			stop:        []string{"systemctl", "stop", "homelab-inventory-agent.service"},
			reload:      []string{"systemctl", "daemon-reload"},
			start:       []string{"systemctl", "restart", "homelab-inventory-agent.service"},
			health:      []string{"systemctl", "is-active", "--quiet", "homelab-inventory-agent.service"},
			serviceMode: 0o644,
		}, nil
	case "freebsd":
		if arch != "amd64" {
			return platform{}, fmt.Errorf("unsupported FreeBSD architecture %s", arch)
		}
		return platform{
			os: "freebsd", arch: arch,
			binaryPath: "/usr/local/sbin/homelab-inventory-agent", servicePath: "/usr/local/etc/rc.d/homelab_inventory_agent",
			binaryAsset: "homelab-inventory-agent-freebsd-amd64", serviceAsset: "homelab_inventory_agent",
			stop:        []string{"service", "homelab_inventory_agent", "stop"},
			start:       []string{"service", "homelab_inventory_agent", "restart"},
			health:      []string{"service", "homelab_inventory_agent", "onestatus"},
			serviceMode: 0o555,
		}, nil
	default:
		return platform{}, fmt.Errorf("native agent updates are unsupported on %s", runtime.GOOS)
	}
}

func runCommand(ctx context.Context, runner commandRunner, command []string) error {
	if len(command) == 0 {
		return nil
	}
	if err := runner.Run(ctx, command[0], command[1:]...); err != nil {
		return fmt.Errorf("run %s: %w", command[0], err)
	}
	return nil
}

func waitForSustainedHealth(ctx context.Context, runner commandRunner, command []string, checks int, interval time.Duration) error {
	if len(command) == 0 {
		return nil
	}
	if checks < 1 {
		checks = 1
	}
	for check := 0; check < checks; check++ {
		if err := runCommand(ctx, runner, command); err != nil {
			return err
		}
		if check+1 == checks || interval <= 0 {
			continue
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

type savedFile struct {
	path string
	body []byte
	mode os.FileMode
}

func snapshotFile(filePath string) (savedFile, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return savedFile{}, err
	}
	if !info.Mode().IsRegular() {
		return savedFile{}, errors.New("update target is not a regular file")
	}
	body, err := os.ReadFile(filePath)
	if err != nil {
		return savedFile{}, err
	}
	return savedFile{path: filePath, body: body, mode: info.Mode().Perm()}, nil
}

func atomicWrite(filePath string, body []byte, mode os.FileMode) error {
	directory := filepath.Dir(filePath)
	file, err := os.CreateTemp(directory, ".homelab-inventory-agent-update-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, filePath); err != nil {
		return err
	}
	directoryFile, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryFile.Close()
	return directoryFile.Sync()
}
