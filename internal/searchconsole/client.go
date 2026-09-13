// Package searchconsole is a small, direct REST client for the Google Search
// Console API. It owns request construction, error normalization, and
// response normalization so that CLI commands stay thin.
package searchconsole

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

const (
	// DefaultBaseURL serves sites and Search Analytics (webmasters v3).
	DefaultBaseURL = "https://www.googleapis.com/webmasters/v3"
	// DefaultInspectionBaseURL serves the URL Inspection API (v1).
	DefaultInspectionBaseURL = "https://searchconsole.googleapis.com/v1"

	// Timezone is the fixed time zone Search Analytics uses for dates.
	Timezone = "America/Los_Angeles"

	maxAttempts = 3
	userAgent   = "SearchProbe/gsc (+https://github.com/morgancrozier/searchprobe)"
)

// Client talks to the Search Console API using an authenticated http.Client.
type Client struct {
	HTTP              *http.Client
	BaseURL           string
	InspectionBaseURL string
	// Sleep is used between retries; overridable for tests.
	Sleep func(time.Duration)
	// MaxAutoPages is the defensive page guard for QueryPerformanceAll;
	// zero means DefaultMaxAutoPages.
	MaxAutoPages int
}

// New returns a Client using hc for transport. hc must attach OAuth credentials.
func New(hc *http.Client) *Client {
	return &Client{
		HTTP:              hc,
		BaseURL:           DefaultBaseURL,
		InspectionBaseURL: DefaultInspectionBaseURL,
		Sleep:             time.Sleep,
	}
}

// do performs a JSON request with bounded retries for transient failures and
// decodes a 2xx response into out.
func (c *Client) do(ctx context.Context, method, url string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return gscerr.Wrap(err, gscerr.CodeInternal, "Could not encode request.", "")
		}
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			return gscerr.Wrap(err, gscerr.CodeInternal, "Could not build request.", "")
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", userAgent)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = mapTransportError(err)
		} else {
			respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
			resp.Body.Close()
			if readErr != nil {
				lastErr = gscerr.Wrap(readErr, gscerr.CodeNetworkError, "Could not read the Google API response.", "Retry the command.")
			} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if out == nil || len(bytes.TrimSpace(respBody)) == 0 {
					return nil
				}
				if err := json.Unmarshal(respBody, out); err != nil {
					return gscerr.Wrap(err, gscerr.CodeGoogleAPIError, "Google returned a response gsc could not parse.", "Retry the command; if it persists, report a bug with the command used.")
				}
				return nil
			} else {
				lastErr = MapHTTPError(resp.StatusCode, respBody)
			}
		}

		ge := gscerr.From(lastErr)
		if !ge.Retryable || attempt == maxAttempts || ctx.Err() != nil {
			return lastErr
		}
		c.Sleep(time.Duration(attempt*attempt) * 500 * time.Millisecond)
	}
	return lastErr
}

func mapTransportError(err error) error {
	var ge *gscerr.Error
	if errors.As(err, &ge) {
		// Token refresh failures surface through the OAuth transport.
		return ge
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return gscerr.Wrap(err, gscerr.CodeNetworkError, "The request was cancelled or timed out.", "Retry the command.")
	}
	return &gscerr.Error{
		Code:      gscerr.CodeNetworkError,
		Message:   fmt.Sprintf("Could not reach the Google Search Console API: %v", err),
		Action:    "Check your network connection and retry.",
		Retryable: true,
		Cause:     err,
	}
}
