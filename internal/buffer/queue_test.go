package buffer

import (
	"bytes"
	"testing"
)

func TestQueueBoundsAndPersistsOldestFirst(t *testing.T) {
	directory := t.TempDir()
	queue, err := Open(directory, Limits{Samples: 2, Bytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	for sequence := uint64(1); sequence <= 3; sequence++ {
		if _, err := queue.Add(Entry{Sequence: sequence, Body: bytes.Repeat([]byte{byte(sequence)}, 4)}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := queue.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Sequence != 2 || entries[1].Sequence != 3 {
		t.Fatalf("unexpected queue: %#v", entries)
	}
	if dropped, err := queue.Dropped(); err != nil || dropped != 1 {
		t.Fatalf("dropped: %d %v", dropped, err)
	}

	reopened, err := Open(directory, Limits{Samples: 2, Bytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	entries, err = reopened.Entries()
	if err != nil || len(entries) != 2 || entries[0].Sequence != 2 {
		t.Fatalf("reopened queue: %#v %v", entries, err)
	}
	if err := reopened.Remove(2); err != nil {
		t.Fatal(err)
	}
	if err := reopened.ClearDropped(); err != nil {
		t.Fatal(err)
	}
	if dropped, _ := reopened.Dropped(); dropped != 0 {
		t.Fatalf("dropped counter not cleared: %d", dropped)
	}
}

func TestQueueAcknowledgesOnlyReportedDrops(t *testing.T) {
	queue, err := Open(t.TempDir(), Limits{Samples: 1, Bytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for sequence := uint64(1); sequence <= 3; sequence++ {
		if _, err := queue.Add(Entry{Sequence: sequence, Body: []byte("body")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := queue.AcknowledgeDropped(1); err != nil {
		t.Fatal(err)
	}
	if dropped, _ := queue.Dropped(); dropped != 1 {
		t.Fatalf("unreported drop was lost: %d", dropped)
	}
}

func TestQueueAppliesUpdatedContractLimits(t *testing.T) {
	queue, err := Open(t.TempDir(), Limits{Samples: 3, Bytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	for sequence := uint64(1); sequence <= 3; sequence++ {
		_, _ = queue.Add(Entry{Sequence: sequence, Body: []byte("body")})
	}
	if dropped, err := queue.SetLimits(Limits{Samples: 1, Bytes: 100}); err != nil || dropped != 2 {
		t.Fatalf("updated limits: %d %v", dropped, err)
	}
	entries, _ := queue.Entries()
	if len(entries) != 1 || entries[0].Sequence != 3 {
		t.Fatalf("updated limits retained wrong entries: %#v", entries)
	}
}

func TestQueueRejectsOversizedAndDuplicateEntries(t *testing.T) {
	queue, err := Open(t.TempDir(), Limits{Samples: 2, Bytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Add(Entry{Sequence: 1, Body: []byte("12345")}); err == nil {
		t.Fatal("oversized entry accepted")
	}
	if _, err := queue.Add(Entry{Sequence: 1, Body: []byte("1")}); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Add(Entry{Sequence: 1, Body: []byte("1")}); err == nil {
		t.Fatal("duplicate sequence accepted")
	}
}
