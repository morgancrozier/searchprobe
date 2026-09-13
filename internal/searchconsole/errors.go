package searchconsole

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

// googleErrorBody is the standard Google API error envelope.
type googleErrorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Errors  []struct {
			Domain  string `json:"domain"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"errors"`
		Details []struct {
			Type   string `json:"@type"`
			Reason string `json:"reason"`
		} `json:"details"`
	} `json:"error"`
}

// MapHTTPError converts a non-2xx Google API response into a normalized error.
// Google's message is preserved in Message so humans and agents keep the
// upstream context, while Code provides the stable machine contract.
func MapHTTPError(status int, body []byte) *gscerr.Error {
	var gb googleErrorBody
	_ = json.Unmarshal(body, &gb)
	msg := strings.TrimSpace(gb.Error.Message)
	if msg == "" {
		msg = strings.TrimSpace(string(body))
		if len(msg) > 300 {
			msg = msg[:300] + "..."
		}
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	lower := strings.ToLower(msg)
	reasons := make([]string, 0, len(gb.Error.Errors)+len(gb.Error.Details))
	for _, e := range gb.Error.Errors {
		reasons = append(reasons, strings.ToLower(e.Reason))
	}
	for _, d := range gb.Error.Details {
		reasons = append(reasons, strings.ToLower(d.Reason))
	}
	hasReason := func(subs ...string) bool {
		for _, r := range reasons {
			for _, s := range subs {
				if strings.Contains(r, s) {
					return true
				}
			}
		}
		return false
	}
	googleMsg := fmt.Sprintf("Google API error (HTTP %d): %s", status, msg)

	switch {
	case status == http.StatusUnauthorized:
		return gscerr.New(gscerr.CodeAuthRevoked,
			"Google rejected the stored credentials: "+msg,
			"Run `gsc auth login` to sign in again.")

	case status == http.StatusTooManyRequests ||
		hasReason("ratelimitexceeded", "userratelimitexceeded") ||
		strings.Contains(lower, "rate limit"):
		return &gscerr.Error{Code: gscerr.CodeRateLimited, Message: googleMsg,
			Action: "Wait briefly and retry; reduce request frequency.", Retryable: true}

	case hasReason("quotaexceeded", "dailylimitexceeded") || gb.Error.Status == "RESOURCE_EXHAUSTED" ||
		strings.Contains(lower, "quota"):
		return &gscerr.Error{Code: gscerr.CodeQuotaExceeded, Message: googleMsg,
			Action: "Wait for the quota window to reset before retrying. See https://developers.google.com/webmaster-tools/limits."}

	case status == http.StatusForbidden:
		if hasReason("accessnotconfigured", "service_disabled") {
			return gscerr.New(gscerr.CodeConfigError, "The Google Search Console API is not enabled for this OAuth project: "+msg, "Enable Google Search Console API in your Google Cloud project, wait for activation, then rerun the command. No new client download is needed.")
		}
		if hasReason("insufficientpermissions") || strings.Contains(lower, "insufficient authentication scopes") {
			return gscerr.New(gscerr.CodeAuthScopeInsufficient,
				"The stored credentials lack the Search Console scope or the API is not enabled: "+msg,
				"Enable the Google Search Console API in the OAuth client's Google Cloud project, then run `gsc auth login` again.")
		}
		return gscerr.New(gscerr.CodePropertyAccessDenied,
			"The authenticated Google account cannot access this property: "+msg,
			"Run `gsc sites --json` to list accessible properties and pass one exactly as listed with --site.")

	case status == http.StatusNotFound:
		return gscerr.New(gscerr.CodePropertyNotFound,
			"Search Console property or resource not found: "+msg,
			"Run `gsc sites --json` to list accessible properties and pass one exactly as listed with --site.")

	case status == http.StatusBadRequest:
		switch {
		case strings.Contains(lower, "not under") || strings.Contains(lower, "outside") ||
			strings.Contains(lower, "not part of the property") || strings.Contains(lower, "not in property"):
			return gscerr.New(gscerr.CodeURLOutsideProperty,
				"The inspected URL is not within the specified property: "+msg,
				"Pass a --site property that contains the URL (for example sc-domain:example.com for any URL on example.com).")
		case strings.Contains(lower, "date"):
			return gscerr.New(gscerr.CodeInvalidDateRange,
				"Google rejected the date range: "+msg,
				"Adjust --days; Search Analytics dates use Pacific Time and are not available for future dates.")
		case strings.Contains(lower, "dimension"):
			return gscerr.New(gscerr.CodeInvalidDimensionCombination,
				"Google rejected the dimension combination: "+msg,
				"Use a supported combination of query, page, country, device, date, and searchAppearance.")
		default:
			return gscerr.New(gscerr.CodeInvalidArgument, googleMsg,
				"Check the command arguments and retry.")
		}

	case status >= 500:
		return &gscerr.Error{Code: gscerr.CodeGoogleAPIError, Message: googleMsg,
			Action: "Google returned a server error; retry shortly.", Retryable: true}
	}

	return gscerr.New(gscerr.CodeGoogleAPIError, googleMsg, "Retry the command; if it persists, check https://developers.google.com/webmaster-tools.")
}
