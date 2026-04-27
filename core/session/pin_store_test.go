package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPinStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pins.json")

	now := time.Now().UTC().Truncate(time.Second)

	key1 := "slack:C123:1700000000.000100"
	key2 := "slack:D456"

	pins := map[string][]PinnedItem{
		key1: {
			{Text: "pin one", Source: "user_explicit", PinnedAt: now, PinnedBy: "U001"},
			{Text: "pin two", Source: "user_explicit", PinnedAt: now, PinnedBy: "U001"},
		},
		key2: {
			{Text: "pin three", Source: "user_explicit", PinnedAt: now, PinnedBy: "U002"},
		},
	}

	ps := NewPinStore(path)

	// File does not yet exist — Load should return an empty map, not an error.
	empty, err := ps.Load()
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected empty map, got %d keys", len(empty))
	}

	// Save 3 pins across 2 session keys.
	if err := ps.Save(pins); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Simulate daemon restart: create a fresh PinStore pointed at the same file.
	ps2 := NewPinStore(path)
	loaded, err := ps2.Load()
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 session keys, got %d", len(loaded))
	}
	if len(loaded[key1]) != 2 {
		t.Fatalf("key1: expected 2 pins, got %d", len(loaded[key1]))
	}
	if len(loaded[key2]) != 1 {
		t.Fatalf("key2: expected 1 pin, got %d", len(loaded[key2]))
	}
	if loaded[key1][0].Text != "pin one" {
		t.Errorf("key1 pin[0] text = %q, want %q", loaded[key1][0].Text, "pin one")
	}
	if loaded[key2][0].Text != "pin three" {
		t.Errorf("key2 pin[0] text = %q, want %q", loaded[key2][0].Text, "pin three")
	}
}

func TestPinStoreReloadIntoNewStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pins.json")
	ps := NewPinStore(path)

	now := time.Now().UTC().Truncate(time.Second)

	key1 := "slack:C111:1700000000.000100"
	key2 := "slack:D222"

	// Build initial store with empty saved pins (new install).
	store1 := NewInMemorySessionStore(ps, nil)

	// Spawn sessions and add pins via Update.
	sess1, _ := store1.Spawn(key1, "objective one")
	sess1.Pinned = append(sess1.Pinned, PinnedItem{Text: "alpha", Source: "user_explicit", PinnedAt: now, PinnedBy: "U1"})
	sess1.Pinned = append(sess1.Pinned, PinnedItem{Text: "beta", Source: "user_explicit", PinnedAt: now, PinnedBy: "U1"})
	if err := store1.Update(sess1); err != nil {
		t.Fatal(err)
	}

	sess2, _ := store1.Spawn(key2, "objective two")
	sess2.Pinned = append(sess2.Pinned, PinnedItem{Text: "gamma", Source: "user_explicit", PinnedAt: now, PinnedBy: "U2"})
	if err := store1.Update(sess2); err != nil {
		t.Fatal(err)
	}

	// SavePins should write all active session pins to disk.
	if err := store1.SavePins(); err != nil {
		t.Fatalf("SavePins: %v", err)
	}

	// Simulate daemon restart: load pins, create fresh store.
	savedPins, err := ps.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	store2 := NewInMemorySessionStore(ps, savedPins)

	// Spawn sessions in new store — pins should auto-load.
	newSess1, _ := store2.Spawn(key1, "re-started objective one")
	if len(newSess1.Pinned) != 2 {
		t.Fatalf("key1: expected 2 pinned items after reload, got %d", len(newSess1.Pinned))
	}
	if newSess1.Pinned[0].Text != "alpha" {
		t.Errorf("key1 pin[0] = %q, want %q", newSess1.Pinned[0].Text, "alpha")
	}
	if newSess1.Pinned[1].Text != "beta" {
		t.Errorf("key1 pin[1] = %q, want %q", newSess1.Pinned[1].Text, "beta")
	}

	newSess2, _ := store2.Spawn(key2, "re-started objective two")
	if len(newSess2.Pinned) != 1 {
		t.Fatalf("key2: expected 1 pinned item after reload, got %d", len(newSess2.Pinned))
	}
	if newSess2.Pinned[0].Text != "gamma" {
		t.Errorf("key2 pin[0] = %q, want %q", newSess2.Pinned[0].Text, "gamma")
	}
}

func TestPinStoreTerminateSaves(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pins.json")
	ps := NewPinStore(path)

	now := time.Now().UTC().Truncate(time.Second)
	key := "slack:C999:1700000000.000100"

	store := NewInMemorySessionStore(ps, nil)
	sess, _ := store.Spawn(key, "objective")
	sess.Pinned = append(sess.Pinned, PinnedItem{Text: "survive restart", Source: "user_explicit", PinnedAt: now, PinnedBy: "U9"})
	_ = store.Update(sess)

	// Terminate should save pins to disk.
	if err := store.Terminate(key); err != nil {
		t.Fatalf("Terminate: %v", err)
	}

	// File must exist after Terminate.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("pins.json not found after Terminate: %v", err)
	}

	// Reload and verify pin is there.
	loaded, err := ps.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded[key]) != 1 || loaded[key][0].Text != "survive restart" {
		t.Errorf("pin not found after reload: %v", loaded)
	}
}
