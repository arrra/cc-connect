package twilio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPhoneThreadStore_FilePerms(t *testing.T) {
	dir := t.TempDir()
	storeDir := filepath.Join(dir, "boi-pii")
	path := filepath.Join(storeDir, "threads.json")

	s := NewPhoneThreadStore(path)
	if err := s.SetThread("+19165550100", LeadThread{Channel: "C123", ThreadTS: "1234.5678"}); err != nil {
		t.Fatalf("SetThread: %v", err)
	}

	// File must be 0600
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("file perm = %o, want 0600", got)
	}

	// Directory must be 0700
	di, err := os.Stat(storeDir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("dir perm = %o, want 0700", got)
	}
}

func TestPhoneThreadStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "threads.json")

	s := NewPhoneThreadStore(path)
	th := LeadThread{Channel: "CSLACK", ThreadTS: "9999.0001"}
	if err := s.SetThread("+14155550100", th); err != nil {
		t.Fatalf("SetThread: %v", err)
	}

	got, ok := s.GetThread("+14155550100")
	if !ok {
		t.Fatal("GetThread: not found after set")
	}
	if got != th {
		t.Errorf("GetThread = %+v, want %+v", got, th)
	}
}

func TestPhoneThreadStore_Reload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "threads.json")

	s1 := NewPhoneThreadStore(path)
	if err := s1.SetThread("+15305550100", LeadThread{Channel: "CABC", ThreadTS: "111.222"}); err != nil {
		t.Fatalf("SetThread: %v", err)
	}

	// New store instance reads from same file.
	s2 := NewPhoneThreadStore(path)
	got, ok := s2.GetThread("+15305550100")
	if !ok {
		t.Fatal("GetThread after reload: not found")
	}
	if got.Channel != "CABC" || got.ThreadTS != "111.222" {
		t.Errorf("reloaded thread = %+v", got)
	}
}

func TestPhoneThreadStore_InMemory(t *testing.T) {
	s := NewPhoneThreadStore("")
	if err := s.SetThread("+10000000000", LeadThread{Channel: "X", ThreadTS: "0"}); err != nil {
		t.Fatalf("in-memory SetThread: %v", err)
	}
	if _, ok := s.GetThread("+10000000000"); !ok {
		t.Error("GetThread: not found in in-memory store")
	}
}

func TestPhoneThreadStore_GetPhone(t *testing.T) {
	s := NewPhoneThreadStore("")
	const phone = "+19165550100"
	const channel = "CABC"
	const threadTS = "111.222"
	if err := s.SetThread(phone, LeadThread{Channel: channel, ThreadTS: threadTS}); err != nil {
		t.Fatalf("SetThread: %v", err)
	}

	// Forward lookup succeeds.
	got, ok := s.GetPhone(channel, threadTS)
	if !ok {
		t.Fatal("GetPhone: not found")
	}
	if got != phone {
		t.Errorf("GetPhone = %q, want %q", got, phone)
	}

	// Reverse lookup miss.
	_, ok = s.GetPhone("COTHER", "000.000")
	if ok {
		t.Error("GetPhone: should return false for unknown channel/ts")
	}
}

func TestPhoneThreadStore_PersistError(t *testing.T) {
	// Path inside a non-existent deep hierarchy that can't be created
	// (use a file as a directory to force an error).
	dir := t.TempDir()
	// Create a file where we'd expect a directory.
	blocked := dir + "/notadir"
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	s := NewPhoneThreadStore(blocked + "/threads.json")
	err := s.SetThread("+19165550100", LeadThread{Channel: "C1", ThreadTS: "1.0"})
	if err == nil {
		t.Error("expected error when path is unwritable")
	}
}

func TestPhoneThreadStore_LoadCorrupted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "threads.json")
	// Write invalid JSON — load should silently ignore it.
	if err := os.WriteFile(path, []byte("not valid json"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	s := NewPhoneThreadStore(path)
	// Should start empty, not panic.
	if _, ok := s.GetThread("+19165550100"); ok {
		t.Error("expected empty store after corrupt file")
	}
}

func TestPhoneThreadStore_LoadNullThreads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "threads.json")
	// threads: null — snap.Threads will be nil, so we keep the empty map.
	if err := os.WriteFile(path, []byte(`{"threads":null}`), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	s := NewPhoneThreadStore(path)
	if _, ok := s.GetThread("+19165550100"); ok {
		t.Error("expected empty store when file has null threads")
	}
}
