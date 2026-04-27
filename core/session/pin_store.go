package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// pinFile is the on-disk JSON envelope for persisted pins.
type pinFile struct {
	Version   int                     `json:"version"`
	PinsByKey map[string][]PinnedItem `json:"pins_by_session_key"`
}

// PinStore persists pin data to a JSON file on disk.
// It does not hold state — callers supply the map to save and receive it on load.
type PinStore struct {
	path string
}

// NewPinStore returns a PinStore that reads/writes to path.
// The parent directory must already exist (or be created by the caller).
func NewPinStore(path string) *PinStore {
	return &PinStore{path: path}
}

// Load reads the pins file from disk and returns the pins map.
// Returns an empty map (not an error) if the file does not yet exist.
func (ps *PinStore) Load() (map[string][]PinnedItem, error) {
	data, err := os.ReadFile(ps.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(map[string][]PinnedItem), nil
		}
		return nil, err
	}
	var f pinFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	if f.PinsByKey == nil {
		return make(map[string][]PinnedItem), nil
	}
	return f.PinsByKey, nil
}

// Save atomically writes all pins to disk.
// It writes to a temp file in the same directory then renames over the target,
// so the file is never left in a partial state on crash.
func (ps *PinStore) Save(pins map[string][]PinnedItem) error {
	f := pinFile{Version: 1, PinsByKey: pins}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}

	dir := filepath.Dir(ps.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-pins-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, ps.path)
}
