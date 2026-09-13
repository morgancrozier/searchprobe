package searchconsole

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New(srv.Client())
	c.BaseURL = srv.URL + "/webmasters/v3"
	c.InspectionBaseURL = srv.URL + "/v1"
	c.Sleep = func(time.Duration) {}
	return c, srv
}

func TestListSites(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/webmasters/v3/sites" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept header missing")
		}
		_, _ = io.WriteString(w, `{"siteEntry":[{"siteUrl":"https://www.example.com/","permissionLevel":"siteFullUser"},{"siteUrl":"sc-domain:example.com","permissionLevel":"siteOwner"}]}`)
	})
	sites, err := c.ListSites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 {
		t.Fatalf("len = %d", len(sites))
	}
	if sites[0].SiteURL != "https://www.example.com/" || sites[0].Type != "urlPrefix" {
		t.Errorf("sites[0] = %+v", sites[0])
	}
	if sites[1].SiteURL != "sc-domain:example.com" || sites[1].Type != "domain" || sites[1].PermissionLevel != "siteOwner" {
		t.Errorf("sites[1] = %+v", sites[1])
	}
}

func TestListSitesEmpty(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	})
	sites, err := c.ListSites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sites == nil || len(sites) != 0 {
		t.Errorf("want empty non-nil slice, got %#v", sites)
	}
}

