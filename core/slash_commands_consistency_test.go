package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// extractCommandName mirrors the dispatch logic in platform/slack/slack.go:
// it strips the leading "/" and, if the result has a "cc" prefix whose
// remainder matches a known builtin, strips that prefix too. This lets the
// consistency test handle cc-prefixed manifest commands (/ccforget → forget).
func extractCommandName(slashCommand string) string {
	name := strings.TrimPrefix(slashCommand, "/")
	if stripped := strings.TrimPrefix(name, "cc"); stripped != name && isBuiltinCommand(stripped) {
		return stripped
	}
	return name
}

// TestSlashCommands_ManifestMatchesBuiltins asserts that every slash command in
// the Slack app manifest (except /btw, which has dedicated handling via
// isBtwCommand) is registered in builtinCommands and therefore reachable via
// text dispatch.
//
// This test is the narrower (manifest → builtinCommands) direction. The wider
// direction (builtinCommands → manifest) is intentionally not enforced here
// because not all builtinCommands are exposed as Slack slash commands (e.g.
// heartbeat, show, dir, tts are internal-only).
func TestSlashCommands_ManifestMatchesBuiltins(t *testing.T) {
	_, callerFile, _, _ := runtime.Caller(0)
	manifestPath := filepath.Join(filepath.Dir(callerFile), "..", "docs", "slack-app-manifest.json")

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	var manifest struct {
		Features struct {
			SlashCommands []struct {
				Command string `json:"command"`
			} `json:"slash_commands"`
		} `json:"features"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	// Build a set of all names (IDs + aliases) registered in builtinCommands.
	registered := make(map[string]bool)
	for _, bc := range builtinCommands {
		for _, name := range bc.names {
			registered[name] = true
		}
		registered[bc.id] = true
	}

	for _, sc := range manifest.Features.SlashCommands {
		// /btw is handled by isBtwCommand, not builtinCommands.
		if sc.Command == "/btw" {
			continue
		}

		// extractCommandName mirrors the dispatch logic: strips "/" then strips
		// "cc" prefix when the remainder matches a known builtin.
		cmd := extractCommandName(sc.Command)

		if !registered[cmd] {
			t.Errorf("manifest has %s but it is not in builtinCommands — add it or remove it from the manifest", sc.Command)
		}
	}
}

// TestSlashCommands_CCPrefixedRoute is a table-driven unit test for
// extractCommandName, verifying that cc-prefixed slash commands resolve to
// their canonical builtin names while non-builtin cc-prefixed names are left
// intact.
//
// TODO: add an integration-level test asserting that the engine's handleCommand
// receives the canonical (stripped) name once a fake Slack platform is available.
func TestSlashCommands_CCPrefixedRoute(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		// bare names pass through unchanged
		{"/forget", "forget"},
		{"/recall", "recall"},
		{"/pin", "pin"},
		// cc-prefixed builtins → canonical name
		{"/ccforget", "forget"},
		{"/ccrecall", "recall"},
		{"/ccreset-scope", "reset-scope"},
		{"/ccpromote", "promote"},
		{"/ccname", "name"},
		{"/ccsearch", "search"},
		{"/ccstatus", "status"},
		{"/cchistory", "history"},
		{"/ccnew", "new"},
		{"/cchelp", "help"},
		{"/cccommands", "commands"},
		// cc-prefixed name that is NOT a builtin — must NOT strip
		{"/ccmd-custom", "ccmd-custom"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := extractCommandName(tc.input)
			if got != tc.want {
				t.Errorf("extractCommandName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
