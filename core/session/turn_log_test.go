package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestBuildTurnLog_AllFieldsPresent verifies all 15 top-level fields are present
// and have correct types when serialized as JSON.
func TestBuildTurnLog_AllFieldsPresent(t *testing.T) {
	sess := &Session{
		SessionID:      "test-session-uuid",
		SessionKey:     "T12345678.000100",
		RootObjective:  "Help me refactor the auth module",
		TurnCount:      3,
		LastActivityTs: time.Now(),
		CreatedAt:      time.Now(),
		Pinned: []PinnedItem{
			{Text: "always check jwt expiry", Source: "user_explicit", PinnedAt: time.Now(), PinnedBy: "U001"},
		},
	}
	ws := &WorkingSet{
		RootObjective: sess.RootObjective,
		Pinned:        sess.Pinned,
		RecentUserMessage: &UserMessage{
			Text: "what did we decide about token refresh?",
			Ts:   "1714068000.000000",
		},
		RecentToolResult: &ToolResult{
			Tool:    "Bash",
			Summary: "exit 0: tests passed",
			Ts:      "1714068000.111111",
		},
	}

	rec := BuildTurnLog(sess, ws, "what did we decide about token refresh?", "some full prompt content here", 120, 45, 2, 1234)

	// Serialize and parse to validate JSON shape.
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	// Assert all 15 top-level fields present.
	required := []string{
		"timestamp", "session_id", "session_key", "turn_count",
		"prompt_tokens", "response_tokens", "response_latency_ms", "user_message_hash",
		"hex_retrieval_token_count", "tool_results_count", "working_set_item_count",
		"working_set_token_estimate", "pinned_count", "kept", "evicted",
	}
	for _, field := range required {
		if _, exists := parsed[field]; !exists {
			t.Errorf("missing field: %s", field)
		}
	}

	// Spot-check values and types.
	if parsed["session_id"] != "test-session-uuid" {
		t.Errorf("session_id = %v, want test-session-uuid", parsed["session_id"])
	}
	if parsed["session_key"] != "T12345678.000100" {
		t.Errorf("session_key = %v", parsed["session_key"])
	}
	if parsed["turn_count"].(float64) != 3 {
		t.Errorf("turn_count = %v, want 3", parsed["turn_count"])
	}
	if parsed["prompt_tokens"].(float64) != 120 {
		t.Errorf("prompt_tokens = %v, want 120", parsed["prompt_tokens"])
	}
	if parsed["response_tokens"].(float64) != 45 {
		t.Errorf("response_tokens = %v, want 45", parsed["response_tokens"])
	}
	if parsed["response_latency_ms"].(float64) != 1234 {
		t.Errorf("response_latency_ms = %v, want 1234", parsed["response_latency_ms"])
	}
	if parsed["hex_retrieval_token_count"].(float64) != 0 {
		t.Errorf("hex_retrieval_token_count should be 0, got %v", parsed["hex_retrieval_token_count"])
	}
	if parsed["tool_results_count"].(float64) != 2 {
		t.Errorf("tool_results_count = %v, want 2", parsed["tool_results_count"])
	}
	if parsed["pinned_count"].(float64) != 1 {
		t.Errorf("pinned_count = %v, want 1", parsed["pinned_count"])
	}

	// user_message_hash should be a non-empty hex string.
	hashStr, ok := parsed["user_message_hash"].(string)
	if !ok || len(hashStr) != 64 {
		t.Errorf("user_message_hash should be 64-char hex, got %q", hashStr)
	}

	// working_set_item_count: root_objective(1) + recent_user_message(1) + tool_result(1) + pinned(1) = 4.
	if parsed["working_set_item_count"].(float64) != 4 {
		t.Errorf("working_set_item_count = %v, want 4", parsed["working_set_item_count"])
	}

	// kept should be a non-empty array.
	kept, ok := parsed["kept"].([]interface{})
	if !ok || len(kept) == 0 {
		t.Errorf("kept should be a non-empty array, got %v", parsed["kept"])
	}

	// evicted should be an array (may be empty if TurnCount <= 1, but we have 3 here).
	evicted, ok := parsed["evicted"].([]interface{})
	if !ok {
		t.Errorf("evicted should be an array, got %T", parsed["evicted"])
	}
	if len(evicted) == 0 {
		t.Errorf("evicted should be non-empty for TurnCount=3 (prior turns exist)")
	}
}

