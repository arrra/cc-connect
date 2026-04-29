package twilio

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// twilioSleep is the sleep function used for retry backoff; overridden in tests.
var twilioSleep = time.Sleep

// TwilioAdapter manages Twilio SMS and voice interactions.
type TwilioAdapter struct {
	accountSID string
	authToken  string
	fromNumber string
	client     *client
}

// Init initializes the adapter from environment variables.
// Required: TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN, TWILIO_FROM_NUMBER.
func (a *TwilioAdapter) Init() error {
	a.accountSID = os.Getenv("TWILIO_ACCOUNT_SID")
	a.authToken = os.Getenv("TWILIO_AUTH_TOKEN")
	a.fromNumber = os.Getenv("TWILIO_FROM_NUMBER")

	if a.accountSID == "" {
		return fmt.Errorf("twilio: TWILIO_ACCOUNT_SID not set")
	}
	if a.authToken == "" {
		return fmt.Errorf("twilio: TWILIO_AUTH_TOKEN not set")
	}
	if a.fromNumber == "" {
		return fmt.Errorf("twilio: TWILIO_FROM_NUMBER not set")
	}

	a.client = newClient(a.accountSID, a.authToken)
	if os.Getenv("RECORDING_STATUS_URL") == "" {
		slog.Warn("[twilio] recording disabled — RECORDING_STATUS_URL not set")
	}
	if os.Getenv("TWILIO_WEBHOOK_PUBLIC_BASE_URL") == "" {
		slog.Warn("[twilio] TWILIO_WEBHOOK_PUBLIC_BASE_URL not set — webhook signature verification falls back to Host+X-Forwarded-Proto reconstruction, which is attacker-controllable; set this env var for canonical, proxy-safe validation")
	}
	slog.Info("twilio: adapter initialized", "from", a.fromNumber)
	return nil
}

// HandleInbound verifies the Twilio request signature and parses the inbound SMS.
// The caller must ensure req.URL reflects the full public URL Twilio used
// (scheme+host+path+query); use X-Forwarded-Proto / Host headers if behind a proxy.
func (a *TwilioAdapter) HandleInbound(req *http.Request) (Inbound, error) {
	if err := req.ParseForm(); err != nil {
		return Inbound{}, fmt.Errorf("twilio: parse form: %w", err)
	}

	sig := req.Header.Get("X-Twilio-Signature")
	if sig == "" {
		return Inbound{}, fmt.Errorf("twilio: missing X-Twilio-Signature header")
	}

	var rawURL string
	if publicBase := os.Getenv("TWILIO_WEBHOOK_PUBLIC_BASE_URL"); publicBase != "" {
		rawURL = publicBase + req.URL.Path
	} else {
		rawURL = req.URL.String()
		if !req.URL.IsAbs() {
			scheme := req.Header.Get("X-Forwarded-Proto")
			if scheme == "" {
				scheme = "https"
			}
			host := req.Host
			if host == "" {
				host = req.URL.Host
			}
			rawURL = scheme + "://" + host + req.URL.RequestURI()
		}
	}

	if !verifyTwilioSignature(a.authToken, rawURL, req.PostForm, sig) {
		return Inbound{}, fmt.Errorf("twilio: invalid signature")
	}

	inbound := Inbound{
		MessageSID: req.PostFormValue("MessageSid"),
		AccountSID: req.PostFormValue("AccountSid"),
		From:       req.PostFormValue("From"),
		Body:       req.PostFormValue("Body"),
	}

	if inbound.From == "" {
		return Inbound{}, fmt.Errorf("twilio: missing From field")
	}
	if inbound.MessageSID == "" {
		return Inbound{}, fmt.Errorf("twilio: missing MessageSid field")
	}

	return inbound, nil
}

// verifyTwilioSignature implements Twilio's documented webhook signature algorithm:
// HMAC-SHA1(authToken, url + sorted_post_key0 + value0 + ...) → base64.
// Comparison is constant-time to prevent timing attacks.
func verifyTwilioSignature(authToken, rawURL string, params url.Values, sig string) bool {
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
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(sig))
}

// SendSMS sends an outbound SMS to the given E.164 phone number.
// Returns the Twilio Message SID on success.
// Retries: 429 → exponential backoff (1s/2s/4s, max 3 retries); 5xx → 1 retry; 4xx → no retry.
func (a *TwilioAdapter) SendSMS(to, body string) (string, error) {
	form := url.Values{
		"From": {a.fromNumber},
		"To":   {to},
		"Body": {body},
	}
	if cb := os.Getenv("TWILIO_STATUS_CALLBACK_URL"); cb != "" {
		form.Set("StatusCallback", cb)
	}

	path := fmt.Sprintf("/2010-04-01/Accounts/%s/Messages.json", a.accountSID)

	backoffs := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	retries429 := 0
	retried5xx := false

	for attempt := 1; ; attempt++ {
		var buf bytes.Buffer
		resp, err := a.client.post(path, form, &buf)
		if err != nil {
			return "", err
		}

		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
			var msg Message
			if err := json.Unmarshal(buf.Bytes(), &msg); err != nil {
				return "", fmt.Errorf("twilio: parse response: %w", err)
			}
			slog.Info("[twilio] outbound sms",
				"to", maskPhone(to),
				"sid", msg.SID,
				"length", len(body),
				"attempt", attempt,
			)
			return msg.SID, nil
		}

		if resp.StatusCode == 429 && retries429 < len(backoffs) {
			slog.Warn("[twilio] outbound sms rate limited, retrying",
				"to", maskPhone(to),
				"attempt", attempt,
			)
			twilioSleep(backoffs[retries429])
			retries429++
			continue
		}

		if resp.StatusCode >= 500 && !retried5xx {
			slog.Warn("[twilio] outbound sms server error, retrying",
				"to", maskPhone(to),
				"status", resp.StatusCode,
				"attempt", attempt,
			)
			retried5xx = true
			continue
		}

		var te twilioError
		_ = json.Unmarshal(buf.Bytes(), &te)
		slog.Error("[twilio] outbound sms failed",
			"to", maskPhone(to),
			"status", resp.StatusCode,
			"attempt", attempt,
		)
		if te.Message != "" {
			return "", fmt.Errorf("twilio: send sms: %w", &te)
		}
		return "", fmt.Errorf("twilio: send sms: http %d", resp.StatusCode)
	}
}

