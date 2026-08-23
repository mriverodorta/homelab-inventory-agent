//go:build linux

package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	commandrunner "github.com/mriverodorta/homelab-inventory-agent/internal/command"
	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

const maxOpenRCOutput = 512 * 1024

var (
	openRCServiceNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,255}$`)
	openRCPackagePattern     = regexp.MustCompile(`^(.+)-[0-9][^-]*(-r[0-9]+)?$`)
)

type OpenRC struct {
	root    string
	runner  commandrunner.Runner
	timeout time.Duration
}

func NewOpenRC(root string) *OpenRC {
	return &OpenRC{root: root, runner: commandrunner.Exec{MaxOutput: maxOpenRCOutput}, timeout: 10 * time.Second}
}

func openRCDescription(body []byte) string {
	for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "description=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "description="))
		value = strings.Trim(value, `"'`)
		if len(value) > 512 {
			value = value[:512]
		}
		return value
	}
	return ""
}

func openRCState(root, name string) (string, string) {
	states := []struct {
		directory string
		active    string
		sub       string
	}{
		{"started", "active", "started"},
		{"starting", "activating", "starting"},
		{"stopping", "deactivating", "stopping"},
		{"failed", "failed", "failed"},
		{"inactive", "inactive", "stopped"},
	}
	for _, state := range states {
		if _, err := os.Lstat(filepath.Join(root, "run/openrc", state.directory, name)); err == nil {
			return state.active, state.sub
		}
	}
	return "inactive", "stopped"
}

func openRCEnabled(root, name string) bool {
	levels, err := os.ReadDir(filepath.Join(root, "etc/runlevels"))
	if err != nil {
		return false
	}
	for _, level := range levels {
		if !level.IsDir() {
			continue
		}
		if _, err := os.Lstat(filepath.Join(root, "etc/runlevels", level.Name(), name)); err == nil {
			return true
		}
	}
	return false
}

func openRCPackageName(value string) string {
	value = strings.TrimSpace(value)
	if match := openRCPackagePattern.FindStringSubmatch(value); len(match) > 1 {
		return match[1]
	}
	return value
}

func parseOpenRCOwners(body []byte) map[string]string {
	owners := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		path, owner, found := strings.Cut(strings.TrimSpace(line), " is owned by ")
		if found && filepath.IsAbs(path) {
			owners[filepath.Clean(path)] = openRCPackageName(owner)
		}
	}
	return owners
}

func readOpenRCWorld(root string) map[string]struct{} {
	world := map[string]struct{}{}
	body, err := os.ReadFile(filepath.Join(root, "etc/apk/world"))
	if err != nil {
		return world
	}
	for _, line := range strings.Split(string(body), "\n") {
		name := strings.TrimSpace(line)
		if index := strings.IndexAny(name, "=<>~"); index >= 0 {
			name = name[:index]
		}
		if name != "" {
			world[name] = struct{}{}
		}
	}
	return world
}

func (collector *OpenRC) classify(ctx context.Context, paths []string) map[string]string {
	classifications := make(map[string]string, len(paths))
	for _, path := range paths {
		classifications[path] = "unknown"
	}
	apk := ""
	for _, candidate := range []string{"/sbin/apk", "/usr/sbin/apk"} {
		if _, err := os.Stat(filepath.Join(collector.root, strings.TrimPrefix(candidate, "/"))); err == nil {
			apk = candidate
			break
		}
	}
	if apk == "" {
		return classifications
	}
	arguments := append([]string{"info", "--who-owns"}, paths...)
	body, err := collector.runner.Run(ctx, apk, arguments...)
	if err != nil && len(strings.TrimSpace(string(body))) == 0 {
		return classifications
	}
	owners := parseOpenRCOwners(body)
	world := readOpenRCWorld(collector.root)
	for _, path := range paths {
		owner, found := owners[path]
		if !found {
			classifications[path] = "user-installed"
			continue
		}
		classifications[path] = "system"
		if _, explicit := world[owner]; explicit {
			classifications[path] = "user-installed"
		}
	}
	return classifications
}

func (collector *OpenRC) Collect(ctx context.Context) ([]protocol.Service, error) {
	collectionContext, cancel := context.WithTimeout(ctx, collector.timeout)
	defer cancel()
	directory := filepath.Join(collector.root, "etc/init.d")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("list OpenRC services: %w", err)
	}
	if len(entries) > 512 {
		return nil, errors.New("OpenRC service count exceeds the protocol limit")
	}
	paths := make([]string, 0, len(entries))
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !openRCServiceNamePattern.MatchString(name) {
			return nil, errors.New("OpenRC returned an unsafe service name")
		}
		info, statErr := entry.Info()
		if statErr != nil || info.IsDir() {
			continue
		}
		names = append(names, name)
		paths = append(paths, filepath.Join("/etc/init.d", name))
	}
	sort.Strings(names)
	classifications := collector.classify(collectionContext, paths)
	result := make([]protocol.Service, 0, len(names))
	for _, name := range names {
		relativePath := filepath.Join("/etc/init.d", name)
		body, readErr := os.ReadFile(filepath.Join(collector.root, "etc/init.d", name))
		if readErr != nil {
			return nil, fmt.Errorf("read OpenRC service %s: %w", name, readErr)
		}
		active, sub := openRCState(collector.root, name)
		enabled := openRCEnabled(collector.root, name)
		result = append(result, protocol.Service{
			Manager: "openrc", Name: name, Description: openRCDescription(body), ActiveState: active,
			SubState: sub, Enabled: &enabled, Classification: classifications[relativePath],
		})
	}
	return result, nil
}
