package agentupdate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/mriverodorta/homelab-inventory-agent/internal/config"
)

type Options struct {
	ConfigPath      string
	CurrentVersion  string
	Requested       string
	CheckOnly       bool
	HTTPClient      *http.Client
	EffectiveUserID func() int
	Output          func(string, ...any)
	platform        *platform
	runner          commandRunner
	lockPath        string
}

type Result struct {
	CurrentVersion  string
	TargetVersion   string
	UpdateAvailable bool
	Updated         bool
}

func Run(ctx context.Context, options Options) (Result, error) {
	effectiveUserID := options.EffectiveUserID
	if effectiveUserID == nil {
		effectiveUserID = os.Geteuid
	}
	if effectiveUserID() != 0 {
		return Result{}, errors.New("native updates require elevated privileges; run sudo homelab-inventory-agent update")
	}
	if _, err := parseVersion(options.CurrentVersion); err != nil {
		return Result{}, fmt.Errorf("current agent version is invalid: %w", err)
	}
	configuration, err := config.Load(options.ConfigPath)
	if err != nil {
		return Result{}, err
	}
	selected := options.platform
	if selected == nil {
		value, platformErr := defaultPlatform()
		if platformErr != nil {
			return Result{}, platformErr
		}
		selected = &value
	}
	runner := options.runner
	if runner == nil {
		runner = execRunner{}
	}
	client, err := newReleaseClient(configuration.Endpoint, options.HTTPClient)
	if err != nil {
		return Result{}, err
	}
	targetVersion := options.Requested
	manifestURL := ""
	if targetVersion == "" {
		current, currentErr := client.current(ctx)
		if currentErr != nil {
			return Result{}, currentErr
		}
		targetVersion = current.Version
		manifestURL = current.ManifestURL
	}
	comparison, err := compareVersions(options.CurrentVersion, targetVersion)
	if err != nil {
		return Result{}, err
	}
	result := Result{CurrentVersion: options.CurrentVersion, TargetVersion: targetVersion, UpdateAvailable: comparison < 0}
	if options.Requested == "" && comparison >= 0 {
		return result, nil
	}
	if options.CheckOnly {
		return result, nil
	}
	lockPath := options.lockPath
	if lockPath == "" {
		lockPath = filepath.Join(filepath.Dir(selected.binaryPath), ".homelab-inventory-agent-update.lock")
	}
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return Result{}, errors.New("another agent update is already running")
		}
		return Result{}, fmt.Errorf("create update lock: %w", err)
	}
	_ = lock.Close()
	defer os.Remove(lockPath)

	release, err := client.manifest(ctx, targetVersion, manifestURL)
	if err != nil {
		return Result{}, err
	}
	assets := make(map[string]asset, len(release.Assets))
	for _, item := range release.Assets {
		assets[item.Path] = item
	}
	binaryAsset, binaryExists := assets[selected.binaryAsset]
	serviceAsset, serviceExists := assets[selected.serviceAsset]
	if !binaryExists || !serviceExists {
		return Result{}, fmt.Errorf("agent release does not support %s/%s", selected.os, selected.arch)
	}
	binaryBody, err := client.download(ctx, targetVersion, binaryAsset)
	if err != nil {
		return Result{}, err
	}
	serviceBody, err := client.download(ctx, targetVersion, serviceAsset)
	if err != nil {
		return Result{}, err
	}
	previousBinary, err := snapshotFile(selected.binaryPath)
	if err != nil {
		return Result{}, fmt.Errorf("snapshot existing agent executable: %w", err)
	}
	previousService, err := snapshotFile(selected.servicePath)
	if err != nil {
		return Result{}, fmt.Errorf("snapshot existing agent service: %w", err)
	}
	if err := runCommand(ctx, runner, selected.stop); err != nil {
		return Result{}, fmt.Errorf("stop agent service: %w", err)
	}
	replaced := false
	rollback := func(cause error) error {
		var binaryErr, serviceErr error
		if replaced {
			binaryErr = atomicWrite(previousBinary.path, previousBinary.body, previousBinary.mode)
			serviceErr = atomicWrite(previousService.path, previousService.body, previousService.mode)
		}
		_ = runCommand(ctx, runner, selected.reload)
		restartErr := runCommand(ctx, runner, selected.start)
		var healthErr error
		if restartErr == nil {
			healthErr = runCommand(ctx, runner, selected.health)
		}
		if replaced {
			if binaryErr != nil || serviceErr != nil || restartErr != nil || healthErr != nil {
				return fmt.Errorf("update failed (%v) and rollback was incomplete (binary=%v service=%v restart=%v health=%v)", cause, binaryErr, serviceErr, restartErr, healthErr)
			}
		} else if restartErr != nil || healthErr != nil {
			return fmt.Errorf("update failed (%v) and the unchanged service could not be restored (restart=%v health=%v)", cause, restartErr, healthErr)
		}
		return cause
	}
	if err := atomicWrite(selected.binaryPath, binaryBody, 0o755); err != nil {
		return Result{}, rollback(fmt.Errorf("replace agent executable: %w", err))
	}
	replaced = true
	if err := atomicWrite(selected.servicePath, serviceBody, selected.serviceMode); err != nil {
		return Result{}, rollback(fmt.Errorf("replace agent service: %w", err))
	}
	output, err := runner.Output(ctx, selected.binaryPath, "-version")
	if err != nil || strings.TrimSpace(string(output)) != targetVersion {
		return Result{}, rollback(errors.New("updated agent executable failed version verification"))
	}
	if err := runCommand(ctx, runner, selected.reload); err != nil {
		return Result{}, rollback(err)
	}
	if err := runCommand(ctx, runner, selected.start); err != nil {
		return Result{}, rollback(err)
	}
	if err := runCommand(ctx, runner, selected.health); err != nil {
		return Result{}, rollback(fmt.Errorf("updated agent service did not become healthy: %w", err))
	}
	result.Updated = true
	if options.Output != nil {
		options.Output("Homelab Inventory Agent %s installed successfully.\n", targetVersion)
	}
	return result, nil
}
