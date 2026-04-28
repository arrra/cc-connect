// Twilio bridge integration tests. No external API calls — all Slack and Twilio
// interactions are replaced by in-process mocks. Tests verify end-to-end wiring
// across InboundRouter, VistaHillsHandler, SmsCmd, and CallCmd.
//
// Run: go test ./tests/integration/twilio-bridge_test.go -count=1 -timeout 60s

package integration

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	slacksvc "github.com/chenhg5/cc-connect/platform/slack"
	slashcmds "github.com/chenhg5/cc-connect/platform/slack/commands"
	tw "github.com/chenhg5/cc-connect/platform/twilio"
)

// ---------------------------------------------------------------------------
// Shared mocks
// ---------------------------------------------------------------------------

// bridgeMockSlack records PostMessage / PostReply calls.
// It satisfies both tw.SlackPoster and the unexported vistaSlackPoster interface
// in platform/slack (Go's structural typing — method sets match without naming the type).
type bridgeMockSlack struct {
	messages []bridgeSlackMsg
	replies  []bridgeSlackReply
	nextTS   string
}

type bridgeSlackMsg struct{ Channel, Text string }
type bridgeSlackReply struct{ Channel, ThreadTS, Text string }

func (m *bridgeMockSlack) PostMessage(_ context.Context, channelID, text string) (string, error) {
	m.messages = append(m.messages, bridgeSlackMsg{channelID, text})
	ts := m.nextTS
	if ts == "" {
		ts = "1700000000.000001"
	}
	return ts, nil
}

func (m *bridgeMockSlack) PostReply(_ context.Context, channelID, threadTS, text string) error {
	m.replies = append(m.replies, bridgeSlackReply{channelID, threadTS, text})
	return nil
}

// bridgeMockSMSSender captures SendSMS arguments.
type bridgeMockSMSSender struct {
	sid    string
	err    error
	lastTo string
	lastBody string
}

func (m *bridgeMockSMSSender) SendSMS(to, body string) (string, error) {
	m.lastTo = to
	m.lastBody = body
	return m.sid, m.err
}

// bridgeMockCallInitiator captures InitiateCallWithTwiML arguments.
type bridgeMockCallInitiator struct {
	callSid      string
	err          error
	lastToLead   string
	lastTwimlXML string
}

func (m *bridgeMockCallInitiator) InitiateCallWithTwiML(toLead, twimlXML string) (string, error) {
	m.lastToLead = toLead
	m.lastTwimlXML = twimlXML
	return m.callSid, m.err
}

// bridgeThreadLookup wraps PhoneThreadStore to satisfy slashcmds.ThreadLookup.
type bridgeThreadLookup struct{ store *tw.PhoneThreadStore }

func (l *bridgeThreadLookup) GetPhone(channel, threadTS string) (string, bool) {
	return l.store.GetPhone(channel, threadTS)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// captureBridgeLogs replaces the default slog handler with a buffer for test duration.
func captureBridgeLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// computeBridgeSig computes the X-Twilio-Signature for test requests.
func computeBridgeSig(authToken, rawURL string, params url.Values) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(rawURL)
	for _, k := range keys {
		sb.WriteString(k)
		if vals := params[k]; len(vals) > 0 {
			sb.WriteString(vals[0])
		}
	}
	mac := hmac.New(sha1.New, []byte(authToken))
	mac.Write([]byte(sb.String()))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// newSignedInboundReq builds a Twilio-signed POST request for inbound-sms handler tests.
func newSignedInboundReq(t *testing.T, authToken, rawURL string, params url.Values) *http.Request {
	t.Helper()
	sig := computeBridgeSig(authToken, rawURL, params)
	req := httptest.NewRequest(http.MethodPost, rawURL, strings.NewReader(params.Encode()))
	req.Host = "example.com"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", sig)
	req.Header.Set("X-Forwarded-Proto", "https")
	return req
}

// postJSONReq builds a JSON POST request with an optional secret header.
func postJSONReq(t *testing.T, path string, body any, secret string) *http.Request {
	t.Helper()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("X-CC-Connect-Secret", secret)
	}
	return req
}

// ---------------------------------------------------------------------------
// Scenario 1: Outbound SMS via !sms command
// ---------------------------------------------------------------------------

