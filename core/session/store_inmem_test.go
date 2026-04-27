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

func TestSpawnOrAttach_AtomicUnderConcurrency(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	const key = "slack:C1:t1"
	const goroutines = 50

	type result struct {
		wasSpawned bool
		err        error
	}
	results := make([]result, goroutines)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			_, spawned, err := store.SpawnOrAttach(key, "obj")
			results[idx] = result{wasSpawned: spawned, err: err}
		}(i)
	}
	wg.Wait()

	sess, err := store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if sess == nil {
		t.Fatal("session not found after concurrent SpawnOrAttach calls")
	}

	spawnedCount := 0
	for _, r := range results {
		if r.err != nil {
			t.Errorf("SpawnOrAttach returned error: %v", r.err)
		}
		if r.wasSpawned {
			spawnedCount++
		}
	}
	if spawnedCount != 1 {
		t.Errorf("wasSpawned == true count = %d, want exactly 1", spawnedCount)
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
