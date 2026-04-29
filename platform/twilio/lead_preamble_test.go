package twilio

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestHandleLeadPreamble_ReturnsConsentTwiML verifies the lead-preamble endpoint
// returns valid TwiML with the required consent text.
func TestHandleLeadPreamble_ReturnsConsentTwiML(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/twilio/lead-preamble", nil)
	rr := httptest.NewRecorder()

	HandleLeadPreamble(rr, req)

	res := rr.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", res.StatusCode)
	}

	ct := res.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/xml") {
		t.Errorf("Content-Type: got %q, want text/xml", ct)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	bodyStr := string(body)

	const consentText = "This call may be recorded for quality and training purposes."
	if !strings.Contains(bodyStr, consentText) {
		t.Errorf("response missing consent text %q; got:\n%s", consentText, bodyStr)
	}

	if !strings.Contains(bodyStr, `voice="alice"`) {
		t.Errorf("response should use alice voice; got:\n%s", bodyStr)
	}

	if !strings.Contains(bodyStr, "<Pause") {
		t.Errorf("response should include <Pause>; got:\n%s", bodyStr)
	}

	if !strings.HasPrefix(strings.TrimSpace(bodyStr), `<?xml`) {
		t.Errorf("response should start with XML declaration; got:\n%s", bodyStr)
	}
}

// TestHandleLeadPreamble_ValidPhone verifies that a valid E.164 lead param is accepted.
func TestHandleLeadPreamble_ValidPhone(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/twilio/lead-preamble?lead="+url.QueryEscape("+19165550100"), nil)
	rr := httptest.NewRecorder()

	HandleLeadPreamble(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
}

// TestHandleLeadPreamble_InvalidPhone verifies that a non-E.164 lead param returns 400.
func TestHandleLeadPreamble_InvalidPhone(t *testing.T) {
	invalidPhones := []string{
		"1234567890",         // no leading +
		"+abc",               // letters
		"+12345",             // too short
		`<script>alert(1)`,  // script injection
		`"><Redirect>evil`,  // XML injection
	}

	for _, phone := range invalidPhones {
		phone := phone
		t.Run(phone, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet,
				"/twilio/lead-preamble?lead="+url.QueryEscape(phone), nil)
			rr := httptest.NewRecorder()

			HandleLeadPreamble(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("phone %q: got status %d, want 400", phone, rr.Code)
			}
		})
	}
}

// TestHandleLeadPreamble_NoLeadParam verifies that a missing lead param is allowed
// (Twilio may omit it in some edge cases; the TwiML is served regardless).
func TestHandleLeadPreamble_NoLeadParam(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/twilio/lead-preamble", nil)
	rr := httptest.NewRecorder()

	HandleLeadPreamble(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200 for missing lead param", rr.Code)
	}
}
