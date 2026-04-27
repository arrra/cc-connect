package session

import (
	"testing"
	"time"
)

func pinnedItem(text string, tier Tier) PinnedItem {
	return PinnedItem{
		Text:     text,
		Source:   "test",
		PinnedAt: time.Now(),
		PinnedBy: "U001",
		Tier:     tier,
	}
}

func turnSnap(text string, num int, tier Tier) TurnSnapshot {
	return TurnSnapshot{
		UserMessage: UserMessage{Text: text, Ts: "ts"},
		TurnNum:     num,
		Tier:        tier,
	}
}

// TestBuildWorkingSet_PinnedDefault: PinnedItem with empty Tier → appears in ws.Pinned.
func TestBuildWorkingSet_PinnedDefault(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	key := "slack:T001:tier-default"
	sess, _ := store.Spawn(key, "obj")
	sess.Pinned = []PinnedItem{pinnedItem("keep me", "")}
	_ = store.Update(sess)

	current, _ := store.GetByKey(key)
	ws := BuildWorkingSet(current, &UserMessage{Text: "hi", Ts: "ts"})

	if len(ws.Pinned) != 1 || ws.Pinned[0].Text != "keep me" {
		t.Errorf("expected 1 pinned item 'keep me', got %v", ws.Pinned)
	}
	if len(ws.QuarantinedItems) != 0 {
		t.Errorf("expected no quarantined items, got %v", ws.QuarantinedItems)
	}
	if len(ws.OptionalItems) != 0 {
		t.Errorf("expected no optional items, got %v", ws.OptionalItems)
	}
}

// TestBuildWorkingSet_QuarantinedItem: TierQuarantined → ws.QuarantinedItems, NOT ws.Pinned.
func TestBuildWorkingSet_QuarantinedItem(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	key := "slack:T002:tier-quarantined"
	sess, _ := store.Spawn(key, "obj")
	sess.Pinned = []PinnedItem{pinnedItem("outdated info", TierQuarantined)}
	_ = store.Update(sess)

	current, _ := store.GetByKey(key)
	ws := BuildWorkingSet(current, &UserMessage{Text: "hi", Ts: "ts"})

	if len(ws.Pinned) != 0 {
		t.Errorf("quarantined item must NOT appear in ws.Pinned, got %v", ws.Pinned)
	}
	if len(ws.QuarantinedItems) != 1 || ws.QuarantinedItems[0].Text != "outdated info" {
		t.Errorf("expected 1 quarantined item, got %v", ws.QuarantinedItems)
	}
}

// TestBuildWorkingSet_OptionalItem: TierOptional → ws.OptionalItems only.
func TestBuildWorkingSet_OptionalItem(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	key := "slack:T003:tier-optional"
	sess, _ := store.Spawn(key, "obj")
	sess.Pinned = []PinnedItem{pinnedItem("example A", TierOptional)}
	_ = store.Update(sess)

	current, _ := store.GetByKey(key)
	ws := BuildWorkingSet(current, &UserMessage{Text: "hi", Ts: "ts"})

	if len(ws.Pinned) != 0 {
		t.Errorf("optional item must NOT appear in ws.Pinned, got %v", ws.Pinned)
	}
	if len(ws.OptionalItems) != 1 || ws.OptionalItems[0].Text != "example A" {
		t.Errorf("expected 1 optional item, got %v", ws.OptionalItems)
	}
}

// TestBuildWorkingSet_OptionalTruncated: MaxOptionalItems+2 optional items → exactly MaxOptionalItems in ws.OptionalItems.
func TestBuildWorkingSet_OptionalTruncated(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	key := "slack:T004:optional-truncated"
	sess, _ := store.Spawn(key, "obj")
	for i := 0; i < MaxOptionalItems+2; i++ {
		sess.Pinned = append(sess.Pinned, pinnedItem("opt item", TierOptional))
	}
	_ = store.Update(sess)

	current, _ := store.GetByKey(key)
	ws := BuildWorkingSet(current, &UserMessage{Text: "hi", Ts: "ts"})

	if len(ws.OptionalItems) != MaxOptionalItems {
		t.Errorf("expected %d optional items (truncated), got %d", MaxOptionalItems, len(ws.OptionalItems))
	}
}

