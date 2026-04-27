package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

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
		// Strip leading slash to match builtinCommands naming convention.
		cmd := sc.Command
		if len(cmd) > 0 && cmd[0] == '/' {
			cmd = cmd[1:]
		}

		// /btw is handled by isBtwCommand, not builtinCommands.
		if cmd == "btw" {
			continue
		}

		if !registered[cmd] {
			t.Errorf("manifest has /%s but it is not in builtinCommands — add it or remove it from the manifest", cmd)
		}
	}
}
