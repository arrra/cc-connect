package core

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chenhg5/cc-connect/core/hexmem"
)

// compactOutput3 is a memory_search.py --compact style response with 3 results.
// Used by auto-recall tests to simulate a populated hex store.
const compactOutput3 = `    [1] source/one > heading1  (score: 1.00)
        content one

    [2] source/two > heading2  (score: 0.90)
        content two

    [3] source/three > heading3  (score: 0.80)
        content three
`

// TestAutoRecall_FirstTurnInjectsHexResults verifies that applyAutoRecall
// prepends a <cc-connect:hex_memory> block when TurnCount==0 and hex is enabled.
func TestAutoRecall_FirstTurnInjectsHexResults(t *testing.T) {
	const sessionKey = "slack:C111:T222"
	const basePrompt = "base prompt"

	var calls atomic.Int32
	var capturedQuery string

	mockExec := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls.Add(1)
		// args: srchPath, --compact, --top, 5, <query>
		if len(args) >= 5 {
			capturedQuery = args[4]
		}
		return []byte(compactOutput3), nil
	}

	e, store, _ := newV1TestEngine(t, true)
	if _, err := store.Spawn(sessionKey, "test objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	hexClient := hexmem.NewClientForTest("/fake/hex", "/fake/save.py", "/fake/search.py", mockExec)
	e.SetHexClient(hexClient)

	result := e.applyAutoRecall(sessionKey, basePrompt)

	if calls.Load() != 1 {
		t.Errorf("hex Search called %d times, want 1", calls.Load())
	}
	wantQuery := hexmem.ChannelID(sessionKey) // "C111"
	if capturedQuery != wantQuery {
		t.Errorf("hex Search query = %q, want %q", capturedQuery, wantQuery)
	}
	if !strings.HasPrefix(result, "<cc-connect:hex_memory>") {
		t.Errorf("result should start with <cc-connect:hex_memory>, got:\n%s", result)
	}
	if !strings.Contains(result, "[hex-recall] content one") {
		t.Errorf("result missing [hex-recall] content one, got:\n%s", result)
	}
	if !strings.Contains(result, "[hex-recall] content two") {
		t.Errorf("result missing [hex-recall] content two, got:\n%s", result)
	}
	if !strings.Contains(result, "[hex-recall] content three") {
		t.Errorf("result missing [hex-recall] content three, got:\n%s", result)
	}
	if !strings.HasSuffix(result, basePrompt) {
		t.Errorf("result should end with base prompt, got:\n%s", result)
	}
}

// TestAutoRecall_LaterTurns_NoRecall verifies that applyAutoRecall skips hex
// Search on turn 2+ (TurnCount > 0) — auto-recall is one-shot per session.
func TestAutoRecall_LaterTurns_NoRecall(t *testing.T) {
	const sessionKey = "slack:C222:T333"
	const basePrompt = "turn 2 prompt"

	var calls atomic.Int32
	mockExec := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		calls.Add(1)
		return []byte(compactOutput3), nil
	}

	e, store, _ := newV1TestEngine(t, true)
	if _, err := store.Spawn(sessionKey, "test objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// Advance past turn 0 — now TurnCount==1, auto-recall should not fire.
	if _, err := store.IncrementTurn(sessionKey); err != nil {
		t.Fatalf("IncrementTurn: %v", err)
	}

	hexClient := hexmem.NewClientForTest("/fake/hex", "/fake/save.py", "/fake/search.py", mockExec)
	e.SetHexClient(hexClient)

	result := e.applyAutoRecall(sessionKey, basePrompt)

	if calls.Load() != 0 {
		t.Errorf("hex Search called %d times on turn 2, want 0", calls.Load())
	}
	if result != basePrompt {
		t.Errorf("result = %q, want %q (no hex block on later turns)", result, basePrompt)
	}
}

// TestAutoRecall_HexDisabled_NoQuery verifies that applyAutoRecall is a no-op
// when no hex client is wired — no panic, prompt unchanged.
func TestAutoRecall_HexDisabled_NoQuery(t *testing.T) {
	const sessionKey = "slack:C333:T444"
	const basePrompt = "no hex prompt"

	e, store, _ := newV1TestEngine(t, true)
	if _, err := store.Spawn(sessionKey, "test objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// hexClient is intentionally NOT set — e.hexClient == nil.

	sess, err := store.GetByKey(sessionKey)
	if err != nil || sess == nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if sess.TurnCount != 0 {
		t.Fatalf("expected TurnCount==0, got %d", sess.TurnCount)
	}

	result := e.applyAutoRecall(sessionKey, basePrompt)

	if result != basePrompt {
		t.Errorf("result = %q, want %q (hex disabled)", result, basePrompt)
	}
}

