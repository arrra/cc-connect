package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPinHandler_AddToSession verifies that adding a pin via Update (as cmdPin does)
// stores it in the session and that GetByKey reflects it.
func TestPinHandler_AddToSession(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	key := "slack:C001:1700000000.000100"

	sess, err := store.Spawn(key, "root objective")
	if err != nil {
		t.Fatal(err)
	}

	pin := PinnedItem{
		Text:     "remember this",
		Source:   "user_explicit",
		PinnedAt: time.Now(),
		PinnedBy: "U001",
	}
	sess.Pinned = append(sess.Pinned, pin)
	if err := store.Update(sess); err != nil {
		t.Fatalf("Update: %v", err)
	}

	loaded, err := store.GetByKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Pinned) != 1 {
		t.Fatalf("expected 1 pin, got %d", len(loaded.Pinned))
	}
	if loaded.Pinned[0].Text != "remember this" {
		t.Errorf("pin text = %q, want %q", loaded.Pinned[0].Text, "remember this")
	}
	if loaded.Pinned[0].Source != "user_explicit" {
		t.Errorf("pin source = %q, want %q", loaded.Pinned[0].Source, "user_explicit")
	}
	if loaded.Pinned[0].PinnedBy != "U001" {
		t.Errorf("pin pinned_by = %q, want %q", loaded.Pinned[0].PinnedBy, "U001")
	}
}

// TestPinHandler_MultiplePins verifies that successive /pin calls accumulate.
func TestPinHandler_MultiplePins(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	key := "slack:C002:1700000000.000200"

	sess, _ := store.Spawn(key, "objective")

	for i, text := range []string{"alpha", "beta", "gamma"} {
		s, err := store.GetByKey(key)
		if err != nil {
			t.Fatalf("pin %d: GetByKey: %v", i, err)
		}
		s.Pinned = append(s.Pinned, PinnedItem{
			Text:     text,
			Source:   "user_explicit",
			PinnedAt: time.Now(),
			PinnedBy: "U001",
		})
		if err := store.Update(s); err != nil {
			t.Fatalf("pin %d: Update: %v", i, err)
		}
	}
	_ = sess

	final, _ := store.GetByKey(key)
	if len(final.Pinned) != 3 {
		t.Fatalf("expected 3 pins, got %d", len(final.Pinned))
	}
	texts := []string{final.Pinned[0].Text, final.Pinned[1].Text, final.Pinned[2].Text}
	want := []string{"alpha", "beta", "gamma"}
	for i, w := range want {
		if texts[i] != w {
			t.Errorf("pin[%d].Text = %q, want %q", i, texts[i], w)
		}
	}
}

// TestPinHandler_PersistenceWrite verifies the full /pin flow: add pin, call
// SavePins, then verify the pin survives a store restart (simulating daemon restart).
func TestPinHandler_PersistenceWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pins.json")
	ps := NewPinStore(path)

	store := NewInMemorySessionStore(ps, nil)
	key := "slack:C003:1700000000.000300"

	sess, err := store.Spawn(key, "root objective")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate cmdPin: add pin, Update, SavePins.
	sess.Pinned = append(sess.Pinned, PinnedItem{
		Text:     "pinned item",
		Source:   "user_explicit",
		PinnedAt: time.Now(),
		PinnedBy: "U002",
	})
	if err := store.Update(sess); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := store.SavePins(); err != nil {
		t.Fatalf("SavePins: %v", err)
	}

	// File must exist after SavePins.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("pins.json not created after SavePins: %v", err)
	}

	// Simulate daemon restart: load saved pins, create a fresh store.
	savedPins, err := ps.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	store2 := NewInMemorySessionStore(ps, savedPins)

	// Spawn new session — pins should auto-load.
	newSess, err := store2.Spawn(key, "re-started objective")
	if err != nil {
		t.Fatalf("Spawn (restart): %v", err)
	}
	if len(newSess.Pinned) != 1 {
		t.Fatalf("expected 1 pin after reload, got %d", len(newSess.Pinned))
	}
	if newSess.Pinned[0].Text != "pinned item" {
		t.Errorf("pin text after reload = %q, want %q", newSess.Pinned[0].Text, "pinned item")
	}
}

// TestPinHandler_NoSession verifies that GetByKey returns nil for a key with no session.
func TestPinHandler_NoSession(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	sess, err := store.GetByKey("slack:C999:no-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess != nil {
		t.Errorf("expected nil session, got %+v", sess)
	}
}