// TestTwilioBridge_OutboundSMS verifies that SmsCmd calls Twilio with the correct
// To/Body and posts a human-readable confirmation to the Slack thread.
func TestTwilioBridge_OutboundSMS(t *testing.T) {
	logs := captureBridgeLogs(t)

	const channel = "C_LEADS"
	const threadTS = "1700000001.000001"
	const leadPhone = "+19165550123"
	const msgText = "hello mary"
	const smsSID = "SM_outbound_001"

	store := tw.NewPhoneThreadStore("")
	_ = store.SetThread(leadPhone, tw.LeadThread{Channel: channel, ThreadTS: threadTS})

	sender := &bridgeMockSMSSender{sid: smsSID}
	slacker := &bridgeMockSlack{}
	lookup := &bridgeThreadLookup{store: store}

	cmd := &slashcmds.SmsCmd{
		Twilio: sender,
		Store:  lookup,
		Slack:  slacker,
	}

	if err := cmd.Handle(context.Background(), channel, threadTS, msgText); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	// Twilio called with correct To and Body.
	if sender.lastTo != leadPhone {
		t.Errorf("SMS To: got %q, want %q", sender.lastTo, leadPhone)
	}
	if sender.lastBody != msgText {
		t.Errorf("SMS Body: got %q, want %q", sender.lastBody, msgText)
	}

	// Slack thread confirmation.
	if len(slacker.replies) != 1 {
		t.Fatalf("expected 1 Slack reply, got %d", len(slacker.replies))
	}
	r := slacker.replies[0]
	if r.Channel != channel || r.ThreadTS != threadTS {
		t.Errorf("confirmation in wrong thread: channel=%q ts=%q", r.Channel, r.ThreadTS)
	}
	if !strings.Contains(r.Text, "✓ SMS sent") {
		t.Errorf("confirmation missing success marker; got: %q", r.Text)
	}
	if !strings.Contains(r.Text, smsSID[:8]) {
		t.Errorf("confirmation missing truncated SID; got: %q", r.Text)
	}

	// Log has [!sms] entry without raw phone.
	logOut := logs.String()
	if !strings.Contains(logOut, "[!sms]") {
		t.Errorf("log missing [!sms]; got: %s", logOut)
	}
	if strings.Contains(logOut, leadPhone) {
		t.Errorf("log must not contain raw phone; got: %s", logOut)
	}
}

// ---------------------------------------------------------------------------
// Scenario 2: Inbound SMS → Slack thread routing
// ---------------------------------------------------------------------------