// TestBuildWorkingSet_ActiveWindowTruncates: TurnHistory with ActiveWindowSize+3 TierActive entries
// → ws.ActiveTurns has exactly ActiveWindowSize entries, most-recent-first.
func TestBuildWorkingSet_ActiveWindowTruncates(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	key := "slack:T005:active-window"
	sess, _ := store.Spawn(key, "obj")
	total := ActiveWindowSize + 3
	for i := 1; i <= total; i++ {
		sess.TurnHistory = append(sess.TurnHistory, turnSnap("msg", i, TierActive))
	}
	_ = store.Update(sess)

	current, _ := store.GetByKey(key)
	ws := BuildWorkingSet(current, &UserMessage{Text: "hi", Ts: "ts"})

	if len(ws.ActiveTurns) != ActiveWindowSize {
		t.Fatalf("expected %d active turns, got %d", ActiveWindowSize, len(ws.ActiveTurns))
	}
	// Most-recent-first: first entry should be TurnNum == total.
	if ws.ActiveTurns[0].TurnNum != total {
		t.Errorf("expected most recent turn first (TurnNum=%d), got TurnNum=%d", total, ws.ActiveTurns[0].TurnNum)
	}
	// Last entry should be TurnNum == total - ActiveWindowSize + 1.
	wantLast := total - ActiveWindowSize + 1
	if ws.ActiveTurns[ActiveWindowSize-1].TurnNum != wantLast {
		t.Errorf("expected last active turn TurnNum=%d, got TurnNum=%d", wantLast, ws.ActiveTurns[ActiveWindowSize-1].TurnNum)
	}
}

// TestBuildWorkingSet_ActiveSkipsQuarantinedTurns: mix of TierActive + TierQuarantined
// → ws.ActiveTurns only includes TierActive ones (up to ActiveWindowSize).
func TestBuildWorkingSet_ActiveSkipsQuarantinedTurns(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	key := "slack:T006:active-skip-quarantined"
	sess, _ := store.Spawn(key, "obj")
	// 3 active, 2 quarantined interleaved
	sess.TurnHistory = []TurnSnapshot{
		turnSnap("a1", 1, TierActive),
		turnSnap("q1", 2, TierQuarantined),
		turnSnap("a2", 3, TierActive),
		turnSnap("q2", 4, TierQuarantined),
		turnSnap("a3", 5, TierActive),
	}
	_ = store.Update(sess)

	current, _ := store.GetByKey(key)
	ws := BuildWorkingSet(current, &UserMessage{Text: "hi", Ts: "ts"})

	if len(ws.ActiveTurns) != 3 {
		t.Fatalf("expected 3 active turns (quarantined skipped), got %d", len(ws.ActiveTurns))
	}
	for _, snap := range ws.ActiveTurns {
		if snap.Tier != TierActive {
			t.Errorf("non-active turn %q (tier=%s) leaked into ws.ActiveTurns", snap.UserMessage.Text, snap.Tier)
		}
	}
}

// TestAppendTurn_RollingWindow: AppendTurn called MaxTurnHistory+5 times
// → sess.TurnHistory capped at MaxTurnHistory; oldest entries dropped.
func TestAppendTurn_RollingWindow(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	key := "slack:T007:rolling-window"
	_, err := store.Spawn(key, "obj")
	if err != nil {
		t.Fatal(err)
	}

	total := MaxTurnHistory + 5
	for i := 1; i <= total; i++ {
		snap := TurnSnapshot{
			UserMessage: UserMessage{Text: "msg", Ts: "ts"},
			TurnNum:     i,
			Tier:        TierActive,
		}
		if err := store.AppendTurn(key, snap); err != nil {
			t.Fatalf("AppendTurn %d: %v", i, err)
		}
	}

	sess, err := store.GetByKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.TurnHistory) != MaxTurnHistory {
		t.Errorf("expected TurnHistory capped at %d, got %d", MaxTurnHistory, len(sess.TurnHistory))
	}
	// Oldest should be dropped: first entry should be TurnNum == total - MaxTurnHistory + 1.
	wantFirst := total - MaxTurnHistory + 1
	if sess.TurnHistory[0].TurnNum != wantFirst {
		t.Errorf("expected first TurnNum=%d (oldest dropped), got TurnNum=%d", wantFirst, sess.TurnHistory[0].TurnNum)
	}
}

// TestAppendTurn_SessionNotFound: AppendTurn on non-existent key → ErrSessionNotFound.
func TestAppendTurn_SessionNotFound(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	snap := TurnSnapshot{
		UserMessage: UserMessage{Text: "hi", Ts: "ts"},
		TurnNum:     1,
		Tier:        TierActive,
	}
	err := store.AppendTurn("nonexistent:key", snap)
	if err != ErrSessionNotFound {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}
