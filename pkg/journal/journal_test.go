package journal

import (
	"testing"
	"time"
)

func TestKeyedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if got, err := ReadKeyedLineHashes(dir, "repo", 1, time.Time{}); err != nil || got != nil {
		t.Fatalf("empty journal: %v, %v", got, err)
	}
	w, err := OpenKeyed(dir, "repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(1, []uint64{7, 8}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadKeyedLineHashes(dir, "repo", 1, time.Time{})
	if err != nil || len(got) != 2 {
		t.Fatalf("ReadKeyedLineHashes = %v, %v", got, err)
	}
	if _, err := OpenKeyed(dir, ""); err == nil {
		t.Fatal("empty repo id accepted")
	}
}