// InitiateCall places a click-to-call: Twilio calls callbackURL TwiML,
// which rings Sagar's cell first and then bridges to the lead.
// toLead is used for logging only; callbackURL encodes the lead phone for TwiML.
// Returns the Twilio Call SID on success.
func (a *TwilioAdapter) InitiateCall(toLead, callbackURL string) (string, error) {
	sagarCell := os.Getenv("SAGAR_CELL_NUMBER")
	if sagarCell == "" {
		return "", fmt.Errorf("twilio: SAGAR_CELL_NUMBER not set")
	}
	if a.fromNumber == "" {
		return "", fmt.Errorf("twilio: TWILIO_FROM_NUMBER not set")
	}

	form := url.Values{
		"From": {a.fromNumber},
		"To":   {sagarCell},
		"Url":  {callbackURL},
	}

	path := fmt.Sprintf("/2010-04-01/Accounts/%s/Calls.json", a.accountSID)

	var buf bytes.Buffer
	resp, err := a.client.post(path, form, &buf)
	if err != nil {
		return "", err
	}

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		var call Call
		if err := json.Unmarshal(buf.Bytes(), &call); err != nil {
			return "", fmt.Errorf("twilio: parse call response: %w", err)
		}
		slog.Info("[twilio] outbound call",
			"lead", maskPhone(toLead),
			"sagar", maskPhone(sagarCell),
			"sid", call.SID,
		)
		return call.SID, nil
	}

	var te twilioError
	_ = json.Unmarshal(buf.Bytes(), &te)
	slog.Error("[twilio] outbound call failed",
		"lead", maskPhone(toLead),
		"status", resp.StatusCode,
	)
	if te.Message != "" {
		return "", fmt.Errorf("twilio: initiate call: %w", &te)
	}
	return "", fmt.Errorf("twilio: initiate call: http %d", resp.StatusCode)
}

// InitiateCallWithTwiML places a click-to-call using inline TwiML (Twiml param, not Url).
// Twilio rings sagarCell (from SAGAR_CELL_NUMBER env) first; when answered, the TwiML
// executes to bridge to the lead. twimlXML must include the two-party consent preamble.
func (a *TwilioAdapter) InitiateCallWithTwiML(toLead, twimlXML string) (string, error) {
	sagarCell := os.Getenv("SAGAR_CELL_NUMBER")
	if sagarCell == "" {
		return "", fmt.Errorf("twilio: SAGAR_CELL_NUMBER not set")
	}
	if a.fromNumber == "" {
		return "", fmt.Errorf("twilio: TWILIO_FROM_NUMBER not set")
	}

	form := url.Values{
		"From":  {a.fromNumber},
		"To":    {sagarCell},
		"Twiml": {twimlXML},
	}

	path := fmt.Sprintf("/2010-04-01/Accounts/%s/Calls.json", a.accountSID)

	var buf bytes.Buffer
	resp, err := a.client.post(path, form, &buf)
	if err != nil {
		return "", err
	}

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		var call Call
		if err := json.Unmarshal(buf.Bytes(), &call); err != nil {
			return "", fmt.Errorf("twilio: parse call response: %w", err)
		}
		slog.Info("[twilio] outbound call (inline twiml)",
			"lead", maskPhone(toLead),
			"sagar", maskPhone(sagarCell),
			"sid", call.SID,
		)
		return call.SID, nil
	}

	var te twilioError
	_ = json.Unmarshal(buf.Bytes(), &te)
	slog.Error("[twilio] outbound call failed",
		"lead", maskPhone(toLead),
		"status", resp.StatusCode,
	)
	if te.Message != "" {
		return "", fmt.Errorf("twilio: initiate call: %w", &te)
	}
	return "", fmt.Errorf("twilio: initiate call: http %d", resp.StatusCode)
}

// New constructs a TwilioAdapter from a config options map (mirrors platform/slack New pattern).
func New(opts map[string]any) (*TwilioAdapter, error) {
	a := &TwilioAdapter{}
	if sid, ok := opts["account_sid"].(string); ok && sid != "" {
		a.accountSID = sid
	}
	if tok, ok := opts["auth_token"].(string); ok && tok != "" {
		a.authToken = tok
	}
	if from, ok := opts["from_number"].(string); ok && from != "" {
		a.fromNumber = from
	}
	if a.accountSID != "" && a.authToken != "" {
		a.client = newClient(a.accountSID, a.authToken)
	}
	return a, nil
}

// maskPhone masks the middle digits of a phone number for safe logging.
// "+19165550123" → "+19*****0123"
func maskPhone(phone string) string {
	if len(phone) < 8 {
		return "***"
	}
	return phone[:3] + strings.Repeat("*", len(phone)-7) + phone[len(phone)-4:]
}
