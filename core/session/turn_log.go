package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"time"
)

// TurnLogRecord is the structured log record emitted after each Claude Code turn.
// v2's Meter computes session signals from this schema — do not add fields in v1.
type TurnLogRecord struct {
	Timestamp               time.Time     `json:"timestamp"`
	SessionID               string        `json:"session_id"`
	SessionKey              string        `json:"session_key"`
	TurnCount               int           `json:"turn_count"`
	PromptTokens            int           `json:"prompt_tokens"`
	ResponseTokens          int           `json:"response_tokens"`
	ResponseLatencyMs       int64         `json:"response_latency_ms"`
	UserMessageHash         string        `json:"user_message_hash"`
	HexRetrievalTokenCount  int           `json:"hex_retrieval_token_count"`
	ToolResultsCount        int           `json:"tool_results_count"`
	WorkingSetItemCount     int           `json:"working_set_item_count"`
	WorkingSetTokenEstimate int           `json:"working_set_token_estimate"`
	PinnedCount             int           `json:"pinned_count"`
	Kept                    []KeptItem    `json:"kept"`
	Evicted                 []EvictedItem `json:"evicted"`
}

// KeptItem describes an item that survived working-set eviction this turn.
type KeptItem struct {
	Type       string `json:"type"`
	KeptReason string `json:"kept_reason"`
}

// EvictedItem describes an item that was evicted from the working set this turn.
type EvictedItem struct {
	Type          string `json:"type"`
	EvictedReason string `json:"evicted_reason"`
}

// BuildTurnLog constructs a TurnLogRecord from turn data.
//
// promptTokens/responseTokens: pass SDK-reported values if available (>0); if 0
// the function falls back to a len/4 heuristic over promptContent (v1 approximation,
// flagged in PR description).
//
// hexRetrievalTokenCount is always 0 in v1; hex retrieval is external to cc-connect.
func BuildTurnLog(
	sess *Session,
	ws *WorkingSet,
	userMessage string,
	promptContent string,
	promptTokens int,
	responseTokens int,
	toolResultsCount int,
	latencyMs int64,
) TurnLogRecord {
	h := sha256.Sum256([]byte(userMessage))
	userMsgHash := hex.EncodeToString(h[:])

	// Token estimates: prefer SDK values; fall back to len/4 proxy (v1 approximation).
	if promptTokens == 0 && promptContent != "" {
		promptTokens = (len([]rune(promptContent)) + 3) / 4
	}

	wsItemCount := 0
	wsTokenEstimate := 0
	kept := []KeptItem{}
	evicted := []EvictedItem{}

	if ws != nil {
		// root_objective and recent_user_message are always present in v1.
		wsItemCount += 2
		kept = append(kept,
			KeptItem{Type: "root_objective", KeptReason: "always"},
			KeptItem{Type: "recent_user_message", KeptReason: "current_turn"},
		)
		if ws.RecentToolResult != nil {
			wsItemCount++
			kept = append(kept, KeptItem{Type: "recent_tool_result", KeptReason: "last_tool_unconditional"})
		}
		for range ws.Pinned {
			wsItemCount++
			kept = append(kept, KeptItem{Type: "pinned_item", KeptReason: "pinned"})
		}
		// Prior turns are always evicted under the v1 single-turn working-set policy.
		if sess.TurnCount > 1 {
			evicted = append(evicted, EvictedItem{Type: "prior_turn_user", EvictedReason: "older_than_recent"})
		}
		if b, err := json.Marshal(ws); err == nil {
			wsTokenEstimate = (len(b) + 3) / 4
		}
	}

	return TurnLogRecord{
		Timestamp:               time.Now().UTC(),
		SessionID:               sess.SessionID,
		SessionKey:              sess.SessionKey,
		TurnCount:               sess.TurnCount,
		PromptTokens:            promptTokens,
		ResponseTokens:          responseTokens,
		ResponseLatencyMs:       latencyMs,
		UserMessageHash:         userMsgHash,
		HexRetrievalTokenCount:  0,
		ToolResultsCount:        toolResultsCount,
		WorkingSetItemCount:     wsItemCount,
		WorkingSetTokenEstimate: wsTokenEstimate,
		PinnedCount:             len(sess.Pinned),
		Kept:                    kept,
		Evicted:                 evicted,
	}
}

// EmitTurnLog emits rec as a structured slog entry at INFO level ("v1_turn").
// All 15 schema fields are emitted as log attrs. Configure the default slog handler
// to control output format and destination (e.g. slog.NewJSONHandler for structured logs).
func EmitTurnLog(rec TurnLogRecord) {
	slog.LogAttrs(context.Background(), slog.LevelInfo, "v1_turn",
		slog.Time("timestamp", rec.Timestamp),
		slog.String("session_id", rec.SessionID),
		slog.String("session_key", rec.SessionKey),
		slog.Int("turn_count", rec.TurnCount),
		slog.Int("prompt_tokens", rec.PromptTokens),
		slog.Int("response_tokens", rec.ResponseTokens),
		slog.Int64("response_latency_ms", rec.ResponseLatencyMs),
		slog.String("user_message_hash", rec.UserMessageHash),
		slog.Int("hex_retrieval_token_count", rec.HexRetrievalTokenCount),
		slog.Int("tool_results_count", rec.ToolResultsCount),
		slog.Int("working_set_item_count", rec.WorkingSetItemCount),
		slog.Int("working_set_token_estimate", rec.WorkingSetTokenEstimate),
		slog.Int("pinned_count", rec.PinnedCount),
		slog.Any("kept", rec.Kept),
		slog.Any("evicted", rec.Evicted),
	)
}

// HashUserMessage returns the hex-encoded SHA-256 of text (PII-safe).
// Exported for use in tests and downstream consumers.
func HashUserMessage(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}
