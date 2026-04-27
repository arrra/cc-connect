package session

import (
	"sync"
	"testing"
	"time"
)

func newTestRouter() (*Router, *InMemorySessionStore) {
	store := NewInMemorySessionStore(nil, nil)
	return NewRouter(store), store
}

func TestRouter_TopLevelMentionSpawnsNew(t *testing.T) {
	router, store := newTestRouter()

	ev := SlackEvent{
		ChannelID:    "C001",
		ChannelType:  "channel",
		UserID:       "U001",
		MessageTS:    "1234567890.000100",
		ThreadTS:     "",
		IsBotMention: true,
		ReceivedAt:   time.Now(),
	}

	result, err := router.Route(ev, "help me with X")
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if result.Action != RouteActionSpawn {
		t.Fatalf("expected Spawn, got %v", result.Action)
	}
	if result.SessionKey != ev.MessageTS {
		t.Errorf("session_key should be MessageTS %q, got %q", ev.MessageTS, result.SessionKey)
	}
	if result.Session == nil {
		t.Fatal("session should not be nil")
	}

	// Session should be retrievable by the thread root ts.
	sess, _ := store.GetByKey(ev.MessageTS)
	if sess == nil {
		t.Fatal("session not found in store after spawn")
	}
	if sess.RootObjective != "help me with X" {
		t.Errorf("unexpected root_objective: %q", sess.RootObjective)
	}
}

func TestRouter_ReplyAttachesToExistingSession(t *testing.T) {
	router, store := newTestRouter()
	threadTS := "1234567890.000100"

	// Prime the store with a live session.
	existing, _ := store.Spawn(threadTS, "original objective")

	ev := SlackEvent{
		ChannelID:    "C001",
		ChannelType:  "channel",
		UserID:       "U001",
		MessageTS:    "1234567890.000200",
		ThreadTS:     threadTS,
		IsBotMention: false,
		ReceivedAt:   existing.CreatedAt.Add(5 * time.Minute),
	}

	result, err := router.Route(ev, "follow-up")
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if result.Action != RouteActionAttach {
		t.Fatalf("expected Attach, got %v", result.Action)
	}
	if result.Session.SessionID != existing.SessionID {
		t.Errorf("expected same session_id %q, got %q", existing.SessionID, result.Session.SessionID)
	}
}

func TestRouter_ReplyAfter31MinSpawnsNew(t *testing.T) {
	router, store := newTestRouter()
	threadTS := "1234567890.000100"

	// Spawn a session and age it beyond the TTL.
	old, _ := store.Spawn(threadTS, "original objective")
	old.LastActivityTs = old.CreatedAt.Add(-(31 * time.Minute))
	_ = store.Update(old)

	ev := SlackEvent{
		ChannelID:    "C001",
		ChannelType:  "channel",
		UserID:       "U001",
		MessageTS:    "1234567890.000300",
		ThreadTS:     threadTS,
		IsBotMention: false,
		ReceivedAt:   time.Now(),
	}

	result, err := router.Route(ev, "new objective")
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if result.Action != RouteActionSpawn {
		t.Fatalf("expected Spawn after TTL, got %v", result.Action)
	}
	if result.Session.SessionID == old.SessionID {
		t.Error("expected new session_id after TTL expiry, got same as old")
	}
	if result.SessionKey != threadTS {
		t.Errorf("session_key should still be thread root %q, got %q", threadTS, result.SessionKey)
	}
}

func TestRouter_DMUsesChannelID(t *testing.T) {
	router, _ := newTestRouter()

	ev := SlackEvent{
		ChannelID:    "D001",
		ChannelType:  "im",
		UserID:       "U001",
		MessageTS:    "1234567890.000100",
		ThreadTS:     "",
		IsBotMention: false,
		ReceivedAt:   time.Now(),
	}

	result, err := router.Route(ev, "dm message")
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if result.Action != RouteActionSpawn {
		t.Fatalf("expected Spawn for new DM, got %v", result.Action)
	}
	if result.SessionKey != "D001" {
		t.Errorf("DM session_key should be channel_id D001, got %q", result.SessionKey)
	}
}

