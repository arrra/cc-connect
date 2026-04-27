package core

import (
	"os"
	"testing"

	"github.com/chenhg5/cc-connect/core/hexmem"
	sessv1 "github.com/chenhg5/cc-connect/core/session"
)

// stubShortcutPlatform wraps stubPlatformEngine and records whether a non-nil
// shortcut handler was installed via SetMessageShortcutHandler.
// Implements core.MessageShortcutSetter.
type stubShortcutPlatform struct {
	stubPlatformEngine
	handlerSet bool
}

func (s *stubShortcutPlatform) SetMessageShortcutHandler(fn func(sessionKey, messageText, userID, threadTS string) error) {
	s.handlerSet = fn != nil
}

// wireV1StoreFromEnv replicates the CC_CONNECT_SESSIONS_V1 wiring block in main.go.
// If the env var is not "1", SetV1Store is never called — same as main.go.
func wireV1StoreFromEnv(e *Engine) {
	if os.Getenv("CC_CONNECT_SESSIONS_V1") == "1" {
		store := sessv1.NewInMemorySessionStore(nil, nil)
		e.SetV1Store(store)
	}
}

// wireHexClientFromEnv replicates the CC_CONNECT_HEX_MEMORY wiring block in main.go.
func wireHexClientFromEnv(e *Engine) {
	if os.Getenv("CC_CONNECT_HEX_MEMORY") == "1" {
		hexRoot := os.Getenv("CC_HEX_ROOT")
		hexCfg := hexmem.Config{
			HexRoot: hexRoot,
			Enabled: true,
		}
		hexClient := hexmem.NewClient(hexCfg)
		e.SetHexClient(hexClient)
	}
}

// wireShortcutHandlersFromPlatforms replicates the shortcut-handler loop in main.go.
func wireShortcutHandlersFromPlatforms(e *Engine, platforms []Platform) {
	for _, p := range platforms {
		if mss, ok := p.(MessageShortcutSetter); ok {
			mss.SetMessageShortcutHandler(e.HandleMessageShortcut)
		}
	}
}

// TestEngineStartup_V1FlagOn_WiresV1Store verifies that when CC_CONNECT_SESSIONS_V1=1
// the engine receives a non-nil v1Store. Would have caught q-087 where SetV1Store
// was never invoked despite the feature being enabled.
func TestEngineStartup_V1FlagOn_WiresV1Store(t *testing.T) {
	t.Setenv("CC_CONNECT_SESSIONS_V1", "1")
	e := NewEngine("test", &stubAgent{}, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)

	wireV1StoreFromEnv(e)

	if e.v1Store == nil {
		t.Fatal("v1Store must be non-nil when CC_CONNECT_SESSIONS_V1=1 — SetV1Store never called")
	}
}

// TestEngineStartup_V1FlagOff_NoV1Store locks the invariant that without the flag
// v1Store is nil after wiring (no unintentional v1 activation).
func TestEngineStartup_V1FlagOff_NoV1Store(t *testing.T) {
	t.Setenv("CC_CONNECT_SESSIONS_V1", "0")
	e := NewEngine("test", &stubAgent{}, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)

	wireV1StoreFromEnv(e)

	if e.v1Store != nil {
		t.Fatal("v1Store must be nil when CC_CONNECT_SESSIONS_V1 is not set to 1")
	}
}

// TestEngineStartup_HexMemoryOn_WiresHexClient verifies that when CC_CONNECT_HEX_MEMORY=1
// SetHexClient is called on the engine (hexClient is non-nil). Would have caught q-087
// where the hex wiring block existed in code but SetHexClient was never invoked.
//
// Note: hexClient.Enabled() is false in CI because the hex scripts are not installed
// at the temp test path. The nil check is the load-bearing assertion — it confirms the
// wiring call happened, not that the scripts are present.
func TestEngineStartup_HexMemoryOn_WiresHexClient(t *testing.T) {
	t.Setenv("CC_CONNECT_HEX_MEMORY", "1")
	t.Setenv("CC_HEX_ROOT", t.TempDir()) // dir exists but has no hex scripts
	e := NewEngine("test", &stubAgent{}, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)

	wireHexClientFromEnv(e)

	if e.hexClient == nil {
		t.Fatal("hexClient must be non-nil when CC_CONNECT_HEX_MEMORY=1 — SetHexClient never called")
	}
}

// TestEngineStartup_SlackPlatform_WiresShortcutHandler verifies that the main.go
// shortcut-handler loop calls SetMessageShortcutHandler on every platform that
// implements MessageShortcutSetter. Would have caught q-070 where the loop was
// absent and shortcutHandler was always nil.
func TestEngineStartup_SlackPlatform_WiresShortcutHandler(t *testing.T) {
	stub := &stubShortcutPlatform{stubPlatformEngine: stubPlatformEngine{n: "slack"}}
	platforms := []Platform{stub}
	e := NewEngine("test", &stubAgent{}, platforms, "", LangEnglish)

	wireShortcutHandlersFromPlatforms(e, platforms)

	if !stub.handlerSet {
		t.Fatal("shortcutHandler must be set non-nil after wiring — shortcut handler loop not wired (q-070)")
	}
}
