package twilio

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
)

// computeSig mirrors verifyTwilioSignature for use in test setup.
func computeSig(authToken, rawURL string, params url.Values) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString(rawURL)
	for _, k := range keys {
		sb.WriteString(k)
		for _, v := range params[k] {
			sb.WriteString(v)
		}
	}

	mac := hmac.New(sha1.New, []byte(authToken))
	mac.Write([]byte(sb.String()))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// twilioDocParams is the documented Twilio test vector from
// https://www.twilio.com/docs/usage/security#validating-signatures
var twilioDocParams = url.Values{
	"CallSid": {"CA1234567890ABCDE"},
	"Caller":  {"+14158675309"},
	"Digits":  {"1234"},
	"From":    {"+14158675309"},
	"To":      {"+18005551212"},
}

// TestHandleInbound_TwilioDocTestVector verifies our signing algorithm against the
// expected HMAC-SHA1 output for the Twilio-documented test inputs:
// auth_token=12345, url=https://mycompany.com/myapp.php?foo=1&bar=2,
// params: CallSid, Caller, Digits, From, To (values from Twilio's security docs).
// Expected value confirmed by running the algorithm against these inputs.
func TestHandleInbound_TwilioDocTestVector(t *testing.T) {
	const (
		authToken = "12345"
		docURL    = "https://mycompany.com/myapp.php?foo=1&bar=2"
		wantSig   = "RSOYDt4T1cUTdK1PDd93/VVr8B8="
	)
	if !verifyTwilioSignature(authToken, docURL, twilioDocParams, wantSig) {
		got := computeSig(authToken, docURL, twilioDocParams)
		t.Errorf("verifyTwilioSignature mismatch with Twilio doc vector\n  got  sig: %s\n  want sig: %s", got, wantSig)
	}
}

// smsParams builds a minimal valid inbound SMS form payload.
func smsParams(from, msgSID, body string) url.Values {
	return url.Values{
		"AccountSid": {"ACtest"},
		"From":       {from},
		"To":         {"+19165550100"},
		"Body":       {body},
		"MessageSid": {msgSID},
	}
}

func inboundRequest(t *testing.T, rawURL string, params url.Values, sig string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, rawURL, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", sig)
	return req
}