func TestQueryPerformanceRequestAndNormalization(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "search_analytics_query.json"))
	})

	res, err := c.QueryPerformance(context.Background(), PerformanceRequest{
		Site:       "sc-domain:example.com",
		StartDate:  "2026-08-15",
		EndDate:    "2026-09-11",
		Dimensions: []string{"query"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/webmasters/v3/sites/sc-domain:example.com/searchAnalytics/query" {
		t.Errorf("path = %s", gotPath)
	}
	if gotBody["startDate"] != "2026-08-15" || gotBody["endDate"] != "2026-09-11" || gotBody["dataState"] != "final" || gotBody["type"] != "web" {
		t.Errorf("body = %v", gotBody)
	}
	if gotBody["rowLimit"] != float64(DefaultRowLimit) {
		t.Errorf("rowLimit = %v", gotBody["rowLimit"])
	}
	if _, has := gotBody["startRow"]; has {
		t.Errorf("startRow should be omitted when zero")
	}

	if len(res.Rows) != 3 || res.ResponseAggregationType != "byProperty" || !res.PaginationExhausted || res.PagesFetched != 1 {
		t.Errorf("result = %+v", res)
	}
	r0 := res.Rows[0]
	if r0.Keys["query"] != "example search" || r0.Clicks != 42 || r0.Impressions != 910 || r0.Position != 6.3 {
		t.Errorf("row0 = %+v", r0)
	}
	clicks, imps := res.Totals()
	if clicks != 49 || imps != 1045 {
		t.Errorf("totals = %v %v", clicks, imps)
	}

	b, err := json.Marshal(r0)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"query":"example search","clicks":42,"impressions":910,"ctr":0.046153846153846156,"position":6.3}`
	if string(b) != want {
		t.Errorf("row JSON:\n got %s\nwant %s", b, want)
	}
}

func TestQueryPerformanceURLPrefixSiteEscaping(t *testing.T) {
	var gotPath string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = io.WriteString(w, `{"rows":[]}`)
	})
	_, err := c.QueryPerformance(context.Background(), PerformanceRequest{
		Site: "https://www.example.com/", StartDate: "2026-09-01", EndDate: "2026-09-02", RowLimit: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotPath, "/sites/https:%2F%2Fwww.example.com%2F/searchAnalytics/query") {
		t.Errorf("site URL not path-escaped: %s", gotPath)
	}
}

func TestQueryPerformancePaginationIncomplete(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"rows":[{"keys":["a"],"clicks":1,"impressions":1,"ctr":1,"position":1},{"keys":["b"],"clicks":1,"impressions":1,"ctr":1,"position":1}]}`)
	})
	res, err := c.QueryPerformance(context.Background(), PerformanceRequest{
		Site: "sc-domain:example.com", StartDate: "2026-09-01", EndDate: "2026-09-02", Dimensions: []string{"page"}, RowLimit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.PaginationExhausted {
		t.Errorf("rows == rowLimit must not claim pagination complete")
	}
}

func TestQueryPerformanceMultiDimensionRow(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"rows":[{"keys":["2026-09-01","MOBILE"],"clicks":3,"impressions":30,"ctr":0.1,"position":2}]}`)
	})
	res, err := c.QueryPerformance(context.Background(), PerformanceRequest{
		Site: "sc-domain:example.com", StartDate: "2026-09-01", EndDate: "2026-09-02", Dimensions: []string{"date", "device"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res.Rows[0])
	if string(b) != `{"date":"2026-09-01","device":"MOBILE","clicks":3,"impressions":30,"ctr":0.1,"position":2}` {
		t.Errorf("row JSON = %s", b)
	}
}

func TestPerformanceRequestValidate(t *testing.T) {
	base := PerformanceRequest{Site: "sc-domain:example.com", StartDate: "2026-09-01", EndDate: "2026-09-02"}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*PerformanceRequest)
		code string
	}{
		{"no site", func(r *PerformanceRequest) { r.Site = "" }, gscerr.CodeInvalidArgument},
		{"bad start", func(r *PerformanceRequest) { r.StartDate = "09/01/2026" }, gscerr.CodeInvalidDateRange},
		{"reversed", func(r *PerformanceRequest) { r.StartDate, r.EndDate = r.EndDate, r.StartDate }, gscerr.CodeInvalidDateRange},
		{"limit too high", func(r *PerformanceRequest) { r.RowLimit = MaxRowLimit + 1 }, gscerr.CodeInvalidArgument},
		{"negative start", func(r *PerformanceRequest) { r.StartRow = -1 }, gscerr.CodeInvalidArgument},
	}
	for _, tc := range cases {
		r := base
		tc.mut(&r)
		err := r.Validate()
		if gscerr.From(err) == nil || gscerr.From(err).Code != tc.code {
			t.Errorf("%s: got %v, want %s", tc.name, err, tc.code)
		}
	}
}

func TestParseDimensions(t *testing.T) {
	got, err := ParseDimensions(" Query, page ,SEARCHAPPEARANCE,device,country,date")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"query", "page", "searchAppearance", "device", "country", "date"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v", got)
	}
	if got, err := ParseDimensions(""); err != nil || got != nil {
		t.Errorf("empty: %v %v", got, err)
	}
	if got, err := ParseDimensions("hour"); err != nil || len(got) != 1 || got[0] != "hour" {
		t.Errorf("hour must parse (validated against data state later): %v %v", got, err)
	}
	for _, bad := range []string{"query,query", "bogus"} {
		if _, err := ParseDimensions(bad); gscerr.From(err) == nil || gscerr.From(err).Code != gscerr.CodeInvalidArgument {
			t.Errorf("%q: want INVALID_ARGUMENT, got %v", bad, err)
		}
	}
}

func TestDateRangeForDays(t *testing.T) {
	// 2026-09-12 03:00 UTC is 2026-09-11 20:00 PDT; "yesterday" in PT is 09-10.
	now := time.Date(2026, 9, 12, 3, 0, 0, 0, time.UTC)
	start, end, err := DateRangeForDays(now, 28)
	if err != nil {
		t.Fatal(err)
	}
	if end != "2026-09-10" || start != "2026-08-14" {
		t.Errorf("got %s..%s", start, end)
	}
	start, end, _ = DateRangeForDays(now, 1)
	if start != end || end != "2026-09-10" {
		t.Errorf("1 day: %s..%s", start, end)
	}
	if _, _, err := DateRangeForDays(now, 0); gscerr.From(err).Code != gscerr.CodeInvalidDateRange {
		t.Errorf("0 days: %v", err)
	}
}

func TestInspectURL(t *testing.T) {
	var gotBody map[string]any
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/urlInspection/index:inspect" || r.Method != "POST" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write(fixture(t, "url_inspection.json"))
	})
	res, err := c.InspectURL(context.Background(), InspectionRequest{Site: "sc-domain:example.com", InspectionURL: "https://example.com/page"})
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["inspectionUrl"] != "https://example.com/page" || gotBody["siteUrl"] != "sc-domain:example.com" {
		t.Errorf("body = %v", gotBody)
	}
	if _, has := gotBody["languageCode"]; has {
		t.Errorf("languageCode should be omitted when unset")
	}
	is := res.IndexStatus
	if is == nil || is.Verdict != "PASS" || is.CoverageState != "Submitted and indexed" || is.GoogleCanonical != "https://example.com/page" || is.CrawledAs != "MOBILE" {
		t.Errorf("indexStatus = %+v", is)
	}
	if len(is.Sitemaps) != 1 || len(is.ReferringURLs) != 1 {
		t.Errorf("lists = %+v", is)
	}
	if res.RichResults == nil || res.RichResults.DetectedItems[0].Type != "Breadcrumbs" {
		t.Errorf("richResults = %+v", res.RichResults)
	}
	if res.MobileUsability != nil || res.AMP != nil {
		t.Errorf("absent sub-results must stay nil")
	}
	b, _ := json.Marshal(res)
	if strings.Contains(string(b), `"mobileUsability"`) || !strings.Contains(string(b), `"referringUrls":["https://example.com/"]`) {
		t.Errorf("json = %s", b)
	}
}

func TestInspectURLEmptyLists(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"inspectionResult":{"indexStatusResult":{"verdict":"NEUTRAL","coverageState":"URL is unknown to Google"}}}`)
	})
	res, err := c.InspectURL(context.Background(), InspectionRequest{Site: "https://example.com/", InspectionURL: "https://example.com/missing"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res.IndexStatus)
	if !strings.Contains(string(b), `"sitemaps":[]`) || !strings.Contains(string(b), `"referringUrls":[]`) {
		t.Errorf("empty lists must serialize as []: %s", b)
	}
}

