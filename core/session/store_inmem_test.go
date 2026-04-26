package session

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestAddPin_LimitEnforced(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	const key = "test-pin-limit-key"

	if _, err := store.Spawn(key, "root"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	for i := range MaxPinsPerSession {
		pin := PinnedItem{
			Text:     fmt.Sprintf("pin-%d", i),
			Source:   "user_explicit",
			PinnedAt: time.Now(),
			PinnedBy: "U001",
		}
		if _, err := store.AddPin(key, pin); err != nil {
			t.Fatalf("AddPin %d: unexpected error: %v", i, err)
		}
	}

	// 21st pin must fail.
	_, err := store.AddPin(key, PinnedItem{
		Text:     "overflow",
		Source:   "user_explicit",
		PinnedAt: time.Now(),
		PinnedBy: "U001",
	})
	if !errors.Is(err, ErrPinLimitReached) {
		t.Fatalf("AddPin at limit: got %v, want ErrPinLimitReached", err)
	}

	// Session still has exactly MaxPinsPerSession pins — no partial write.
	sess, err := store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if len(sess.Pinned) != MaxPinsPerSession {
		t.Errorf("Pinned count = %d, want %d", len(sess.Pinned), MaxPinsPerSession)
	}
}

func TestInMemorySessionStore_ConcurrentMutation(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	const key = "test-key"
	const goroutines = 50

	if _, err := store.Spawn(key, "root"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			if _, err := store.IncrementTurn(key); err != nil {
				t.Errorf("IncrementTurn: %v", err)
			}
		}()
	}
	wg.Wait()

	sess, err := store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if sess == nil {
		t.Fatal("session not found after concurrent increments")
	}
	if sess.TurnCount != goroutines {
		t.Errorf("TurnCount = %d, want %d (lost updates detected)", sess.TurnCount, goroutines)
	}
}
