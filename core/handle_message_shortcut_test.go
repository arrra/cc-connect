package core

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	sessv1 "github.com/chenhg5/cc-connect/core/session"
)

// TestHandleMessageShortcut_PinsMessageText verifies that HandleMessageShortcut
// creates a pin with the expected fields and spawns the session.
func TestHandleMessageShortcut_PinsMessageText(t *testing.T) {
	e, store, _ := newV1TestEngine(t, true)

	err := e.HandleMessageShortcut("slack:C1", "Important message body", "U001", "1234.5678")
	if err != nil {
		t.Fatalf("HandleMessageShortcut: %v", err)
	}

	sess, err := store.GetByKey("slack:C1")
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if len(sess.Pinned) != 1 {
		t.Fatalf("expected 1 pin, got %d", len(sess.Pinned))
	}
	pin := sess.Pinned[0]
	if pin.Text != "Important message body" {
		t.Errorf("pin.Text = %q, want %q", pin.Text, "Important message body")
	}
	if pin.PinnedVia != "message_shortcut" {
		t.Errorf("pin.PinnedVia = %q, want %q", pin.PinnedVia, "message_shortcut")
	}
	if pin.PinnedBy != "U001" {
		t.Errorf("pin.PinnedBy = %q, want %q", pin.PinnedBy, "U001")
	}
}

// TestHandleMessageShortcut_PinCapEnforced verifies that the 21st call returns
// ErrPinLimitReached and does not breach the cap.
func TestHandleMessageShortcut_PinCapEnforced(t *testing.T) {
	e, store, _ := newV1TestEngine(t, true)

	const key = "slack:C2"
	if _, _, err := store.SpawnOrAttach(key, "test objective"); err != nil {
		t.Fatalf("SpawnOrAttach: %v", err)
	}

	for i := range sessv1.MaxPinsPerSession {
		if err := e.HandleMessageShortcut(key, fmt.Sprintf("message %d", i), "U001", ""); err != nil {
			t.Fatalf("pin %d: unexpected error: %v", i, err)
		}
	}

	err := e.HandleMessageShortcut(key, "overflow message", "U001", "")
	if !errors.Is(err, sessv1.ErrPinLimitReached) {
		t.Errorf("21st pin: got %v, want ErrPinLimitReached", err)
	}

	sess, err := store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if len(sess.Pinned) != sessv1.MaxPinsPerSession {
		t.Errorf("after cap breach attempt: %d pins, want %d (cap must not be breached)", len(sess.Pinned), sessv1.MaxPinsPerSession)
	}
}

// TestHandleMessageShortcut_FlagOff_ReturnsError verifies that calling
// HandleMessageShortcut with no v1 store installed returns a clear error.
func TestHandleMessageShortcut_FlagOff_ReturnsError(t *testing.T) {
	e, _, _ := newV1TestEngine(t, false)

	err := e.HandleMessageShortcut("slack:C3", "some message", "U001", "")
	if err == nil {
		t.Fatal("expected error when v1Store is nil, got nil")
	}
	if !strings.Contains(err.Error(), "CC_CONNECT_SESSIONS_V1") {
		t.Errorf("error should mention CC_CONNECT_SESSIONS_V1, got: %v", err)
	}
}
