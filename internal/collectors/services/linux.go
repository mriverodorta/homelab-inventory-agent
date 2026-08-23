//go:build linux

package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

type Collector interface {
	Collect(context.Context) ([]protocol.Service, error)
}

type LinuxSelection struct {
	Collector Collector
	Manager   string
	Detail    string
}

type unavailableCollector struct {
	err error
}

func (collector unavailableCollector) Collect(context.Context) ([]protocol.Service, error) {
	return nil, collector.err
}

func directoryExists(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && stat.IsDir()
}

func NewLinux(root string) LinuxSelection {
	systemd := directoryExists(filepath.Join(root, "run/systemd/system"))
	openrc := directoryExists(filepath.Join(root, "run/openrc"))
	switch {
	case systemd && !openrc:
		return LinuxSelection{Collector: NewSystemd(), Manager: "systemd", Detail: "systemd"}
	case openrc && !systemd:
		return LinuxSelection{Collector: NewOpenRC(root), Manager: "openrc", Detail: "OpenRC"}
	case systemd && openrc:
		return LinuxSelection{Collector: unavailableCollector{err: errors.New("multiple supported Linux service managers detected")}, Detail: "service manager detection is ambiguous"}
	default:
		return LinuxSelection{Collector: unavailableCollector{err: errors.New("no supported Linux service manager detected")}, Detail: "no supported service manager detected"}
	}
}