// TestTwilioBridge_InboundSMS_KnownThread verifies that a Twilio-signed inbound
// webhook for a known lead is posted as a thread reply and the mapping is preserved.
func TestTwilioBridge_InboundSMS_KnownThread(t *testing.T) {
	logs := captureBridgeLogs(t)

	const authToken = "test-auth-inbound"
	const channel = "C_LEADS"
	const existingTS = "1700000002.000001"
	const leadPhone = "+19165550124"
	const rawURL = "https://example.com/twilio/inbound-sms"
	const msgSID = "SM_inbound_001"
	const msgBody = "Hi, I'd like more info about pricing"

	adapter, _ := tw.New(map[string]any{
		"account_sid": "AC_test",
		"auth_token":  authToken,
		"from_number": "+19160000000",
	})
	store := tw.NewPhoneThreadStore("")
	_ = store.SetThread(leadPhone, tw.LeadThread{Channel: channel, ThreadTS: existingTS})

	slacker := &bridgeMockSlack{}
	router := tw.NewInboundRouter(adapter, slacker, store, channel)

	params := url.Values{
		"From":       {leadPhone},
		"Body":       {msgBody},
		"MessageSid": {msgSID},
		"AccountSid": {"AC_test"},
	}
	req := newSignedInboundReq(t, authToken, rawURL, params)
	rr := httptest.NewRecorder()
	router.HandleInbound(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	// Threaded reply posted (not a new top-level post).
	if len(slacker.messages) != 0 {
		t.Errorf("expected no new top-level posts for known thread, got %d", len(slacker.messages))
	}
	if len(slacker.replies) != 1 {
		t.Fatalf("expected 1 thread reply, got %d", len(slacker.replies))
	}
	reply := slacker.replies[0]
	if reply.Channel != channel {
		t.Errorf("reply channel: got %q, want %q", reply.Channel, channel)
	}
	if reply.ThreadTS != existingTS {
		t.Errorf("reply threadTS: got %q, want %q", reply.ThreadTS, existingTS)
	}
	if !strings.Contains(reply.Text, msgBody) {
		t.Errorf("reply should contain message body; got: %q", reply.Text)
	}

	// Thread mapping not overwritten.
	thread, ok := store.GetThread(leadPhone)
	if !ok || thread.ThreadTS != existingTS {
		t.Errorf("thread mapping changed; got %v", thread)
	}

	// Log output.
	logOut := logs.String()
	if !strings.Contains(logOut, "[twilio-inbound]") {
		t.Errorf("log missing [twilio-inbound]; got: %s", logOut)
	}
	if strings.Contains(logOut, leadPhone) {
		t.Errorf("log must not contain raw phone; got: %s", logOut)
	}
}

// TestTwilioBridge_InboundSMS_OrphanCreatesThread verifies that an orphan inbound
// SMS creates a new top-level Slack post and persists the thread mapping.
func TestTwilioBridge_InboundSMS_OrphanCreatesThread(t *testing.T) {
	const authToken = "test-auth-orphan"
	const channel = "C_LEADS"
	const rawURL = "https://example.com/twilio/inbound-sms"
	const orphanPhone = "+19165550125"
	const returnedTS = "1700000003.000001"

	adapter, _ := tw.New(map[string]any{
		"account_sid": "AC_test2",
		"auth_token":  authToken,
		"from_number": "+19160000000",
	})
	store := tw.NewPhoneThreadStore("")
	slacker := &bridgeMockSlack{nextTS: returnedTS}
	router := tw.NewInboundRouter(adapter, slacker, store, channel)

	params := url.Values{
		"From":       {orphanPhone},
		"Body":       {"Saw your ad"},
		"MessageSid": {"SM_orphan_001"},
		"AccountSid": {"AC_test2"},
	}
	req := newSignedInboundReq(t, authToken, rawURL, params)
	rr := httptest.NewRecorder()
	router.HandleInbound(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	// New top-level message posted.
	if len(slacker.messages) != 1 {
		t.Fatalf("expected 1 top-level post for orphan, got %d", len(slacker.messages))
	}
	if slacker.messages[0].Channel != channel {
		t.Errorf("orphan post channel: got %q, want %q", slacker.messages[0].Channel, channel)
	}

	// Mapping persisted.
	thread, ok := store.GetThread(orphanPhone)
	if !ok {
		t.Fatal("orphan thread mapping not stored")
	}
	if thread.ThreadTS != returnedTS {
		t.Errorf("orphan thread TS: got %q, want %q", thread.ThreadTS, returnedTS)
	}
}

// ---------------------------------------------------------------------------
// Scenario 3: !call → TwiML preamble + Slack confirmation
// ---------------------------------------------------------------------------

// TestTwilioBridge_Call verifies that CallCmd sends TwiML with the two-party
// consent preamble before <Dial>, and posts a Slack confirmation in the thread.
func TestTwilioBridge_Call(t *testing.T) {
	logs := captureBridgeLogs(t)

	const channel = "C_LEADS"
	const threadTS = "1700000004.000001"
	const leadPhone = "+19165550126"
	const sagarCell = "+15305550199"
	const callSID = "CA_bridge_001"

	t.Setenv("SAGAR_CELL_NUMBER", sagarCell)
	t.Setenv("RECORDING_STATUS_URL", "")

	store := tw.NewPhoneThreadStore("")
	_ = store.SetThread(leadPhone, tw.LeadThread{Channel: channel, ThreadTS: threadTS})

	caller := &bridgeMockCallInitiator{callSid: callSID}
	slacker := &bridgeMockSlack{}
	lookup := &bridgeThreadLookup{store: store}

	cmd := &slashcmds.CallCmd{
		Twilio: caller,
		Store:  lookup,
		Slack:  slacker,
	}

	if err := cmd.Handle(context.Background(), channel, threadTS, ""); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	// Twilio called with lead's phone as toLead.
	if caller.lastToLead != leadPhone {
		t.Errorf("toLead: got %q, want %q", caller.lastToLead, leadPhone)
	}

	// TwiML includes California two-party consent preamble (hard legal requirement).
	if !strings.Contains(caller.lastTwimlXML, "This call may be recorded for quality.") {
		t.Errorf("TwiML missing consent preamble; got:\n%s", caller.lastTwimlXML)
	}
	sayIdx := strings.Index(caller.lastTwimlXML, "<Say")
	dialIdx := strings.Index(caller.lastTwimlXML, "<Dial")
	if sayIdx == -1 || dialIdx == -1 {
		t.Fatalf("TwiML missing <Say> or <Dial>; got:\n%s", caller.lastTwimlXML)
	}
	if sayIdx > dialIdx {
		t.Errorf("<Say> must precede <Dial> for consent; TwiML:\n%s", caller.lastTwimlXML)
	}

	// Slack confirmation in correct thread.
	if len(slacker.replies) != 1 {
		t.Fatalf("expected 1 Slack reply, got %d", len(slacker.replies))
	}
	reply := slacker.replies[0]
	if reply.Channel != channel || reply.ThreadTS != threadTS {
		t.Errorf("confirmation in wrong thread: channel=%q ts=%q", reply.Channel, reply.ThreadTS)
	}
	if !strings.Contains(reply.Text, "📞") {
		t.Errorf("confirmation missing 📞; got: %q", reply.Text)
	}

	// Log output.
	logOut := logs.String()
	if !strings.Contains(logOut, "[!call]") {
		t.Errorf("log missing [!call]; got: %s", logOut)
	}
	if strings.Contains(logOut, leadPhone) || strings.Contains(logOut, sagarCell) {
		t.Errorf("log must not contain raw phone numbers; got: %s", logOut)
	}
}

// ---------------------------------------------------------------------------
// Scenario 4: Lead state update → Slack thread reply
// ---------------------------------------------------------------------------

// TestTwilioBridge_LeadStateUpdate exercises the full Vista Hills webhook flow:
// /vista-hills/lead-created creates a Slack thread; /vista-hills/lead-state-update
// posts a thread reply with the state transition.
func TestTwilioBridge_LeadStateUpdate(t *testing.T) {
	logs := captureBridgeLogs(t)

	const secret = "webhook-secret-test"
	const channel = "C_LEADS"
	const phone = "+19165550127"
	const leadID = int64(5871)
	const newTS = "1700000005.000001"

	slacker := &bridgeMockSlack{nextTS: newTS}
	store := tw.NewPhoneThreadStore("")
	h := slacksvc.NewVistaHillsHandler(slacker, store, secret, channel)

	// --- Step 1: POST /vista-hills/lead-created ---
	createdPayload := slacksvc.LeadCreatedRequest{
		LeadID:          leadID,
		Phone:           phone,
		Name:            "Mary Chen",
		Source:          "meta",
		Campaign:        "EDH-Brochure-A",
		FormSubmittedAt: "2026-04-28T14:14:08Z",
	}
	createdReq := postJSONReq(t, "/vista-hills/lead-created", createdPayload, secret)
	createdRR := httptest.NewRecorder()
	h.HandleLeadCreated(createdRR, createdReq)

	if createdRR.Code != http.StatusOK {
		t.Fatalf("lead-created: got %d; body: %s", createdRR.Code, createdRR.Body.String())
	}

	var createdJSON map[string]string
	_ = json.NewDecoder(createdRR.Body).Decode(&createdJSON)
	if createdJSON["slack_thread_ts"] != newTS {
		t.Errorf("lead-created response ts: got %q, want %q", createdJSON["slack_thread_ts"], newTS)
	}

	// Top-level Slack post created with lead info.
	if len(slacker.messages) != 1 {
		t.Fatalf("expected 1 Slack message after lead-created, got %d", len(slacker.messages))
	}
	if !strings.Contains(slacker.messages[0].Text, "Mary Chen") {
		t.Errorf("lead post should mention lead name; got: %q", slacker.messages[0].Text)
	}

	// Thread mapping stored.
	thread, ok := store.GetThread(phone)
	if !ok {
		t.Fatal("thread mapping not stored after lead-created")
	}
	if thread.ThreadTS != newTS {
		t.Errorf("stored TS: got %q, want %q", thread.ThreadTS, newTS)
	}

	// --- Step 2: POST /vista-hills/lead-state-update ---
	updatePayload := slacksvc.LeadStateUpdateRequest{
		LeadID:    leadID,
		FromState: "new",
		ToState:   "qualified",
		QualifyingData: map[string]any{
			"care_for": "mother (84)",
			"area":     "Folsom",
		},
	}
	updateReq := postJSONReq(t, "/vista-hills/lead-state-update", updatePayload, secret)
	updateRR := httptest.NewRecorder()
	h.HandleLeadStateUpdate(updateRR, updateReq)

	if updateRR.Code != http.StatusOK {
		t.Fatalf("lead-state-update: got %d; body: %s", updateRR.Code, updateRR.Body.String())
	}

	// Thread reply posted with state transition.
	if len(slacker.replies) != 1 {
		t.Fatalf("expected 1 Slack reply after state-update, got %d", len(slacker.replies))
	}
	replyText := slacker.replies[0].Text
	if slacker.replies[0].ThreadTS != newTS {
		t.Errorf("state reply in wrong thread: got %q, want %q", slacker.replies[0].ThreadTS, newTS)
	}
	if !strings.Contains(strings.ToLower(replyText), "qualified") {
		t.Errorf("state reply should mention new state; got: %q", replyText)
	}

	// Log output.
	logOut := logs.String()
	if !strings.Contains(logOut, "vista-hills") {
		t.Errorf("log missing vista-hills entries; got: %s", logOut)
	}
}
