package twilio

import (
	"net/http"
	"regexp"
)

const leadPreambleTwiML = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<Response>` +
	`<Say voice="alice">This call may be recorded for quality and training purposes.</Say>` +
	`<Pause length="1"/>` +
	`</Response>`

var preambleE164Re = regexp.MustCompile(`^\+[0-9]{6,15}$`)

// HandleLeadPreamble serves consent TwiML to the called (lead) leg before bridging.
// Twilio fetches this URL via the <Number url="..."> attribute and plays the
// disclosure to the lead before connecting them to the agent.
// This satisfies California Penal Code § 632 two-party consent for the called party.
func HandleLeadPreamble(w http.ResponseWriter, r *http.Request) {
	// Validate the lead phone param (URL-decoded automatically by r.URL.Query()).
	// Reject anything that isn't a valid E.164 number — prevents log injection and
	// ensures the request is coming from a legitimate Twilio leg.
	lead := r.URL.Query().Get("lead")
	if lead != "" && !preambleE164Re.MatchString(lead) {
		http.Error(w, "invalid lead phone format", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(leadPreambleTwiML))
}
