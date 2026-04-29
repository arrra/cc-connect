package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/chenhg5/cc-connect/core"
	slackplatform "github.com/chenhg5/cc-connect/platform/slack"
)

// freeTCPPort finds an available TCP port on loopback for a test server.
func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("freeTCPPort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// stubNonSlackPlatform is a core.Platform that is NOT a *slackplatform.Platform.
type stubNonSlackPlatform struct{}

func (s *stubNonSlackPlatform) Name() string                                        { return "stub" }
func (s *stubNonSlackPlatform) Start(_ core.MessageHandler) error                  { return nil }
func (s *stubNonSlackPlatform) Reply(_ context.Context, _ any, _ string) error     { return nil }
func (s *stubNonSlackPlatform) Send(_ context.Context, _ any, _ string) error      { return nil }
func (s *stubNonSlackPlatform) Stop() error                                         { return nil }

// TestWireTwilioBridge_DisabledWhenNoAccountSID verifies that the bridge returns nil
// and does not crash when TWILIO_ACCOUNT_SID is unset.
func TestWireTwilioBridge_DisabledWhenNoAccountSID(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "")

	srv := wireTwilioBridge(nil, t.TempDir())
	if srv != nil {
		srv.Stop()
		t.Fatal("expected nil server when TWILIO_ACCOUNT_SID not set")
	}
}

// TestWireTwilioBridge_SkipsWithoutSlackPlatform verifies that the bridge skips
// gracefully when no *slackplatform.Platform is present in the platform list.
func TestWireTwilioBridge_SkipsWithoutSlackPlatform(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "ACtest")

	platforms := []core.Platform{&stubNonSlackPlatform{}}
	srv := wireTwilioBridge(platforms, t.TempDir())
	if srv != nil {
		srv.Stop()
		t.Fatal("expected nil when no Slack platform in list")
	}
}

// TestWireTwilioBridge_SkipsWhenAdapterInitFails verifies that the bridge returns nil
// when TwilioAdapter.Init() fails (e.g., missing TWILIO_AUTH_TOKEN).
func TestWireTwilioBridge_SkipsWhenAdapterInitFails(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "ACtest")
	t.Setenv("TWILIO_AUTH_TOKEN", "")
	t.Setenv("TWILIO_FROM_NUMBER", "")

	p, err := slackplatform.New(map[string]any{
		"bot_token": "xoxb-test",
		"app_token": "xapp-test",
	})
	if err != nil {
		t.Fatalf("slackplatform.New: %v", err)
	}

	srv := wireTwilioBridge([]core.Platform{p}, t.TempDir())
	if srv != nil {
		srv.Stop()
		t.Fatal("expected nil server when TWILIO_AUTH_TOKEN is empty")
	}
}

