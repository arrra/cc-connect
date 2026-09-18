package twilio

import (
	"net/http"
	"strings"
	"testing"
)

func callResp(statusCode int, sid string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       jsonBody(Call{SID: sid, Status: CallStatusQueued}),
	}
}

func TestInitiateCall_Success(t *testing.T) {
	t.Setenv("SAGAR_CELL_NUMBER", "+15305550199")

	transport := &mockTransport{
		responses: []*http.Response{callResp(http.StatusCreated, "CA001")},
	}
	a := adapterWithTransport(transport)

	sid, err := a.InitiateCall("+19165550123", "https://worker.example.com/twiml/call?lead=%2B19165550123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sid != "CA001" {
		t.Errorf("sid = %q, want CA001", sid)
	}
	if transport.calls != 1 {
		t.Errorf("expected 1 HTTP call, got %d", transport.calls)
	}
}

func TestInitiateCall_MissingSagarCell(t *testing.T) {
	t.Setenv("SAGAR_CELL_NUMBER", "")

	transport := &mockTransport{
		responses: []*http.Response{callResp(http.StatusCreated, "CA001")},
	}
	a := adapterWithTransport(transport)

	_, err := a.InitiateCall("+19165550123", "https://worker.example.com/twiml/call")
	if err == nil {
		t.Fatal("expected error for missing SAGAR_CELL_NUMBER")
	}
	if !strings.Contains(err.Error(), "SAGAR_CELL_NUMBER") {
		t.Errorf("error %q should mention SAGAR_CELL_NUMBER", err.Error())
	}
	if transport.calls != 0 {
		t.Errorf("expected no HTTP calls, got %d", transport.calls)
	}
}

func TestInitiateCall_MissingFromNumber(t *testing.T) {
	t.Setenv("SAGAR_CELL_NUMBER", "+15305550199")

	transport := &mockTransport{
		responses: []*http.Response{callResp(http.StatusCreated, "CA001")},
	}
	a := adapterWithTransport(transport)
	a.fromNumber = ""

	_, err := a.InitiateCall("+19165550123", "https://worker.example.com/twiml/call")
	if err == nil {
		t.Fatal("expected error for missing TWILIO_FROM_NUMBER")
	}
	if !strings.Contains(err.Error(), "TWILIO_FROM_NUMBER") {
		t.Errorf("error %q should mention TWILIO_FROM_NUMBER", err.Error())
	}
	if transport.calls != 0 {
		t.Errorf("expected no HTTP calls, got %d", transport.calls)
	}
}

func TestInitiateCall_TwilioError(t *testing.T) {
	t.Setenv("SAGAR_CELL_NUMBER", "+15305550199")

	transport := &mockTransport{
		responses: []*http.Response{errResp(400, "Invalid phone number")},
	}
	a := adapterWithTransport(transport)

	_, err := a.InitiateCall("+19165550123", "https://worker.example.com/twiml/call")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "Invalid phone number") {
		t.Errorf("error %q should contain Twilio error message", err.Error())
	}
	if transport.calls != 1 {
		t.Errorf("expected 1 HTTP call, got %d", transport.calls)
	}
}

func TestInitiateCall_500Error(t *testing.T) {
	t.Setenv("SAGAR_CELL_NUMBER", "+15305550199")

	transport := &mockTransport{
		responses: []*http.Response{
			{StatusCode: 500, Body: jsonBody(map[string]any{})},
		},
	}
	a := adapterWithTransport(transport)

	_, err := a.InitiateCall("+19165550123", "https://worker.example.com/twiml/call")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "http 500") {
		t.Errorf("error %q should mention http 500", err.Error())
	}
}

func TestInitiateCall_LogsMaskBothPhones(t *testing.T) {
	lead := "+19165550123"
	sagar := "+15305550199"

	maskedLead := maskPhone(lead)
	maskedSagar := maskPhone(sagar)

	if maskedLead == lead {
		t.Errorf("maskPhone(lead) should differ from input, got %q", maskedLead)
	}
	if maskedSagar == sagar {
		t.Errorf("maskPhone(sagar) should differ from input, got %q", maskedSagar)
	}
	if strings.Contains(maskedLead, "5550") {
		t.Errorf("maskPhone(lead) = %q should mask middle digits", maskedLead)
	}
	if strings.Contains(maskedSagar, "5550") {
		t.Errorf("maskPhone(sagar) = %q should mask middle digits", maskedSagar)
	}
	if !strings.HasSuffix(maskedLead, "0123") {
		t.Errorf("maskPhone(lead) = %q should preserve last 4", maskedLead)
	}
	if !strings.HasSuffix(maskedSagar, "0199") {
		t.Errorf("maskPhone(sagar) = %q should preserve last 4", maskedSagar)
	}
}
