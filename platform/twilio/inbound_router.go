package twilio

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
)

// SlackPoster posts messages to Slack on behalf of the inbound router.
// This interface is implemented by the Slack platform; defined here to avoid
// a circular import between platform/twilio and platform/slack.
type SlackPoster interface {
	// PostMessage sends a new top-level message to channelID and returns the message timestamp.
	PostMessage(ctx context.Context, channelID, text string) (ts string, err error)
	// PostReply sends a threaded reply to an existing message.
	PostReply(ctx context.Context, channelID, threadTS, text string) error
}

// InboundRouter handles Twilio inbound SMS webhooks and routes them to Slack.
//
// Known leads → reply in the existing Slack thread.
// Orphan inbound (no known thread) → create a new top-level post in leadsChannel.
type InboundRouter struct {
	adapter      *TwilioAdapter
	slack        SlackPoster
	store        *PhoneThreadStore
	leadsChannel string
}

// NewInboundRouter creates an InboundRouter.
// leadsChannel comes from the SLACK_LEADS_CHANNEL env var if not provided explicitly;
// defaults to "#chief-of-staff".
func NewInboundRouter(adapter *TwilioAdapter, slack SlackPoster, store *PhoneThreadStore, leadsChannel string) *InboundRouter {
	if leadsChannel == "" {
		leadsChannel = os.Getenv("SLACK_LEADS_CHANNEL")
	}
	if leadsChannel == "" {
		leadsChannel = "#chief-of-staff"
	}
	return &InboundRouter{
		adapter:      adapter,
		slack:        slack,
		store:        store,
		leadsChannel: leadsChannel,
	}
}

// HandleInbound is the HTTP handler for POST /twilio/inbound-sms.
// It verifies the Twilio signature, routes to the correct Slack thread,
// and returns 200 on success, 403 on signature failure, 500 on Slack errors.
func (r *InboundRouter) HandleInbound(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	inbound, err := r.adapter.HandleInbound(req)
	if err != nil {
		slog.Warn("[twilio-inbound] rejected", "error", err)
		http.Error(w, "invalid request", http.StatusForbidden)
		return
	}

	ctx := req.Context()
	thread, known := r.store.GetThread(inbound.From)

	var threadTS string
	if known {
		if err := r.slack.PostReply(ctx, thread.Channel, thread.ThreadTS, inbound.Body); err != nil {
			slog.Error("[twilio-inbound] slack reply failed",
				"from", maskPhone(inbound.From),
				"sid", inbound.MessageSID,
				"error", err,
			)
			http.Error(w, "slack error", http.StatusInternalServerError)
			return
		}
		threadTS = thread.ThreadTS
	} else {
		msg := fmt.Sprintf("📱 Inbound SMS from %s\nAI not yet engaged — orphan inbound\n\n%s",
			maskPhone(inbound.From), inbound.Body)
		ts, err := r.slack.PostMessage(ctx, r.leadsChannel, msg)
		if err != nil {
			slog.Error("[twilio-inbound] slack post failed",
				"from", maskPhone(inbound.From),
				"sid", inbound.MessageSID,
				"error", err,
			)
			http.Error(w, "slack error", http.StatusInternalServerError)
			return
		}
		threadTS = ts
		if err := r.store.SetThread(inbound.From, LeadThread{
			Channel:  r.leadsChannel,
			ThreadTS: ts,
		}); err != nil {
			// Non-fatal: the Slack post succeeded; log and continue.
			slog.Error("[twilio-inbound] store thread failed",
				"from", maskPhone(inbound.From),
				"error", err,
			)
		}
	}

	slog.Info("[twilio-inbound]",
		"from", maskPhone(inbound.From),
		"sid", inbound.MessageSID,
		"slack_ts", threadTS,
	)
	w.WriteHeader(http.StatusOK)
}