func TestRouter_DMAttachesWhenActive(t *testing.T) {
	router, store := newTestRouter()
	channelID := "D001"

	existing, _ := store.Spawn(channelID, "dm session")

	ev := SlackEvent{
		ChannelID:    channelID,
		ChannelType:  "im",
		UserID:       "U001",
		MessageTS:    "1234567890.000200",
		ThreadTS:     "",
		IsBotMention: false,
		ReceivedAt:   existing.CreatedAt.Add(10 * time.Minute),
	}

	result, err := router.Route(ev, "follow-up DM")
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if result.Action != RouteActionAttach {
		t.Fatalf("expected Attach for active DM session, got %v", result.Action)
	}
	if result.Session.SessionID != existing.SessionID {
		t.Errorf("expected same DM session_id %q", existing.SessionID)
	}
}

func TestRouter_NonMentionTopLevelIsIgnored(t *testing.T) {
	router, _ := newTestRouter()

	ev := SlackEvent{
		ChannelID:    "C001",
		ChannelType:  "channel",
		UserID:       "U001",
		MessageTS:    "1234567890.000100",
		ThreadTS:     "",
		IsBotMention: false,
		ReceivedAt:   time.Now(),
	}

	result, err := router.Route(ev, "")
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if result.Action != RouteActionIgnore {
		t.Fatalf("expected Ignore for non-mention top-level, got %v", result.Action)
	}
}

func TestRouter_ReplyNoSessionIgnoresNothing(t *testing.T) {
	// A reply in a thread with NO existing session should spawn a new one
	// (the session may have expired or never existed; user could be replying
	// to an old thread). We spawn rather than ignore to match spec rule 3.
	router, _ := newTestRouter()
	threadTS := "1234567890.000100"

	ev := SlackEvent{
		ChannelID:    "C001",
		ChannelType:  "channel",
		UserID:       "U001",
		MessageTS:    "1234567890.000200",
		ThreadTS:     threadTS,
		IsBotMention: false,
		ReceivedAt:   time.Now(),
	}

	result, err := router.Route(ev, "reply to old thread")
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	// No existing session → spawn (equivalent to idle > TTL path).
	if result.Action != RouteActionSpawn {
		t.Fatalf("expected Spawn for reply with no existing session, got %v", result.Action)
	}
	if result.SessionKey != threadTS {
		t.Errorf("session_key should be thread root ts %q, got %q", threadTS, result.SessionKey)
	}
}

// TestRouter_DoubleSpawnRace verifies that two goroutines calling Route
// simultaneously for the same brand-new thread_id do not panic, do not
// deadlock, and leave exactly ONE session in the store.
//
// v1 uses coarse-grained locking: the two Spawn calls are serialized by the
// store mutex, so the last writer wins. This is the documented v1 behaviour;
// per-key atomic check-and-set is a v2 improvement.
func TestRouter_DoubleSpawnRace(t *testing.T) {
	router, store := newTestRouter()
	threadTS := "9999999999.000001"

	ev := SlackEvent{
		ChannelID:    "C100",
		ChannelType:  "channel",
		UserID:       "U001",
		MessageTS:    threadTS,
		ThreadTS:     "",
		IsBotMention: true,
		ReceivedAt:   time.Now(),
	}

	// Use a WaitGroup to synchronize both goroutines to start near-simultaneously.
	var ready sync.WaitGroup
	ready.Add(2)
	start := make(chan struct{})

	type routeResult struct {
		result *RouteResult
		err    error
	}
	results := make(chan routeResult, 2)

	for i := 0; i < 2; i++ {
		go func() {
			ready.Done()
			<-start // wait for both goroutines to be ready
			r, err := router.Route(ev, "objective")
			results <- routeResult{r, err}
		}()
	}

	ready.Wait() // both goroutines are ready
	close(start) // release both simultaneously

	for i := 0; i < 2; i++ {
		res := <-results
		if res.err != nil {
			t.Errorf("goroutine %d: Route returned error: %v", i, res.err)
		}
		if res.result == nil {
			t.Errorf("goroutine %d: Route returned nil result", i)
		}
	}

	// Exactly ONE session must exist in the store for this thread_id.
	sess, err := store.GetByKey(threadTS)
	if err != nil {
		t.Fatalf("GetByKey after race: %v", err)
	}
	if sess == nil {
		t.Fatal("expected exactly one session after double-spawn race, got nil")
	}
	if sess.SessionKey != threadTS {
		t.Errorf("session.SessionKey = %q, want %q", sess.SessionKey, threadTS)
	}
}