func TestHandleInbound_ValidSignature(t *testing.T) {
	const rawURL = "https://example.com/twilio/inbound-sms"
	const authToken = "secret"
	params := smsParams("+19165550123", "SM001", "Hello from test")
	sig := computeSig(authToken, rawURL, params)

	req := inboundRequest(t, rawURL, params, sig)

	a := &TwilioAdapter{authToken: authToken}
	inbound, err := a.HandleInbound(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inbound.From != "+19165550123" {
		t.Errorf("From = %q, want +19165550123", inbound.From)
	}
	if inbound.MessageSID != "SM001" {
		t.Errorf("MessageSID = %q, want SM001", inbound.MessageSID)
	}
	if inbound.Body != "Hello from test" {
		t.Errorf("Body = %q, want 'Hello from test'", inbound.Body)
	}
	if inbound.AccountSID != "ACtest" {
		t.Errorf("AccountSID = %q, want ACtest", inbound.AccountSID)
	}
}

func TestHandleInbound_InvalidSignature(t *testing.T) {
	const rawURL = "https://example.com/twilio/inbound-sms"
	params := smsParams("+19165550123", "SM001", "Hello")
	// Use wrong auth token to produce a bad signature.
	sig := computeSig("wrong-token", rawURL, params)

	req := inboundRequest(t, rawURL, params, sig)

	a := &TwilioAdapter{authToken: "correct-token"}
	_, err := a.HandleInbound(req)
	if err == nil {
		t.Fatal("expected error for invalid signature, got nil")
	}
	if !strings.Contains(err.Error(), "invalid signature") {
		t.Errorf("error %q should mention 'invalid signature'", err.Error())
	}
}

func TestHandleInbound_MissingSignatureHeader(t *testing.T) {
	const rawURL = "https://example.com/twilio/inbound-sms"
	params := smsParams("+19165550123", "SM001", "Hello")

	req := httptest.NewRequest(http.MethodPost, rawURL, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// No X-Twilio-Signature header.

	a := &TwilioAdapter{authToken: "secret"}
	_, err := a.HandleInbound(req)
	if err == nil {
		t.Fatal("expected error for missing X-Twilio-Signature, got nil")
	}
	if !strings.Contains(err.Error(), "X-Twilio-Signature") {
		t.Errorf("error %q should mention X-Twilio-Signature", err.Error())
	}
}

func TestHandleInbound_MissingFromField(t *testing.T) {
	const rawURL = "https://example.com/twilio/inbound-sms"
	const authToken = "secret"
	params := url.Values{
		"AccountSid": {"ACtest"},
		"MessageSid": {"SM002"},
		"Body":       {"hello"},
		// "From" intentionally omitted
	}
	sig := computeSig(authToken, rawURL, params)
	req := inboundRequest(t, rawURL, params, sig)

	a := &TwilioAdapter{authToken: authToken}
	_, err := a.HandleInbound(req)
	if err == nil {
		t.Fatal("expected error for missing From field, got nil")
	}
	if !strings.Contains(err.Error(), "From") {
		t.Errorf("error %q should mention missing From field", err.Error())
	}
}

func TestHandleInbound_MissingMessageSidField(t *testing.T) {
	const rawURL = "https://example.com/twilio/inbound-sms"
	const authToken = "secret"
	params := url.Values{
		"AccountSid": {"ACtest"},
		"From":       {"+19165550123"},
		"Body":       {"hello"},
		// "MessageSid" intentionally omitted
	}
	sig := computeSig(authToken, rawURL, params)
	req := inboundRequest(t, rawURL, params, sig)

	a := &TwilioAdapter{authToken: authToken}
	_, err := a.HandleInbound(req)
	if err == nil {
		t.Fatal("expected error for missing MessageSid field, got nil")
	}
	if !strings.Contains(err.Error(), "MessageSid") {
		t.Errorf("error %q should mention missing MessageSid field", err.Error())
	}
}

// errReader is an io.ReadCloser that always errors on Read.
type errReader struct{}

func (e *errReader) Read(p []byte) (int, error) { return 0, fmt.Errorf("simulated read error") }
func (e *errReader) Close() error               { return nil }

// TestHandleInbound_ParseFormError verifies that a body read failure is surfaced cleanly.
func TestHandleInbound_ParseFormError(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com/twilio/inbound-sms", &errReader{})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", "sig")

	a := &TwilioAdapter{authToken: "secret"}
	_, err = a.HandleInbound(req)
	if err == nil {
		t.Fatal("expected error for body read failure, got nil")
	}
	if !strings.Contains(err.Error(), "parse form") {
		t.Errorf("error %q should mention 'parse form'", err.Error())
	}
}

// TestHandleInbound_ProxyRequest verifies URL reconstruction from Host + X-Forwarded-Proto
// when running behind a reverse proxy (req.URL is relative).
func TestHandleInbound_ProxyRequest(t *testing.T) {
	// Build a request with a relative URL (as seen by a server handler).
	params := smsParams("+19165550123", "SM010", "proxy test")
	const (
		proxyHost = "webhook.example.com"
		path      = "/twilio/inbound-sms"
		scheme    = "https"
		authToken = "secret"
	)
	reconstructed := scheme + "://" + proxyHost + path
	sig := computeSig(authToken, reconstructed, params)

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", sig)
	req.Header.Set("X-Forwarded-Proto", scheme)
	req.Host = proxyHost

	a := &TwilioAdapter{authToken: authToken}
	inbound, err := a.HandleInbound(req)
	if err != nil {
		t.Fatalf("unexpected error for proxy request: %v", err)
	}
	if inbound.From != "+19165550123" {
		t.Errorf("From = %q, want +19165550123", inbound.From)
	}
}

// TestHandleInbound_ProxyRequestDefaultsToHTTPS verifies that when X-Forwarded-Proto is
// absent the proxy path defaults to https.
func TestHandleInbound_ProxyRequestDefaultsToHTTPS(t *testing.T) {
	params := smsParams("+19165550123", "SM011", "default scheme")
	const (
		proxyHost = "webhook.example.com"
		path      = "/twilio/inbound-sms"
		authToken = "secret"
	)
	reconstructed := "https://" + proxyHost + path
	sig := computeSig(authToken, reconstructed, params)

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", sig)
	// No X-Forwarded-Proto — should default to https.
	req.Host = proxyHost

	a := &TwilioAdapter{authToken: authToken}
	inbound, err := a.HandleInbound(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inbound.MessageSID != "SM011" {
		t.Errorf("MessageSID = %q, want SM011", inbound.MessageSID)
	}
}

// TestSignatureWithPublicBaseURL verifies that when TWILIO_WEBHOOK_PUBLIC_BASE_URL is set,
// HandleInbound uses it as the canonical URL for signature verification (ignoring Host headers).
func TestSignatureWithPublicBaseURL(t *testing.T) {
	const (
		publicBase = "https://cc.example.com"
		path       = "/twilio/inbound-sms"
		authToken  = "secret"
	)
	t.Setenv("TWILIO_WEBHOOK_PUBLIC_BASE_URL", publicBase)

	params := smsParams("+19165550123", "SM020", "public base url test")
	canonicalURL := publicBase + path
	sig := computeSig(authToken, canonicalURL, params)

	// Send request with a different Host — the env var should take precedence.
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", sig)
	req.Host = "attacker-controlled.example.com"

	a := &TwilioAdapter{authToken: authToken}
	inbound, err := a.HandleInbound(req)
	if err != nil {
		t.Fatalf("unexpected error with TWILIO_WEBHOOK_PUBLIC_BASE_URL set: %v", err)
	}
	if inbound.From != "+19165550123" {
		t.Errorf("From = %q, want +19165550123", inbound.From)
	}
}

// TestSignatureFallbackWithoutPublicBaseURL verifies that the existing Host+X-Forwarded-Proto
// fallback still works when TWILIO_WEBHOOK_PUBLIC_BASE_URL is not set.
func TestSignatureFallbackWithoutPublicBaseURL(t *testing.T) {
	const (
		proxyHost = "webhook.example.com"
		path      = "/twilio/inbound-sms"
		scheme    = "https"
		authToken = "secret"
	)
	// Ensure env var is absent.
	t.Setenv("TWILIO_WEBHOOK_PUBLIC_BASE_URL", "")

	params := smsParams("+19165550456", "SM021", "fallback test")
	reconstructed := scheme + "://" + proxyHost + path
	sig := computeSig(authToken, reconstructed, params)

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", sig)
	req.Header.Set("X-Forwarded-Proto", scheme)
	req.Host = proxyHost

	a := &TwilioAdapter{authToken: authToken}
	inbound, err := a.HandleInbound(req)
	if err != nil {
		t.Fatalf("unexpected error for fallback path: %v", err)
	}
	if inbound.MessageSID != "SM021" {
		t.Errorf("MessageSID = %q, want SM021", inbound.MessageSID)
	}
}

func TestHandleInbound_TamperedBody(t *testing.T) {
	const rawURL = "https://example.com/twilio/inbound-sms"
	const authToken = "secret"
	params := smsParams("+19165550123", "SM001", "Original body")
	sig := computeSig(authToken, rawURL, params)

	// Tamper: change the body after signing.
	params.Set("Body", "Tampered body")
	req := inboundRequest(t, rawURL, params, sig)

	a := &TwilioAdapter{authToken: authToken}
	_, err := a.HandleInbound(req)
	if err == nil {
		t.Fatal("expected error for tampered body, got nil")
	}
	if !strings.Contains(err.Error(), "invalid signature") {
		t.Errorf("error %q should mention 'invalid signature'", err.Error())
	}
}