func TestInspectionRequestValidate(t *testing.T) {
	cases := []struct {
		site, url, code string
	}{
		// Domain properties: hostname or any subdomain; scheme and path are not compared.
		{"sc-domain:example.com", "https://example.com/a", ""},
		{"sc-domain:example.com", "http://Blog.Example.com/a?x=1", ""},
		{"sc-domain:example.com", "https://support.m.example.com/deep/path", ""},
		{"sc-domain:example.com", "https://notexample.com/a", gscerr.CodeURLOutsideProperty},
		{"sc-domain:example.com", "https://example.com.evil.net/a", gscerr.CodeURLOutsideProperty},
		// URL-prefix properties: only a different scheme or hostname is certainly outside.
		{"https://www.example.com/", "https://www.example.com/a", ""},
		{"https://www.example.com/", "HTTPS://WWW.EXAMPLE.COM/A", ""},
		{"https://www.example.com/blog/", "https://www.example.com/other", ""}, // paths left to Google
		{"https://www.example.com/", "https://example.com/a", gscerr.CodeURLOutsideProperty},
		{"https://www.example.com/", "http://www.example.com/a", gscerr.CodeURLOutsideProperty},
		// Malformed inputs.
		{"sc-domain:example.com", "example.com/a", gscerr.CodeInvalidArgument},
		{"sc-domain:example.com", "", gscerr.CodeInvalidArgument},
		{"", "https://example.com/a", gscerr.CodeInvalidArgument},
	}
	for _, tc := range cases {
		err := InspectionRequest{Site: tc.site, InspectionURL: tc.url}.Validate()
		got := ""
		if err != nil {
			got = gscerr.From(err).Code
		}
		if got != tc.code {
			t.Errorf("site=%q url=%q: got %q want %q", tc.site, tc.url, got, tc.code)
		}
	}
}

