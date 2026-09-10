package main

import (
	"testing"
)

func TestParsePromptArgs_RequiresSessionKey(t *testing.T) {
	t.Setenv("CC_SESSION_KEY", "")
	t.Setenv("CC_PROJECT", "")

	_, _, err := parsePromptArgs([]string{"-m", "hello"})
	if err == nil {
		t.Fatal("expected error when session key is missing")
	}
}

func TestParsePromptArgs_UsesEnvFallbacks(t *testing.T) {
	t.Setenv("CC_PROJECT", "demo")
	t.Setenv("CC_SESSION_KEY", "discord:channel-1:user-1")

	req, _, err := parsePromptArgs([]string{"-m", "escalation: task T1 blocked"})
	if err != nil {
		t.Fatalf("parsePromptArgs returned error: %v", err)
	}
	if req.Project != "demo" {
		t.Fatalf("project = %q, want demo", req.Project)
	}
	if req.SessionKey != "discord:channel-1:user-1" {
		t.Fatalf("session key = %q, want discord:channel-1:user-1", req.SessionKey)
	}
}

func TestParsePromptArgs_DefaultsFrom(t *testing.T) {
	req, _, err := parsePromptArgs([]string{"-s", "discord:channel-1:user-1", "-m", "hello"})
	if err != nil {
		t.Fatalf("parsePromptArgs returned error: %v", err)
	}
	if req.From != "hex-events" {
		t.Fatalf("from = %q, want hex-events", req.From)
	}
}

// TestParsePromptArgs_UnknownOptionErrors pins F6: an unknown option-looking
// arg (starts with "-", not exactly "-") must be rejected with an error
// naming it, rather than silently swallowed into the positional message —
// even when -m/CC_SESSION_KEY otherwise make the call look complete.
func TestParsePromptArgs_UnknownOptionErrors(t *testing.T) {
	t.Setenv("CC_SESSION_KEY", "discord:channel-1:user-1")

	_, _, err := parsePromptArgs([]string{"--sessoin", "x", "-m", "hello"})
	if err == nil {
		t.Fatal("expected error for unknown option --sessoin, got nil")
	}
}

// TestParsePromptArgs_DoubleDashAllowsLeadingDash pins F6's escape hatch: a
// "--" separator lets subsequent args be treated as positional text even if
// they start with a dash.
func TestParsePromptArgs_DoubleDashAllowsLeadingDash(t *testing.T) {
	req, _, err := parsePromptArgs([]string{"-s", "discord:channel-1:user-1", "--", "-leading-dash"})
	if err != nil {
		t.Fatalf("parsePromptArgs returned error: %v", err)
	}
	if req.Message != "-leading-dash" {
		t.Fatalf("message = %q, want %q", req.Message, "-leading-dash")
	}
}

func TestParsePromptArgs_PositionalMessage(t *testing.T) {
	req, _, err := parsePromptArgs([]string{"-s", "discord:channel-1:user-1", "escalation:", "task", "T1", "blocked"})
	if err != nil {
		t.Fatalf("parsePromptArgs returned error: %v", err)
	}
	if req.Message != "escalation: task T1 blocked" {
		t.Fatalf("message = %q, want %q", req.Message, "escalation: task T1 blocked")
	}
}
