package session

import (
	"context"
	"log/slog"
	"strings"
	"unicode"
)

// Regime categorizes the current session health state for observability.
// Logged as regime_hint in v1_meter events; no enforcement in v1.4.
type Regime string

const (
	RegimeNormal    Regime = "normal"
	RegimeStrict    Regime = "strict"
	RegimeEmergency Regime = "emergency"
)

// Drift and correction thresholds — placeholders; calibrate from real telemetry post-v1.4.
const (
	DriftThresholdStrict         = 0.40
	DriftThresholdEmergency      = 0.70
	CorrectionThresholdStrict    = 0.20
	CorrectionThresholdEmergency = 0.40
)

// MeterRecord is the structured payload emitted by EmitMeterLog as a "v1_meter" slog event.
// Intentionally separate from TurnLogRecord — TurnLogRecord schema is frozen per turn_log.go:13.
type MeterRecord struct {
	SessionID       string  `json:"session_id"`
	SessionKey      string  `json:"session_key"`
	TurnCount       int     `json:"turn_count"`
	DriftScore      float64 `json:"drift_score"`
	CorrectionRate  float64 `json:"correction_rate"`
	CorrectionCount int     `json:"correction_count"`
	RegimeHint      Regime  `json:"regime_hint"`
}

// tokenize lowercases s, splits on non-letter/non-digit runes, and returns
// the set of words with length ≥ 3.
func tokenize(s string) map[string]struct{} {
	tokens := make(map[string]struct{})
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range words {
		if len([]rune(w)) >= 3 {
			tokens[w] = struct{}{}
		}
	}
	return tokens
}

// jaccard computes the Jaccard similarity coefficient between two token sets.
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	intersection := 0
	for k := range a {
		if _, ok := b[k]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 1.0
	}
	return float64(intersection) / float64(union)
}

// workingSetText concatenates the text content of all working-set items
// (pinned, active turns, optional) for use in drift computation.
func workingSetText(ws *WorkingSet) string {
	var parts []string
	for _, p := range ws.Pinned {
		if p.Text != "" {
			parts = append(parts, p.Text)
		}
	}
	for _, t := range ws.ActiveTurns {
		if t.UserMessage.Text != "" {
			parts = append(parts, t.UserMessage.Text)
		}
	}
	for _, o := range ws.OptionalItems {
		if o.Text != "" {
			parts = append(parts, o.Text)
		}
	}
	return strings.Join(parts, " ")
}

// DriftScore returns the keyword-overlap drift between the working set and the
// root objective. Range [0.0, 1.0]: 0.0 = fully aligned, 1.0 = fully drifted.
//
// Empty objective → 0.0 (cannot measure drift without a goal).
// Empty working set with non-empty objective → 1.0 (no content to align against).
func DriftScore(ws *WorkingSet) float64 {
	objective := strings.TrimSpace(ws.RootObjective)
	if objective == "" {
		return 0.0
	}
	wsText := workingSetText(ws)
	if strings.TrimSpace(wsText) == "" {
		return 1.0
	}
	return 1.0 - jaccard(tokenize(wsText), tokenize(objective))
}

// CorrectionRate returns corrections/turns, or 0.0 if turns is zero (avoids divide-by-zero).
func CorrectionRate(corrections, turns int) float64 {
	if turns == 0 {
		return 0.0
	}
	return float64(corrections) / float64(turns)
}

// RegimeHint maps (drift, rate) to a Regime classification.
// Emergency if either signal crosses the emergency threshold.
// Strict if either crosses the strict threshold.
// Normal otherwise.
func RegimeHint(drift, rate float64) Regime {
	if drift >= DriftThresholdEmergency || rate >= CorrectionThresholdEmergency {
		return RegimeEmergency
	}
	if drift >= DriftThresholdStrict || rate >= CorrectionThresholdStrict {
		return RegimeStrict
	}
	return RegimeNormal
}

// BuildMeterRecord composes all meter signals from sess and ws into a MeterRecord.
func BuildMeterRecord(sess *Session, ws *WorkingSet) MeterRecord {
	drift := DriftScore(ws)
	rate := CorrectionRate(sess.CorrectionCount, sess.TurnCount)
	return MeterRecord{
		SessionID:       sess.SessionID,
		SessionKey:      sess.SessionKey,
		TurnCount:       sess.TurnCount,
		DriftScore:      drift,
		CorrectionRate:  rate,
		CorrectionCount: sess.CorrectionCount,
		RegimeHint:      RegimeHint(drift, rate),
	}
}

// IsCorrection returns true if text appears to be a user correction.
// Detects prefixes: "no,", "no ", "actually", "wait,", "wait ", "/correct".
// Known false positive: "no problem" matches — calibrate post-v1.4.
func IsCorrection(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, prefix := range []string{"no,", "no ", "actually", "wait,", "wait ", "/correct"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// EmitMeterLog emits rec as a structured slog entry at INFO level ("v1_meter").
// All 7 fields are emitted as log attrs. Separate from EmitTurnLog — see turn_log.go:13.
func EmitMeterLog(rec MeterRecord) {
	slog.LogAttrs(context.Background(), slog.LevelInfo, "v1_meter",
		slog.String("session_id", rec.SessionID),
		slog.String("session_key", rec.SessionKey),
		slog.Int("turn_count", rec.TurnCount),
		slog.Float64("drift_score", rec.DriftScore),
		slog.Float64("correction_rate", rec.CorrectionRate),
		slog.Int("correction_count", rec.CorrectionCount),
		slog.String("regime_hint", string(rec.RegimeHint)),
	)
}