// TestWireTwilioBridge_Wired verifies that with all required env vars set, the bridge:
// - starts an HTTP server
// - registers /twilio/inbound-sms, /vista-hills/lead-created, /vista-hills/lead-state-update
// - registers !sms and !call bang commands on the Slack platform
func TestWireTwilioBridge_Wired(t *testing.T) {
	port := freeTCPPort(t)

	t.Setenv("TWILIO_ACCOUNT_SID", "ACtest")
	t.Setenv("TWILIO_AUTH_TOKEN", "testtoken")
	t.Setenv("TWILIO_FROM_NUMBER", "+15005550006")
	t.Setenv("TWILIO_BRIDGE_PORT", fmt.Sprintf("%d", port))
	t.Setenv("SLACK_LEADS_CHANNEL", "#test-leads")
	t.Setenv("CC_CONNECT_VISTA_HILLS_SECRET", "test-vh-secret")

	// Build an unstarted *slackplatform.Platform.
	// Tokens are syntactically valid but non-functional; the platform is never Start()ed.
	p, err := slackplatform.New(map[string]any{
		"bot_token": "xoxb-test",
		"app_token": "xapp-test",
	})
	if err != nil {
		t.Fatalf("slackplatform.New: %v", err)
	}
	slackPlat := p.(*slackplatform.Platform)

	srv := wireTwilioBridge([]core.Platform{slackPlat}, t.TempDir())
	if srv == nil {
		t.Fatal("expected a running WebhookServer; got nil")
	}
	t.Cleanup(srv.Stop)

	// Probe all routes. A registered route returns 405 (wrong method) not 404.
	routes := []string{
		"/twilio/inbound-sms",
		"/twilio/lead-preamble",
		"/vista-hills/lead-created",
		"/vista-hills/lead-state-update",
	}
	base := fmt.Sprintf("http://localhost:%d", port)
	for _, route := range routes {
		resp, err := http.Get(base + route)
		if err != nil {
			t.Fatalf("GET %s: %v", route, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			t.Errorf("route %s not registered (got 404)", route)
		}
	}

	// Verify bang commands are registered.
	for _, cmd := range []string{"sms", "call"} {
		if !slackPlat.HasBangCmd(cmd) {
			t.Errorf("bang command !%s not registered on Slack platform", cmd)
		}
	}
}

// TestWireTwilioBridge_SkipsWhenNoLeadsChannel verifies that when SLACK_LEADS_CHANNEL is unset
// the bridge returns nil without crashing — no fail-open fallback to a hardcoded channel.
func TestWireTwilioBridge_SkipsWhenNoLeadsChannel(t *testing.T) {
	port := freeTCPPort(t)

	t.Setenv("TWILIO_ACCOUNT_SID", "ACtest")
	t.Setenv("TWILIO_AUTH_TOKEN", "testtoken")
	t.Setenv("TWILIO_FROM_NUMBER", "+15005550006")
	t.Setenv("TWILIO_BRIDGE_PORT", fmt.Sprintf("%d", port))
	t.Setenv("SLACK_LEADS_CHANNEL", "")

	p, err := slackplatform.New(map[string]any{
		"bot_token": "xoxb-test",
		"app_token": "xapp-test",
	})
	if err != nil {
		t.Fatalf("slackplatform.New: %v", err)
	}

	srv := wireTwilioBridge([]core.Platform{p}, t.TempDir())
	if srv != nil {
		srv.Stop()
		t.Fatal("expected nil server when SLACK_LEADS_CHANNEL is not set")
	}
}

// TestWireTwilioBridge_VistaHillsSkippedWhenNoSecret verifies that when
// CC_CONNECT_VISTA_HILLS_SECRET is unset, /vista-hills/* routes are NOT registered
// (no fail-open) while the rest of the bridge still starts normally.
func TestWireTwilioBridge_VistaHillsSkippedWhenNoSecret(t *testing.T) {
	port := freeTCPPort(t)

	t.Setenv("TWILIO_ACCOUNT_SID", "ACtest")
	t.Setenv("TWILIO_AUTH_TOKEN", "testtoken")
	t.Setenv("TWILIO_FROM_NUMBER", "+15005550006")
	t.Setenv("TWILIO_BRIDGE_PORT", fmt.Sprintf("%d", port))
	t.Setenv("SLACK_LEADS_CHANNEL", "#test-leads")
	t.Setenv("CC_CONNECT_VISTA_HILLS_SECRET", "")
	t.Setenv("CC_CONNECT_WEBHOOK_SECRET", "")

	p, err := slackplatform.New(map[string]any{
		"bot_token": "xoxb-test",
		"app_token": "xapp-test",
	})
	if err != nil {
		t.Fatalf("slackplatform.New: %v", err)
	}

	srv := wireTwilioBridge([]core.Platform{p}, t.TempDir())
	if srv == nil {
		t.Fatal("expected bridge server even without vista hills secret")
	}
	t.Cleanup(srv.Stop)

	base := fmt.Sprintf("http://localhost:%d", port)

	// Twilio inbound route must still be registered.
	resp, err := http.Get(base + "/twilio/inbound-sms")
	if err != nil {
		t.Fatalf("GET /twilio/inbound-sms: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Error("/twilio/inbound-sms should be registered even without vista hills secret")
	}

	// Vista Hills routes must NOT be registered (no fail-open).
	for _, route := range []string{"/vista-hills/lead-created", "/vista-hills/lead-state-update"} {
		resp, err := http.Get(base + route)
		if err != nil {
			t.Fatalf("GET %s: %v", route, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("route %s should NOT be registered when secret is missing (got %d, want 404)", route, resp.StatusCode)
		}
	}
}
