package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type asset struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type manifest struct {
	Version        string  `json:"version"`
	SourceRevision string  `json:"sourceRevision"`
	ProtocolMajor  int     `json:"protocolMajor"`
	Assets         []asset `json:"assets"`
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: releasemanifest <checksums> <manifest>")
		os.Exit(64)
	}
	version := os.Getenv("AGENT_RELEASE_VERSION")
	revision := os.Getenv("AGENT_SOURCE_REVISION")
	if version == "" || revision == "" {
		fmt.Fprintln(os.Stderr, "release version and source revision are required")
		os.Exit(64)
	}
	file, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer file.Close()
	value := manifest{Version: version, SourceRevision: revision, ProtocolMajor: 1}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			panic("invalid checksum line")
		}
		if decoded, err := hex.DecodeString(fields[0]); err != nil || len(decoded) != sha256.Size {
			panic("invalid sha256 digest")
		}
		path := strings.TrimPrefix(fields[1], "*")
		clean := filepath.ToSlash(filepath.Clean(path))
		if clean != path || strings.HasPrefix(clean, "../") || filepath.IsAbs(path) {
			panic("invalid release asset path")
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			panic("release asset is not a regular file: " + strconv.Quote(path))
		}
		value.Assets = append(value.Assets, asset{Path: path, SHA256: fields[0], Bytes: info.Size()})
	}
	if err := scanner.Err(); err != nil {
		panic(err)
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		panic(err)
	}
	body = append(body, '\n')
	if err := os.WriteFile(os.Args[2], body, 0o644); err != nil {
		panic(err)
	}
}
