package twilio

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// httpClient is the interface used for HTTP requests, allowing test injection.
type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// client wraps the Twilio REST API with Basic Auth.
type client struct {
	accountSID string
	authToken  string
	baseURL    string
	http       httpClient
}

func newClient(accountSID, authToken string) *client {
	return &client{
		accountSID: accountSID,
		authToken:  authToken,
		baseURL:    "https://api.twilio.com",
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// post sends a form-encoded POST to the given Twilio API path and decodes the
// JSON response body into dest. Returns the raw *http.Response for status checks.
func (c *client) post(path string, form url.Values, dest io.Writer) (*http.Response, error) {
	u := fmt.Sprintf("%s%s", c.baseURL, path)
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("twilio: build request: %w", err)
	}
	req.SetBasicAuth(c.accountSID, c.authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("twilio: http: %w", err)
	}

	if dest != nil {
		if _, err := io.Copy(dest, resp.Body); err != nil {
			resp.Body.Close()
			return resp, fmt.Errorf("twilio: read body: %w", err)
		}
		resp.Body.Close()
	}

	return resp, nil
}
