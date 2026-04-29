package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/chenhg5/cc-connect/core"
	slackplatform "github.com/chenhg5/cc-connect/platform/slack"
	"github.com/chenhg5/cc-connect/platform/slack/commands"
	"github.com/chenhg5/cc-connect/platform/twilio"
)

// wireTwilioBridge initializes the Twilio bridge when TWILIO_ACCOUNT_SID is set.
// Returns the running WebhookServer so the caller can Stop() it on shutdown,
// or nil if the bridge is disabled or can't be initialized.
//
// Bridge is skipped (not a fatal error) when:
//   - TWILIO_ACCOUNT_SID is unset
//   - No Slack platform found in the provided platform list
//   - TwilioAdapter.Init() fails (missing auth token or from-number)
func wireTwilioBridge(platforms []core.Platform, dataDir string) *core.WebhookServer {
	if os.Getenv("TWILIO_ACCOUNT_SID") == "" {
		slog.Info("Twilio bridge disabled — TWILIO_ACCOUNT_SID not set")
		return nil
	}

	// Find the Slack platform — needed for posting messages and registering bang commands.
	var slackPlat *slackplatform.Platform
	for _, p := range platforms {
		if sp, ok := p.(*slackplatform.Platform); ok {
			slackPlat = sp
			break
		}
	}
	if slackPlat == nil {
		slog.Warn("twilio bridge: no Slack platform found — bridge wiring skipped")
		return nil
	}

	// Initialize adapter; Init() validates all required env vars.
	adapter := &twilio.TwilioAdapter{}
	if err := adapter.Init(); err != nil {
		slog.Warn("twilio bridge disabled", "error", err)
		return nil
	}

	// Load phone→thread store from disk (in-memory when path is empty).
	storePath := filepath.Join(dataDir, "twilio_threads.json")
	store := twilio.NewPhoneThreadStore(storePath)

	leadsChannel := os.Getenv("SLACK_LEADS_CHANNEL")

	// InboundRouter: routes inbound SMS webhooks to the correct Slack thread.
	// Fails if neither the explicit arg nor SLACK_LEADS_CHANNEL is set — no fallback
	// to avoid leaking lead PII to the wrong Slack channel.
	inboundRouter, err := twilio.NewInboundRouter(adapter, slackPlat, store, leadsChannel)
	if err != nil {
		slog.Warn("twilio bridge disabled — SLACK_LEADS_CHANNEL not set", "error", err)
		return nil
	}

	// VistaHillsHandler: surfaces lead lifecycle events in Slack threads.
	// Secret from CC_CONNECT_VISTA_HILLS_SECRET; falls back to CC_CONNECT_WEBHOOK_SECRET.
	// Constructor returns error when no secret is available — routes are skipped rather than fail-open.
	secret := os.Getenv("CC_CONNECT_VISTA_HILLS_SECRET")
	vh, vhErr := slackplatform.NewVistaHillsHandler(slackPlat, store, secret, leadsChannel)
	if vhErr != nil {
		slog.Warn("VistaHills bridge disabled", "error", vhErr)
	}

	// Bang commands wired onto the Slack platform.
	smsCmd := &commands.SmsCmd{
		Twilio: adapter,
		Store:  store,
		Slack:  slackPlat,
	}
	callCmd := &commands.CallCmd{
		Twilio: adapter,
		Store:  store,
		Slack:  slackPlat,
	}

	// HTTP server for Twilio and Vista Hills webhooks.
	// Port: TWILIO_BRIDGE_PORT env (default 9112). Token-free — each handler
	// has its own auth (Twilio HMAC-SHA1 / Vista Hills shared secret).
	port := 9112
	if p := os.Getenv("TWILIO_BRIDGE_PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			port = n
		}
	}
	srv := core.NewWebhookServer(port, "", "/twilio")
	// The Twilio bridge uses only specific authenticated routes; the generic
	// handleHook endpoint would be unauthenticated (empty token) and has no
	// engines registered. Disable it so /twilio returns 404 for unknown paths.
	srv.NoGenericHook = true
	srv.Handle("/twilio/inbound-sms", inboundRouter.HandleInbound)
	srv.Handle("/twilio/lead-preamble", twilio.HandleLeadPreamble)

	// VistaHillsHandler requires a non-empty secret; skip routes if unconfigured.
	if vh != nil {
		srv.Handle("/vista-hills/lead-created", vh.HandleLeadCreated)
		srv.Handle("/vista-hills/lead-state-update", vh.HandleLeadStateUpdate)
	}
	srv.Start()

	// Register bang commands on the Slack platform.
	slackPlat.RegisterBangCmd("sms", slackplatform.BangCmdFunc(smsCmd.Handle))
	slackPlat.RegisterBangCmd("call", slackplatform.BangCmdFunc(callCmd.Handle))

	slog.Info("twilio bridge: wired",
		"port", port,
		"leads_channel", leadsChannel,
	)
	return srv
}
