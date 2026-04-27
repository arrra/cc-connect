package core

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/chenhg5/cc-connect/core/hexmem"
	sessv1 "github.com/chenhg5/cc-connect/core/session"
)

// WireV1Store reads CC_CONNECT_SESSIONS_V1 from env and, if "1", constructs
// the v1 store (with a pin store backed by dataDir/pins.json) and attaches it
// to the engine. Returns nil if the flag is unset (no-op).
func WireV1Store(engine *Engine, projectName, dataDir string) error {
	if os.Getenv("CC_CONNECT_SESSIONS_V1") != "1" {
		slog.Debug("v1 sessions disabled (CC_CONNECT_SESSIONS_V1 not set to 1)", "project", projectName)
		return nil
	}
	pinsPath := filepath.Join(dataDir, "pins.json")
	pinStore := sessv1.NewPinStore(pinsPath)
	savedPins, err := pinStore.Load()
	if err != nil {
		slog.Warn("v1 sessions: failed to load saved pins, starting with empty pins", "path", pinsPath, "err", err)
		savedPins = nil
	}
	store := sessv1.NewInMemorySessionStore(pinStore, savedPins)
	engine.SetV1Store(store)
	slog.Info("v1 sessions enabled", "project", projectName, "pins_path", pinsPath)
	return nil
}

// WireHexClient reads CC_CONNECT_HEX_MEMORY + CC_HEX_ROOT from env and, if
// the flag is "1", constructs the hex client and attaches it to the engine.
// Returns nil if the flag is unset.
func WireHexClient(engine *Engine, projectName string) error {
	if os.Getenv("CC_CONNECT_HEX_MEMORY") != "1" {
		return nil
	}
	hexRoot := os.Getenv("CC_HEX_ROOT")
	if hexRoot == "" {
		hexRoot = "/Users/sagarsingh/hex"
	}
	hexCfg := hexmem.Config{
		HexRoot: hexRoot,
		Enabled: true,
	}
	hexClient := hexmem.NewClient(hexCfg)
	engine.SetHexClient(hexClient)
	slog.Info("hex memory enabled", "project", projectName, "hex_root", hexCfg.HexRoot)
	return nil
}

// WireShortcutHandlers iterates platforms, type-asserts MessageShortcutSetter,
// and calls SetMessageShortcutHandler(engine.HandleMessageShortcut) on each.
func WireShortcutHandlers(engine *Engine, projectName string, platforms []Platform) {
	for _, p := range platforms {
		if mss, ok := p.(MessageShortcutSetter); ok {
			mss.SetMessageShortcutHandler(engine.HandleMessageShortcut)
			slog.Debug("v2: wired shortcut handler", "project", projectName)
		}
	}
}