func TestMapHTTPError(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		code      string
		retryable bool
	}{
		{"401", 401, `{"error":{"code":401,"message":"Request had invalid authentication credentials.","status":"UNAUTHENTICATED"}}`, gscerr.CodeAuthRevoked, false},
		{"403 permission", 403, string(fixture(t, "error_403_permission.json")), gscerr.CodePropertyAccessDenied, false},
		{"403 scope", 403, `{"error":{"code":403,"message":"Request had insufficient authentication scopes.","errors":[{"reason":"insufficientPermissions"}]}}`, gscerr.CodeAuthScopeInsufficient, false},
		{"403 api disabled", 403, `{"error":{"code":403,"message":"Google Search Console API has not been used in project 123 before or it is disabled.","errors":[{"reason":"accessNotConfigured"}]}}`, gscerr.CodeConfigError, false},
		{"403 quota", 403, `{"error":{"code":403,"message":"Quota exceeded for quota metric","errors":[{"reason":"quotaExceeded"}],"status":"RESOURCE_EXHAUSTED"}}`, gscerr.CodeQuotaExceeded, false},
		{"403 rate", 403, `{"error":{"code":403,"message":"Rate Limit Exceeded","errors":[{"reason":"rateLimitExceeded"}]}}`, gscerr.CodeRateLimited, true},
		{"429", 429, `{"error":{"code":429,"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED"}}`, gscerr.CodeRateLimited, true},
		{"404", 404, `{"error":{"code":404,"message":"Not found"}}`, gscerr.CodePropertyNotFound, false},
		{"400 date", 400, `{"error":{"code":400,"message":"startDate must be less than or equal to endDate"}}`, gscerr.CodeInvalidDateRange, false},
		{"400 dimension", 400, `{"error":{"code":400,"message":"Invalid dimension combination"}}`, gscerr.CodeInvalidDimensionCombination, false},
		{"400 outside", 400, `{"error":{"code":400,"message":"URL is not under the specified property"}}`, gscerr.CodeURLOutsideProperty, false},
		{"400 other", 400, `{"error":{"code":400,"message":"Invalid value"}}`, gscerr.CodeInvalidArgument, false},
		{"500", 500, `{"error":{"code":500,"message":"Backend Error"}}`, gscerr.CodeGoogleAPIError, true},
		{"503 html", 503, `<html>Service Unavailable</html>`, gscerr.CodeGoogleAPIError, true},
	}
	for _, tc := range cases {
		got := MapHTTPError(tc.status, []byte(tc.body))
		if got.Code != tc.code || got.Retryable != tc.retryable {
			t.Errorf("%s: got %s retryable=%v, want %s retryable=%v", tc.name, got.Code, got.Retryable, tc.code, tc.retryable)
		}
		if got.Message == "" || got.Action == "" {
			t.Errorf("%s: message and action required: %+v", tc.name, got)
		}
	}
}

func TestDoRetriesTransientThenSucceeds(t *testing.T) {
	calls := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"siteEntry":[]}`)
	})
	if _, err := c.ListSites(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Errorf("calls = %d", calls)
	}
}

func TestDoDoesNotRetryTerminalErrors(t *testing.T) {
	calls := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write(fixture(t, "error_403_permission.json"))
	})
	_, err := c.ListSites(context.Background())
	var ge *gscerr.Error
	if !errors.As(err, &ge) || ge.Code != gscerr.CodePropertyAccessDenied {
		t.Fatalf("got %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestDoGivesUpAfterMaxAttempts(t *testing.T) {
	calls := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := c.ListSites(context.Background())
	if gscerr.From(err).Code != gscerr.CodeGoogleAPIError || calls != maxAttempts {
		t.Errorf("err=%v calls=%d", err, calls)
	}
}

func TestDoPropagatesAuthErrorsFromTransport(t *testing.T) {
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, gscerr.New(gscerr.CodeAuthRevoked, "revoked", "login")
	})}
	c := New(hc)
	c.Sleep = func(time.Duration) {}
	_, err := c.ListSites(context.Background())
	if gscerr.From(err).Code != gscerr.CodeAuthRevoked {
		t.Errorf("got %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
