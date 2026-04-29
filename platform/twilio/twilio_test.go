package twilio

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/url"
	"strings"
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

func TestVerifyTwilioSignature_SingleValues(t *testing.T) {
	token := "test_auth_token"
	rawURL := "https://example.com/twilio/inbound"
	params := url.Values{
		"MessageSid": {"SM123"},
		"From":       {"+19165550100"},
		"Body":       {"hello"},
	}
	sig := computeSig(token, rawURL, params)
	if !verifyTwilioSignature(token, rawURL, params, sig) {
		t.Error("expected valid signature for single-value params")
	}
}

func TestVerifyTwilioSignature_MultiValuedParams(t *testing.T) {
	// Twilio MMS sends MediaUrl0, MediaUrl1 etc as separate params.
	// Signature algorithm requires ALL values for each key concatenated.
	token := "test_auth_token"
	rawURL := "https://example.com/twilio/inbound"
	params := url.Values{
		"MessageSid": {"MM456"},
		"From":       {"+19165550100"},
		"MediaUrl":   {"https://cdn.twilio.com/image0.jpg", "https://cdn.twilio.com/image1.jpg"},
		"NumMedia":   {"2"},
	}
	sig := computeSig(token, rawURL, params)
	if !verifyTwilioSignature(token, rawURL, params, sig) {
		t.Error("expected valid signature for multi-valued params (all values concatenated)")
	}
}

func TestVerifyTwilioSignature_FirstValueOnlyFails(t *testing.T) {
	// Builds a "broken" signature (first value only) and asserts verifyTwilioSignature rejects it,
	// confirming the multi-value fix is active.
	token := "test_auth_token"
	rawURL := "https://example.com/twilio/inbound"
	params := url.Values{
		"MediaUrl": {"https://cdn.twilio.com/image0.jpg", "https://cdn.twilio.com/image1.jpg"},
	}

	var sb strings.Builder
	sb.WriteString(rawURL)
	sb.WriteString("MediaUrl")
	sb.WriteString(params["MediaUrl"][0]) // first value only — intentionally broken
	mac := hmac.New(sha1.New, []byte(token))
	mac.Write([]byte(sb.String()))
	brokenSig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if verifyTwilioSignature(token, rawURL, params, brokenSig) {
		t.Error("first-value-only signature must be rejected when params have multiple values")
	}
}

func TestVerifyTwilioSignature_InvalidSignature(t *testing.T) {
	token := "test_auth_token"
	rawURL := "https://example.com/twilio/inbound"
	params := url.Values{"From": {"+19165550100"}}
	if verifyTwilioSignature(token, rawURL, params, "badsig") {
		t.Error("invalid signature must be rejected")
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

func TestMaskPhone(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"+19165550123", "+19*****0123"},
		{"short", "***"},
		{"1234567", "***"}, // exactly 7 chars — below threshold
		{"12345678", "123*5678"}, // exactly 8 chars — at threshold
	}
	for _, tc := range cases {
		if got := maskPhone(tc.input); got != tc.want {
			t.Errorf("maskPhone(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestInitiateCallWithTwiML_MissingSagarCell(t *testing.T) {
	t.Setenv("SAGAR_CELL_NUMBER", "")
	a := &TwilioAdapter{fromNumber: "+15305550100"}
	_, err := a.InitiateCallWithTwiML("+19165550100", "<Response/>")
	if err == nil {
		t.Fatal("expected error when SAGAR_CELL_NUMBER is unset")
	}
	if !strings.Contains(err.Error(), "SAGAR_CELL_NUMBER") {
		t.Errorf("error should mention SAGAR_CELL_NUMBER, got: %v", err)
	}
}

func TestInitiateCallWithTwiML_MissingFromNumber(t *testing.T) {
	t.Setenv("SAGAR_CELL_NUMBER", "+15305550199")
	a := &TwilioAdapter{fromNumber: ""}
	_, err := a.InitiateCallWithTwiML("+19165550100", "<Response/>")
	if err == nil {
		t.Fatal("expected error when fromNumber is empty")
	}
	if !strings.Contains(err.Error(), "TWILIO_FROM_NUMBER") {
		t.Errorf("error should mention TWILIO_FROM_NUMBER, got: %v", err)
	}
}
