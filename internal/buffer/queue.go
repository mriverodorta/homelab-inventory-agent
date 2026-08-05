package buffer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Limits struct {
	Samples int
	Bytes   int64
}

type Entry struct {
	Sequence uint64
	Body     []byte
}

type metadata struct {
	Dropped uint64 `json:"dropped"`
}

type Queue struct {
	mu        sync.Mutex
	directory string
	limits    Limits
}

func Open(directory string, limits Limits) (*Queue, error) {
	if limits.Samples < 1 || limits.Bytes < 1 {
		return nil, errors.New("buffer limits must be positive")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create buffer: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, err
	}
	queue := &Queue{directory: directory, limits: limits}
	if _, err := queue.entriesLocked(); err != nil {
		return nil, err
	}
	return queue, nil
}

func (q *Queue) entryPath(sequence uint64) string {
	return filepath.Join(q.directory, fmt.Sprintf("%020d.heartbeat.gz", sequence))
}

func (q *Queue) metadataPath() string {
	return filepath.Join(q.directory, "metadata.json")
}

func (q *Queue) readMetadataLocked() (metadata, error) {
	body, err := os.ReadFile(q.metadataPath())
	if errors.Is(err, os.ErrNotExist) {
		return metadata{}, nil
	}
	if err != nil {
		return metadata{}, err
	}
	var value metadata
	if err := json.Unmarshal(body, &value); err != nil {
		return metadata{}, errors.New("buffer metadata is invalid")
	}
	return value, nil
}

func (q *Queue) writeMetadataLocked(value metadata) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return atomicWrite(q.metadataPath(), append(body, '\n'))
}

func atomicWrite(filePath string, body []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(filePath), ".queue-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, filePath)
}

func parseSequence(name string) (uint64, bool) {
	if !strings.HasSuffix(name, ".heartbeat.gz") {
		return 0, false
	}
	sequence, err := strconv.ParseUint(strings.TrimSuffix(name, ".heartbeat.gz"), 10, 64)
	return sequence, err == nil && sequence > 0
}

func (q *Queue) entriesLocked() ([]Entry, error) {
	directoryEntries, err := os.ReadDir(q.directory)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(directoryEntries))
	for _, item := range directoryEntries {
		if item.IsDir() {
			continue
		}
		sequence, valid := parseSequence(item.Name())
		if !valid {
			if item.Name() == "metadata.json" || strings.HasPrefix(item.Name(), ".queue-") {
				continue
			}
			return nil, fmt.Errorf("unsupported buffer file %s", item.Name())
		}
		body, readErr := os.ReadFile(filepath.Join(q.directory, item.Name()))
		if readErr != nil {
			return nil, readErr
		}
		entries = append(entries, Entry{Sequence: sequence, Body: body})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Sequence < entries[j].Sequence })
	return entries, nil
}

func (q *Queue) Add(entry Entry) (uint64, error) {
	if entry.Sequence == 0 || len(entry.Body) == 0 {
		return 0, errors.New("buffer entry is invalid")
	}
	if int64(len(entry.Body)) > q.limits.Bytes {
		return 0, errors.New("buffer entry exceeds the byte limit")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, err := os.Stat(q.entryPath(entry.Sequence)); err == nil {
		return 0, errors.New("buffer sequence already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	if err := atomicWrite(q.entryPath(entry.Sequence), entry.Body); err != nil {
		return 0, err
	}
	entries, err := q.entriesLocked()
	if err != nil {
		return 0, err
	}
	return q.enforceLimitsLocked(entries)
}

func (q *Queue) enforceLimitsLocked(entries []Entry) (uint64, error) {
	var total int64
	for _, current := range entries {
		total += int64(len(current.Body))
	}
	var dropped uint64
	for len(entries) > q.limits.Samples || total > q.limits.Bytes {
		oldest := entries[0]
		if err := os.Remove(q.entryPath(oldest.Sequence)); err != nil {
			return dropped, err
		}
		total -= int64(len(oldest.Body))
		entries = entries[1:]
		dropped++
	}
	if dropped > 0 {
		meta, err := q.readMetadataLocked()
		if err != nil {
			return dropped, err
		}
		meta.Dropped += dropped
		if err := q.writeMetadataLocked(meta); err != nil {
			return dropped, err
		}
	}
	return dropped, nil
}

func (q *Queue) SetLimits(limits Limits) (uint64, error) {
	if limits.Samples < 1 || limits.Bytes < 1 {
		return 0, errors.New("buffer limits must be positive")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.limits = limits
	entries, err := q.entriesLocked()
	if err != nil {
		return 0, err
	}
	return q.enforceLimitsLocked(entries)
}

func (q *Queue) Entries() ([]Entry, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.entriesLocked()
}

func (q *Queue) Remove(sequence uint64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := os.Remove(q.entryPath(sequence)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (q *Queue) Dropped() (uint64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	value, err := q.readMetadataLocked()
	return value.Dropped, err
}

func (q *Queue) ClearDropped() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.writeMetadataLocked(metadata{})
}

func (q *Queue) AcknowledgeDropped(count uint64) error {
	if count == 0 {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	value, err := q.readMetadataLocked()
	if err != nil {
		return err
	}
	if count >= value.Dropped {
		value.Dropped = 0
	} else {
		value.Dropped -= count
	}
	return q.writeMetadataLocked(value)
}
