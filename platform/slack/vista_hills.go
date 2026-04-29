package slack

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	tw "github.com/chenhg5/cc-connect/platform/twilio"
)

// vistaSlackPoster posts messages to Slack on behalf of the Vista Hills handler.
type vistaSlackPoster interface {
	PostMessage(ctx context.Context, channelID, text string) (ts string, err error)
	PostReply(ctx context.Context, channelID, threadTS, text string) error
}

// LeadCreatedRequest is the payload from the Worker for POST /vista-hills/lead-created.
type LeadCreatedRequest struct {
	LeadID          int64  `json:"lead_id"`
	Phone           string `json:"phone"`
	Name            string `json:"name"`
	Source          string `json:"source"`
	Campaign        string `json:"campaign"`
	Email           string `json:"email,omitempty"`
	FormSubmittedAt string `json:"form_submitted_at"`
}

// LeadStateUpdateRequest is the payload for POST /vista-hills/lead-state-update.
type LeadStateUpdateRequest struct {
	LeadID          int64          `json:"lead_id"`
	FromState       string         `json:"from_state"`
	ToState         string         `json:"to_state"`
	QualifyingData  map[string]any `json:"qualifying_data,omitempty"`
}

// VistaHillsHandler handles Vista Hills Worker webhooks that surface lead state
// in Slack threads.
//
//   POST /vista-hills/lead-created       → create top-level Slack post, store thread_ts
//   POST /vista-hills/lead-state-update  → post state-transition reply in lead thread
type VistaHillsHandler struct {
	slack        vistaSlackPoster
	store        *tw.PhoneThreadStore
	secret       string
	leadsChannel string

	mu          sync.RWMutex
	leadPhones  map[int64]string    // lead_id → E.164 phone (populated by lead-created)
	seenUpdates map[string]struct{} // "lead_id:to_state" idempotency set
}

// NewVistaHillsHandler creates a VistaHillsHandler.
// leadsChannel defaults to $SLACK_LEADS_CHANNEL or "#chief-of-staff" if empty.
// secret falls back to $CC_CONNECT_WEBHOOK_SECRET if the arg is empty.
// Returns an error if no secret is available — an empty secret would make the
// endpoint accept all requests (fail-open), which is a security violation.
func NewVistaHillsHandler(slack vistaSlackPoster, store *tw.PhoneThreadStore, secret, leadsChannel string) (*VistaHillsHandler, error) {
	if secret == "" {
		secret = os.Getenv("CC_CONNECT_WEBHOOK_SECRET")
	}
	if secret == "" {
		return nil, fmt.Errorf("CC_CONNECT_VISTA_HILLS_SECRET (or CC_CONNECT_WEBHOOK_SECRET) must be set")
	}
	if leadsChannel == "" {
		leadsChannel = os.Getenv("SLACK_LEADS_CHANNEL")
	}
	if leadsChannel == "" {
		leadsChannel = "#chief-of-staff"
	}
	return &VistaHillsHandler{
		slack:        slack,
		store:        store,
		secret:       secret,
		leadsChannel: leadsChannel,
		leadPhones:   make(map[int64]string),
		seenUpdates:  make(map[string]struct{}),
	}, nil
}

// HandleLeadCreated is the HTTP handler for POST /vista-hills/lead-created.
func (h *VistaHillsHandler) HandleLeadCreated(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !h.authenticate(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req LeadCreatedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Phone == "" || req.LeadID == 0 {
		http.Error(w, "phone and lead_id are required", http.StatusBadRequest)
		return
	}

	text := h.formatLeadBlock(req)
	ctx := r.Context()

	ts, err := h.slack.PostMessage(ctx, h.leadsChannel, text)
	if err != nil {
		slog.Error("[vista-hills] slack post failed",
			"lead_id", req.LeadID,
			"phone", maskPhoneVH(req.Phone),
			"error", err,
		)
		http.Error(w, "slack error", http.StatusInternalServerError)
		return
	}

	if err := h.store.SetThread(req.Phone, tw.LeadThread{
		Channel:  h.leadsChannel,
		ThreadTS: ts,
	}); err != nil {
		slog.Error("[vista-hills] store thread failed",
			"lead_id", req.LeadID,
			"phone", maskPhoneVH(req.Phone),
			"error", err,
		)
	}

	h.mu.Lock()
	h.leadPhones[req.LeadID] = req.Phone
	h.mu.Unlock()

	slog.Info("[vista-hills] lead-created",
		"lead_id", req.LeadID,
		"phone", maskPhoneVH(req.Phone),
		"source", req.Source,
		"slack_ts", ts,
	)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"slack_thread_ts": ts,
	})
}