// TestEmitTurnLog_WritesJSON verifies EmitTurnLog writes a parseable JSON line.
func TestEmitTurnLog_WritesJSON(t *testing.T) {
	sess := &Session{
		SessionID:  "emit-test-id",
		SessionKey: "C999",
		TurnCount:  1,
	}
	ws := &WorkingSet{
		RootObjective:     "test objective",
		RecentUserMessage: &UserMessage{Text: "hello", Ts: "ts1"},
	}

	var buf strings.Builder
	orig := TurnLogOutput
	TurnLogOutput = &buf
	defer func() { TurnLogOutput = orig }()

	rec := BuildTurnLog(sess, ws, "hello", "prompt", 0, 0, 0, 500)
	EmitTurnLog(rec)

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("EmitTurnLog wrote nothing")
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, line)
	}

	// Check session_id round-trips.
	if parsed["session_id"] != "emit-test-id" {
		t.Errorf("session_id = %v", parsed["session_id"])
	}
}

// TestBuildTurnLog_TokenFallback verifies the len/4 estimate is used when
// promptTokens == 0 and no SDK token count is available.
func TestBuildTurnLog_TokenFallback(t *testing.T) {
	sess := &Session{SessionID: "x", SessionKey: "y", TurnCount: 1}
	prompt := "hello world this is a test prompt"

	rec := BuildTurnLog(sess, nil, "msg", prompt, 0, 0, 0, 100)

	expected := (len([]rune(prompt)) + 3) / 4
	if rec.PromptTokens != expected {
		t.Errorf("PromptTokens = %d, want %d (len/4 estimate)", rec.PromptTokens, expected)
	}
}

// TestBuildTurnLog_WorkingSetItemCount verifies working_set_item_count counts all kept items.
func TestBuildTurnLog_WorkingSetItemCount(t *testing.T) {
	sess := &Session{SessionID: "x", SessionKey: "y", TurnCount: 1}

	// No tool result, no pins → 2 items (root_objective + recent_user_message).
	ws := &WorkingSet{
		RootObjective:     "test",
		RecentUserMessage: &UserMessage{Text: "msg", Ts: "ts"},
	}
	rec := BuildTurnLog(sess, ws, "msg", "", 0, 0, 0, 0)
	if rec.WorkingSetItemCount != 2 {
		t.Errorf("no tool/no pins: WorkingSetItemCount = %d, want 2", rec.WorkingSetItemCount)
	}

	// With tool result → 3 items.
	ws.RecentToolResult = &ToolResult{Tool: "bash", Summary: "ok", Ts: "ts2"}
	rec = BuildTurnLog(sess, ws, "msg", "", 0, 0, 0, 0)
	if rec.WorkingSetItemCount != 3 {
		t.Errorf("with tool: WorkingSetItemCount = %d, want 3", rec.WorkingSetItemCount)
	}

	// With 2 pins → 5 items.
	sess.Pinned = []PinnedItem{
		{Text: "pin1", Source: "user_explicit", PinnedAt: time.Now(), PinnedBy: "U1"},
		{Text: "pin2", Source: "user_explicit", PinnedAt: time.Now(), PinnedBy: "U1"},
	}
	ws.Pinned = sess.Pinned
	rec = BuildTurnLog(sess, ws, "msg", "", 0, 0, 0, 0)
	if rec.WorkingSetItemCount != 5 {
		t.Errorf("with tool+2 pins: WorkingSetItemCount = %d, want 5", rec.WorkingSetItemCount)
	}
}

// TestHashUserMessage verifies SHA-256 hash is stable and PII-safe.
func TestHashUserMessage(t *testing.T) {
	h1 := HashUserMessage("hello world")
	h2 := HashUserMessage("hello world")
	h3 := HashUserMessage("different text")

	if h1 != h2 {
		t.Error("HashUserMessage is not deterministic")
	}
	if h1 == h3 {
		t.Error("different inputs produced same hash")
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex, got len=%d", len(h1))
	}
	// Verify hash does not contain the input text.
	if strings.Contains(h1, "hello") {
		t.Error("hash contains original text (not PII-safe)")
	}
}
