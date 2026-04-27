package session

import (
	"encoding/json"
	"log/slog"
	"math"
	"strings"
	"testing"
)

// TestDriftScore_Aligned verifies drift approaches 0.0 when working set closely matches the objective.
func TestDriftScore_Aligned(t *testing.T) {
	ws := &WorkingSet{
		RootObjective: "refactor authentication module",
		Pinned: []PinnedItem{
			{Text: "refactor authentication module"},
		},
	}
	got := DriftScore(ws)
	if got > 0.1 {
		t.Errorf("DriftScore = %.4f, want ~0.0 for fully aligned working set", got)
	}
}

// TestDriftScore_EmptyWorkingSet verifies drift = 1.0 when working set is empty
// but objective is non-empty (no content to align against).
func TestDriftScore_EmptyWorkingSet(t *testing.T) {
	ws := &WorkingSet{
		RootObjective: "refactor authentication module",
	}
	got := DriftScore(ws)
	if got != 1.0 {
		t.Errorf("DriftScore = %.4f, want 1.0 for empty working set with non-empty objective", got)
	}
}

// TestDriftScore_EmptyObjective verifies drift = 0.0 when objective is empty
// (cannot measure drift without a goal).
func TestDriftScore_EmptyObjective(t *testing.T) {
	ws := &WorkingSet{
		RootObjective: "",
		Pinned: []PinnedItem{
			{Text: "some content here"},
		},
	}
	got := DriftScore(ws)
	if got != 0.0 {
		t.Errorf("DriftScore = %.4f, want 0.0 for empty objective", got)
	}
}

// TestDriftScore_PartialOverlap verifies drift is strictly between 0.0 and 1.0
// when working set shares only some tokens with the objective.
// Objective tokens: {refactor, authentication, module}
// Working set tokens: {refactor, database, migration} → intersection={refactor} → Jaccard=1/5≈0.2 → drift≈0.8
func TestDriftScore_PartialOverlap(t *testing.T) {
	ws := &WorkingSet{
		RootObjective: "refactor authentication module",
		Pinned: []PinnedItem{
			{Text: "refactor database migration"},
		},
	}
	got := DriftScore(ws)
	if got <= 0.0 || got >= 1.0 {
		t.Errorf("DriftScore = %.4f, want value in (0.0, 1.0) for partial overlap", got)
	}
}

// TestCorrectionRate_ZeroCorrections verifies rate = 0.0 when no corrections over several turns.
func TestCorrectionRate_ZeroCorrections(t *testing.T) {
	got := CorrectionRate(0, 5)
	if got != 0.0 {
		t.Errorf("CorrectionRate(0, 5) = %.4f, want 0.0", got)
	}
}

// TestCorrectionRate_ZeroTurns verifies rate = 0.0 when turns = 0 (no divide-by-zero).
func TestCorrectionRate_ZeroTurns(t *testing.T) {
	got := CorrectionRate(0, 0)
	if got != 0.0 {
		t.Errorf("CorrectionRate(0, 0) = %.4f, want 0.0 (guard against divide-by-zero)", got)
	}
}

// TestCorrectionRate_AllCorrections verifies rate = 1.0 when every turn is a correction.
func TestCorrectionRate_AllCorrections(t *testing.T) {
	got := CorrectionRate(3, 3)
	if got != 1.0 {
		t.Errorf("CorrectionRate(3, 3) = %.4f, want 1.0", got)
	}
}

// TestRegimeHint_Normal verifies Normal regime for low drift and low correction rate.
func TestRegimeHint_Normal(t *testing.T) {
	got := RegimeHint(0.1, 0.05)
	if got != RegimeNormal {
		t.Errorf("RegimeHint(0.1, 0.05) = %q, want %q", got, RegimeNormal)
	}
}

// TestRegimeHint_StrictViaDrift verifies Strict regime when drift exceeds strict threshold.
func TestRegimeHint_StrictViaDrift(t *testing.T) {
	got := RegimeHint(DriftThresholdStrict+0.01, 0.05)
	if got != RegimeStrict {
		t.Errorf("RegimeHint(drift=%.2f, rate=0.05) = %q, want %q",
			DriftThresholdStrict+0.01, got, RegimeStrict)
	}
}

// TestRegimeHint_StrictViaRate verifies Strict regime when correction rate exceeds strict threshold.
func TestRegimeHint_StrictViaRate(t *testing.T) {
	got := RegimeHint(0.1, CorrectionThresholdStrict+0.01)
	if got != RegimeStrict {
		t.Errorf("RegimeHint(drift=0.1, rate=%.2f) = %q, want %q",
			CorrectionThresholdStrict+0.01, got, RegimeStrict)
	}
}

// TestRegimeHint_EmergencyViaDrift verifies Emergency regime when drift exceeds emergency threshold.
func TestRegimeHint_EmergencyViaDrift(t *testing.T) {
	got := RegimeHint(DriftThresholdEmergency+0.01, 0.05)
	if got != RegimeEmergency {
		t.Errorf("RegimeHint(drift=%.2f, rate=0.05) = %q, want %q",
			DriftThresholdEmergency+0.01, got, RegimeEmergency)
	}
}