// HandleLeadStateUpdate is the HTTP handler for POST /vista-hills/lead-state-update.
func (h *VistaHillsHandler) HandleLeadStateUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !h.authenticate(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req LeadStateUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.LeadID == 0 || req.ToState == "" {
		http.Error(w, "lead_id and to_state are required", http.StatusBadRequest)
		return
	}

	// Idempotency: skip duplicate transitions.
	idempKey := fmt.Sprintf("%d:%s", req.LeadID, req.ToState)
	h.mu.Lock()
	_, seen := h.seenUpdates[idempKey]
	if !seen {
		h.seenUpdates[idempKey] = struct{}{}
	}
	h.mu.Unlock()
	if seen {
		slog.Debug("[vista-hills] lead-state-update duplicate, skipped",
			"lead_id", req.LeadID,
			"to_state", req.ToState,
		)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Look up thread via lead_id → phone → thread_ts.
	h.mu.RLock()
	phone, phoneKnown := h.leadPhones[req.LeadID]
	h.mu.RUnlock()
	if !phoneKnown {
		slog.Warn("[vista-hills] lead-state-update: unknown lead_id, no phone mapping",
			"lead_id", req.LeadID,
		)
		http.Error(w, "lead not found", http.StatusNotFound)
		return
	}

	thread, ok := h.store.GetThread(phone)
	if !ok {
		slog.Warn("[vista-hills] lead-state-update: no slack thread for phone",
			"lead_id", req.LeadID,
			"phone", maskPhoneVH(phone),
		)
		http.Error(w, "no slack thread for lead", http.StatusNotFound)
		return
	}

	text := h.formatStateUpdate(req)
	ctx := r.Context()

	if err := h.slack.PostReply(ctx, thread.Channel, thread.ThreadTS, text); err != nil {
		slog.Error("[vista-hills] slack reply failed",
			"lead_id", req.LeadID,
			"phone", maskPhoneVH(phone),
			"error", err,
		)
		http.Error(w, "slack error", http.StatusInternalServerError)
		return
	}

	slog.Info("[vista-hills] lead-state-update",
		"lead_id", req.LeadID,
		"phone", maskPhoneVH(phone),
		"from_state", req.FromState,
		"to_state", req.ToState,
	)
	w.WriteHeader(http.StatusOK)
}

// authenticate checks the X-CC-Connect-Secret header.
// Returns false unconditionally when secret is empty — empty secret means the
// handler was constructed incorrectly; refusing all requests is safer than fail-open.
func (h *VistaHillsHandler) authenticate(r *http.Request) bool {
	if h.secret == "" {
		return false
	}
	got := r.Header.Get("X-CC-Connect-Secret")
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.secret)) == 1
}

func (h *VistaHillsHandler) formatLeadBlock(req LeadCreatedRequest) string {
	localTime := ""
	if req.FormSubmittedAt != "" {
		if t, err := time.Parse(time.RFC3339, req.FormSubmittedAt); err == nil {
			// Display in US/Pacific — Vista Hills is Sacramento-area.
			loc, locErr := time.LoadLocation("America/Los_Angeles")
			if locErr == nil {
				t = t.In(loc)
			}
			localTime = t.Format("3:04 PM MST")
		}
	}

	source := req.Source
	if source == "" {
		source = "unknown"
	}

	line1 := fmt.Sprintf("🎯 New %s lead — %s", source, req.Name)
	line2 := fmt.Sprintf("📞 %s", formatPhoneVH(req.Phone))
	if req.Email != "" {
		line2 += " | " + req.Email
	}
	line3 := fmt.Sprintf("🕐 %s | Source: %s", localTime, req.Campaign)
	line4 := "Status: AI texting now…"

	return line1 + "\n" + line2 + "\n" + line3 + "\n" + line4
}

func (h *VistaHillsHandler) formatStateUpdate(req LeadStateUpdateRequest) string {
	icon := "🔄"
	if req.ToState == "qualified" {
		icon = "✅"
	} else if req.ToState == "disqualified" || req.ToState == "rejected" {
		icon = "❌"
	} else if req.ToState == "scheduled" {
		icon = "📅"
	}

	text := fmt.Sprintf("%s Lead %s → %s", icon, req.FromState, req.ToState)
	if len(req.QualifyingData) > 0 {
		parts := make([]string, 0, len(req.QualifyingData))
		for k, v := range req.QualifyingData {
			parts = append(parts, fmt.Sprintf("%s: %v", k, v))
		}
		text += " — " + joinStrings(parts, ", ")
	}
	return text
}

func formatPhoneVH(phone string) string {
	digits := ""
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			digits += string(r)
		}
	}
	if len(digits) == 11 && digits[0] == '1' {
		digits = digits[1:]
	}
	if len(digits) == 10 {
		return fmt.Sprintf("(%s) %s-%s", digits[0:3], digits[3:6], digits[6:10])
	}
	return phone
}

func maskPhoneVH(phone string) string {
	if len(phone) < 8 {
		return "***"
	}
	mask := ""
	for range phone[3 : len(phone)-4] {
		mask += "*"
	}
	return phone[:3] + mask + phone[len(phone)-4:]
}

func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	out := ss[0]
	for _, s := range ss[1:] {
		out += sep + s
	}
	return out
}
