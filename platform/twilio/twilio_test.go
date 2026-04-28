package twilio

import (
	"testing"
)

func TestInit_MissingAccountSID(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "")
	t.Setenv("TWILIO_AUTH_TOKEN", "")
	t.Setenv("TWILIO_FROM_NUMBER", "")

	a := &TwilioAdapter{}
	if err := a.Init(); err == nil {
		t.Fatal("expected error for missing TWILIO_ACCOUNT_SID, got nil")
	}
}

func TestInit_MissingAuthToken(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "ACtest")
	t.Setenv("TWILIO_AUTH_TOKEN", "")
	t.Setenv("TWILIO_FROM_NUMBER", "")

	a := &TwilioAdapter{}
	if err := a.Init(); err == nil {
		t.Fatal("expected error for missing TWILIO_AUTH_TOKEN, got nil")
	}
}

func TestInit_MissingFromNumber(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "ACtest")
	t.Setenv("TWILIO_AUTH_TOKEN", "secret")
	t.Setenv("TWILIO_FROM_NUMBER", "")

	a := &TwilioAdapter{}
	if err := a.Init(); err == nil {
		t.Fatal("expected error for missing TWILIO_FROM_NUMBER, got nil")
	}
}

func TestInit_Success(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "ACtest123")
	t.Setenv("TWILIO_AUTH_TOKEN", "secret456")
	t.Setenv("TWILIO_FROM_NUMBER", "+19165550100")

	a := &TwilioAdapter{}
	if err := a.Init(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.accountSID != "ACtest123" {
		t.Errorf("accountSID = %q, want %q", a.accountSID, "ACtest123")
	}
	if a.fromNumber != "+19165550100" {
		t.Errorf("fromNumber = %q, want %q", a.fromNumber, "+19165550100")
	}
}

func TestNew_OptsMap(t *testing.T) {
	opts := map[string]any{
		"account_sid": "ACfoo",
		"auth_token":  "bar",
		"from_number": "+15305550100",
	}
	a, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.accountSID != "ACfoo" {
		t.Errorf("accountSID = %q", a.accountSID)
	}
	if a.client == nil {
		t.Error("client should be initialized when creds are present")
	}
}
