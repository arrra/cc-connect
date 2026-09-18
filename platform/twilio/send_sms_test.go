package twilio

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// mockTransport returns preconfigured responses in sequence.
type mockTransport struct {
	responses []*http.Response
	calls     int
}

func (m *mockTransport) Do(req *http.Request) (*http.Response, error) {
	if m.calls >= len(m.responses) {
		// Return last response repeatedly if exhausted.
		return m.responses[len(m.responses)-1], nil
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

func jsonBody(v any) io.ReadCloser {
	b, _ := json.Marshal(v)
	return io.NopCloser(bytes.NewReader(b))
}

func msgResp(statusCode int, sid string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       jsonBody(Message{SID: sid, Status: MessageStatusSent}),
	}
}

func errResp(statusCode int, msg string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       jsonBody(twilioError{Status: statusCode, Message: msg}),
	}
}

func adapterWithTransport(transport *mockTransport) *TwilioAdapter {
	return &TwilioAdapter{
		accountSID: "ACtest",
		authToken:  "secret",
		fromNumber: "+19165550100",
		client: &client{
			accountSID: "ACtest",
			authToken:  "secret",
			baseURL:    "https://api.twilio.com",
			http:       transport,
		},
	}
}

func TestSendSms_Success(t *testing.T) {
	transport := &mockTransport{
		responses: []*http.Response{msgResp(http.StatusCreated, "SM001")},
	}
	a := adapterWithTransport(transport)
	sid, err := a.SendSMS("+15105550123", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sid != "SM001" {
		t.Errorf("sid = %q, want SM001", sid)
	}
	if transport.calls != 1 {
		t.Errorf("expected 1 HTTP call, got %d", transport.calls)
	}
}

func TestSendSms_429RetrySuccess(t *testing.T) {
	// Disable real sleep in tests.
	old := twilioSleep
	twilioSleep = func(d time.Duration) {}
	defer func() { twilioSleep = old }()

	transport := &mockTransport{
		responses: []*http.Response{
			errResp(429, "Too Many Requests"),
			errResp(429, "Too Many Requests"),
			msgResp(http.StatusCreated, "SM002"),
		},
	}
	a := adapterWithTransport(transport)
	sid, err := a.SendSMS("+15105550123", "hello")
	if err != nil {
		t.Fatalf("unexpected error after retries: %v", err)
	}
	if sid != "SM002" {
		t.Errorf("sid = %q, want SM002", sid)
	}
	if transport.calls != 3 {
		t.Errorf("expected 3 HTTP calls, got %d", transport.calls)
	}
}

func TestSendSms_5xxRetrySuccess(t *testing.T) {
	transport := &mockTransport{
		responses: []*http.Response{
			errResp(500, "Internal Server Error"),
			msgResp(http.StatusCreated, "SM003"),
		},
	}
	a := adapterWithTransport(transport)
	sid, err := a.SendSMS("+15105550123", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sid != "SM003" {
		t.Errorf("sid = %q, want SM003", sid)
	}
	if transport.calls != 2 {
		t.Errorf("expected 2 HTTP calls, got %d", transport.calls)
	}
}

func TestSendSms_4xxErrorNoRetry(t *testing.T) {
	transport := &mockTransport{
		responses: []*http.Response{
			errResp(400, "Bad Request"),
		},
	}
	a := adapterWithTransport(transport)
	_, err := a.SendSMS("+15105550123", "hello")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "Bad Request") {
		t.Errorf("error %q should mention 'Bad Request'", err.Error())
	}
	if transport.calls != 1 {
		t.Errorf("expected 1 HTTP call (no retry), got %d", transport.calls)
	}
}

func TestSendSms_429ExhaustedRetries(t *testing.T) {
	old := twilioSleep
	twilioSleep = func(d time.Duration) {}
	defer func() { twilioSleep = old }()

	// 4 consecutive 429s — should exhaust the 3-retry budget.
	transport := &mockTransport{
		responses: []*http.Response{
			errResp(429, "Rate Limited"),
			errResp(429, "Rate Limited"),
			errResp(429, "Rate Limited"),
			errResp(429, "Rate Limited"),
		},
	}
	a := adapterWithTransport(transport)
	_, err := a.SendSMS("+15105550123", "hello")
	if err == nil {
		t.Fatal("expected error after exhausting 429 retries")
	}
	// 1 initial + 3 retries = 4 calls total
	if transport.calls != 4 {
		t.Errorf("expected 4 HTTP calls (1+3 retries), got %d", transport.calls)
	}
}

func TestSendSms_MaskedPhoneInLogs(t *testing.T) {
	// maskPhone should hide the middle digits.
	masked := maskPhone("+19165550123")
	if strings.Contains(masked, "5550") {
		t.Errorf("maskPhone(%q) = %q should mask the middle digits", "+19165550123", masked)
	}
	if !strings.HasPrefix(masked, "+19") {
		t.Errorf("maskPhone should preserve prefix: got %q", masked)
	}
	if !strings.HasSuffix(masked, "0123") {
		t.Errorf("maskPhone should preserve last 4: got %q", masked)
	}
}