// TestRegimeHint_EmergencyViaRate verifies Emergency regime when correction rate exceeds emergency threshold.
func TestRegimeHint_EmergencyViaRate(t *testing.T) {
	got := RegimeHint(0.1, CorrectionThresholdEmergency+0.01)
	if got != RegimeEmergency {
		t.Errorf("RegimeHint(drift=0.1, rate=%.2f) = %q, want %q",
			CorrectionThresholdEmergency+0.01, got, RegimeEmergency)
	}
}

// TestIsCorrection_Detects verifies correction prefixes are detected.
func TestIsCorrection_Detects(t *testing.T) {
	cases := []struct {
		text string
		desc string
	}{
		{"actually that was wrong", "starts with actually"},
		{"Actually, I meant something else", "starts with Actually (case insensitive)"},
		{"no, that's not right", "starts with no,"},
		{"/correct please fix this", "starts with /correct"},
		{"wait, let me rethink", "starts with wait,"},
		{"wait I meant to say", "starts with wait "},
		{"no wait, that's wrong", "starts with no "},
	}
	for _, tc := range cases {
		if !IsCorrection(tc.text) {
			t.Errorf("IsCorrection(%q) = false, want true (%s)", tc.text, tc.desc)
		}
	}
}

// TestIsCorrection_Rejects verifies non-correction messages are not flagged.
func TestIsCorrection_Rejects(t *testing.T) {
	cases := []struct {
		text string
		desc string
	}{
		{"I'd like to add a new feature", "neutral request"},
		{"Can you help me with this?", "question"},
		{"please explain how this works", "explanation request"},
		{"yes, that looks great", "affirmation"},
	}
	for _, tc := range cases {
		if IsCorrection(tc.text) {
			t.Errorf("IsCorrection(%q) = true, want false (%s)", tc.text, tc.desc)
		}
	}
}

// TestBuildMeterRecord_Integration verifies all 7 fields are populated correctly.
// Uses mock Session (TurnCount=5, CorrectionCount=1) → expects correction_rate≈0.2.
func TestBuildMeterRecord_Integration(t *testing.T) {
	sess := &Session{
		SessionID:       "test-session-uuid",
		SessionKey:      "slack:C123:1234.5678",
		TurnCount:       5,
		CorrectionCount: 1,
		RootObjective:   "refactor authentication module",
		Pinned: []PinnedItem{
			{Text: "always check jwt expiry"},
		},
	}
	ws := &WorkingSet{
		RootObjective: sess.RootObjective,
		Pinned:        sess.Pinned,
	}

	rec := BuildMeterRecord(sess, ws)

	if rec.SessionID != sess.SessionID {
		t.Errorf("SessionID = %q, want %q", rec.SessionID, sess.SessionID)
	}
	if rec.SessionKey != sess.SessionKey {
		t.Errorf("SessionKey = %q, want %q", rec.SessionKey, sess.SessionKey)
	}
	if rec.TurnCount != 5 {
		t.Errorf("TurnCount = %d, want 5", rec.TurnCount)
	}
	if rec.CorrectionCount != 1 {
		t.Errorf("CorrectionCount = %d, want 1", rec.CorrectionCount)
	}
	// correction_rate = 1/5 = 0.2
	wantRate := 0.2
	if math.Abs(rec.CorrectionRate-wantRate) > 0.001 {
		t.Errorf("CorrectionRate = %.4f, want ~%.4f (1/5)", rec.CorrectionRate, wantRate)
	}
	if rec.DriftScore < 0.0 || rec.DriftScore > 1.0 {
		t.Errorf("DriftScore = %.4f, must be in [0.0, 1.0]", rec.DriftScore)
	}
	validRegimes := map[Regime]bool{RegimeNormal: true, RegimeStrict: true, RegimeEmergency: true}
	if !validRegimes[rec.RegimeHint] {
		t.Errorf("RegimeHint = %q, must be one of normal/strict/emergency", rec.RegimeHint)
	}
}

// TestEmitMeterLog_SlogFields verifies EmitMeterLog emits "v1_meter" with all 7 schema fields
// routed through slog (not stdout directly), mirroring the v1_turn pattern.
func TestEmitMeterLog_SlogFields(t *testing.T) {
	var buf strings.Builder
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	rec := MeterRecord{
		SessionID:       "emit-test-id",
		SessionKey:      "C999",
		TurnCount:       3,
		DriftScore:      0.25,
		CorrectionRate:  0.1,
		CorrectionCount: 1,
		RegimeHint:      RegimeNormal,
	}
	EmitMeterLog(rec)

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("EmitMeterLog wrote nothing to slog")
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		t.Fatalf("slog output is not valid JSON: %v\noutput: %s", err, line)
	}

	if parsed["msg"] != "v1_meter" {
		t.Errorf("slog record msg = %q, want %q", parsed["msg"], "v1_meter")
	}

	required := []string{
		"session_id", "session_key", "turn_count",
		"drift_score", "correction_rate", "correction_count", "regime_hint",
	}
	for _, field := range required {
		if _, exists := parsed[field]; !exists {
			t.Errorf("slog output missing field: %s", field)
		}
	}

	if parsed["session_id"] != "emit-test-id" {
		t.Errorf("session_id = %v, want emit-test-id", parsed["session_id"])
	}
	if parsed["regime_hint"] != "normal" {
		t.Errorf("regime_hint = %v, want normal", parsed["regime_hint"])
	}
}
