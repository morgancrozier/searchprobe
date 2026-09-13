package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/morgancrozier/searchprobe/internal/auth"
	"github.com/morgancrozier/searchprobe/internal/gscerr"
	"github.com/morgancrozier/searchprobe/internal/searchconsole"
)

type harness struct {
	deps   *deps
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	store  *auth.Store
	// requests records JSON request bodies received by the fake API.
	requests []map[string]any
	paths    []string
	// maxAutoPages overrides the client's pagination safety cap (0 = default).
	maxAutoPages int
}

// newHarness builds deps backed by a fake Search Console server. handler may be
// nil for commands that must fail before any network call.
func newHarness(t *testing.T, handler http.HandlerFunc) *harness {
	t.Helper()
	h := &harness{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	h.store = &auth.Store{Path: filepath.Join(t.TempDir(), "credentials.json")}

	var srv *httptest.Server
	if handler != nil {
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.paths = append(h.paths, r.Method+" "+r.URL.EscapedPath())
			if r.Body != nil {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				h.requests = append(h.requests, body)
			}
			handler(w, r)
		}))
		t.Cleanup(srv.Close)
	}

	h.deps = &deps{
		stdout: h.stdout,
		stderr: h.stderr,
		stdin:  strings.NewReader(""),
		// 2026-09-12 03:00 UTC == 2026-09-11 20:00 PDT, so "yesterday" PT is 2026-09-10.
		now:   func() time.Time { return time.Date(2026, 9, 12, 3, 0, 0, 0, time.UTC) },
		store: func() (*auth.Store, error) { return h.store, nil },
		client: func(ctx context.Context) (*searchconsole.Client, error) {
			if _, err := h.store.Load(); err != nil {
				return nil, err
			}
			if srv == nil {
				t.Fatal("command attempted a network call without a fake server")
			}
			c := searchconsole.New(srv.Client())
			c.BaseURL = srv.URL + "/webmasters/v3"
			c.InspectionBaseURL = srv.URL + "/v1"
			c.Sleep = func(time.Duration) {}
			c.MaxAutoPages = h.maxAutoPages
			return c, nil
		},
		login: func(ctx context.Context, opts auth.LoginOptions) (*auth.Credentials, error) {
			return fakeCreds(opts.Client), nil
		},
		revoke:      func(ctx context.Context, token string) error { return nil },
		openBrowser: func(string) error { return nil },
	}
	return h
}

func fakeCreds(client auth.ClientConfig) *auth.Credentials {
	if client.ClientID == "" {
		client = auth.ClientConfig{ClientID: "cid", AuthURI: "https://accounts.google.com/o/oauth2/v2/auth", TokenURI: "https://oauth2.googleapis.com/token"}
	}
	return &auth.Credentials{
		Client: client,
		Scopes: []string{auth.ScopeReadOnly},
		Token: auth.StoredToken{
			AccessToken: "access-fixture", TokenType: "Bearer", RefreshToken: "refresh-fixture",
			Expiry: time.Date(2026, 9, 12, 4, 0, 0, 0, time.UTC),
		},
	}
}

func (h *harness) signIn(t *testing.T) {
	t.Helper()
	if err := h.store.Save(fakeCreds(auth.ClientConfig{})); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) run(args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	return run(args, h.deps)
}

type envelope struct {
	OK       bool                       `json:"ok"`
	Data     map[string]any             `json:"data"`
	Meta     map[string]any             `json:"meta"`
	Warnings []map[string]string        `json:"warnings"`
	Error    map[string]any             `json:"error"`
	Raw      map[string]json.RawMessage `json:"-"`
}

func (h *harness) envelope(t *testing.T) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(h.stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not a JSON envelope: %v\n%s", err, h.stdout.String())
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &env.Raw); err != nil {
		t.Fatal(err)
	}
	return env
}

func warningCodes(env envelope) []string {
	out := make([]string, 0, len(env.Warnings))
	for _, w := range env.Warnings {
		out = append(out, w["code"])
	}
	return out
}

func TestHelp(t *testing.T) {
	h := newHarness(t, nil)
	if code := h.run("--help"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	for _, want := range []string{"auth", "sites", "performance", "compare", "inspect", "sitemaps", "sitemap ", "--json", "Exit codes"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("help missing %q", want)
		}
	}
}

func TestUnauthenticatedJSON(t *testing.T) {
	h := newHarness(t, nil)
	for _, args := range [][]string{
		{"sites", "--json"},
		{"performance", "--site", "sc-domain:example.com", "--json"},
		{"inspect", "https://example.com/", "--site", "sc-domain:example.com", "--json"},
		{"sitemaps", "--site", "sc-domain:example.com", "--json"},
		{"compare", "--site", "sc-domain:example.com", "--previous", "--json"},
		{"sitemap", "https://example.com/sitemap.xml", "--site", "sc-domain:example.com", "--json"},
		{"auth", "status", "--json"},
	} {
		code := h.run(args...)
		env := h.envelope(t)
		if code != gscerr.ExitAuthRequired || env.OK || env.Error["code"] != gscerr.CodeAuthRequired {
			t.Errorf("%v: exit=%d env=%+v", args, code, env)
		}
		if !strings.Contains(env.Error["action"].(string), "gsc auth login") {
			t.Errorf("%v: action must tell the user how to log in: %v", args, env.Error)
		}
		if h.stderr.Len() != 0 {
			t.Errorf("%v: JSON mode must keep stderr clean: %q", args, h.stderr.String())
		}
	}
}

func TestUnauthenticatedHuman(t *testing.T) {
	h := newHarness(t, nil)
	code := h.run("sites")
	if code != gscerr.ExitAuthRequired || h.stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q", code, h.stdout.String())
	}
	if !strings.Contains(h.stderr.String(), "Error: Google Search Console authentication is required.") ||
		!strings.Contains(h.stderr.String(), "Next: Run `gsc auth login`.") {
		t.Errorf("stderr = %q", h.stderr.String())
	}
}

func TestUsageErrorsAreStructured(t *testing.T) {
	h := newHarness(t, nil)
	h.signIn(t)
	cases := []struct {
		args []string
		code string
	}{
		{[]string{"performance", "--json"}, gscerr.CodeInvalidArgument},                                                    // missing --site
		{[]string{"performance", "--site", "sc-domain:example.com", "--days", "0", "--json"}, gscerr.CodeInvalidDateRange}, //
		{[]string{"performance", "--site", "sc-domain:example.com", "--dimensions", "bogus", "--json"}, gscerr.CodeInvalidArgument},
		{[]string{"performance", "--site", "sc-domain:example.com", "--limit", "99999", "--json"}, gscerr.CodeInvalidArgument},
		{[]string{"inspect", "--site", "sc-domain:example.com", "--json"}, gscerr.CodeInvalidArgument}, // missing url
		{[]string{"inspect", "https://other.com/x", "--site", "sc-domain:example.com", "--json"}, gscerr.CodeURLOutsideProperty},
		{[]string{"inspect", "example.com/x", "--site", "sc-domain:example.com", "--json"}, gscerr.CodeInvalidArgument},
		{[]string{"sites", "--bogus", "--json"}, gscerr.CodeInvalidArgument},
		{[]string{"nosuchcommand", "--json"}, gscerr.CodeInvalidArgument},
	}
	for _, tc := range cases {
		code := h.run(tc.args...)
		env := h.envelope(t)
		if code != gscerr.ExitUsage || env.OK || env.Error["code"] != tc.code {
			t.Errorf("%v: exit=%d env=%s", tc.args, code, h.stdout.String())
		}
		if env.Error["action"] == "" {
			t.Errorf("%v: action required", tc.args)
		}
	}
}

func TestSitesJSON(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"siteEntry":[{"siteUrl":"sc-domain:example.com","permissionLevel":"siteOwner"},{"siteUrl":"https://Shop.Example.org/","permissionLevel":"siteFullUser"},{"siteUrl":"sc-domain:other.net","permissionLevel":"siteOwner"}]}`)
	})
	h.signIn(t)
	if code := h.run("sites", "--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stdout.String())
	}
	env := h.envelope(t)
	if !env.OK || env.Meta["count"] != float64(3) || env.Meta["total"] != float64(3) || len(env.Warnings) != 0 {
		t.Errorf("env = %s", h.stdout.String())
	}
	if _, has := env.Meta["match"]; has {
		t.Errorf("meta.match must be absent without --match")
	}

	if code := h.run("sites", "--match", "EXAMPLE", "--json"); code != 0 {
		t.Fatal(h.stdout.String())
	}
	env = h.envelope(t)
	matched := env.Data["sites"].([]any)
	if env.Meta["count"] != float64(2) || env.Meta["total"] != float64(3) || env.Meta["match"] != "EXAMPLE" || len(matched) != 2 {
		t.Errorf("match env = %s", h.stdout.String())
	}
	if matched[0].(map[string]any)["siteUrl"] != "https://Shop.Example.org/" || matched[1].(map[string]any)["siteUrl"] != "sc-domain:example.com" {
		t.Errorf("matched = %v", matched)
	}
	if code := h.run("sites", "--match", "nomatch", "--json"); code != 0 || h.envelope(t).Meta["count"] != float64(0) {
		t.Errorf("no match: %d %s", code, h.stdout.String())
	}
	if code := h.run("sites", "--match", "other"); code != 0 || !strings.Contains(h.stdout.String(), "sc-domain:other.net") || strings.Contains(h.stdout.String(), "example.com") {
		t.Errorf("human match: %q", h.stdout.String())
	}
	if code := h.run("sites", "--match", "zzz"); code != 0 || !strings.Contains(h.stdout.String(), `No properties match "zzz" (3 accessible)`) {
		t.Errorf("human no match: %q", h.stdout.String())
	}
	if h.paths[0] != "GET /webmasters/v3/sites" {
		t.Errorf("path = %v", h.paths)
	}
}

func TestSitesHuman(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"siteEntry":[{"siteUrl":"sc-domain:example.com","permissionLevel":"siteOwner"}]}`)
	})
	h.signIn(t)
	if code := h.run("sites"); code != 0 {
		t.Fatal(h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "SITE") || !strings.Contains(h.stdout.String(), "sc-domain:example.com") {
		t.Errorf("stdout = %q", h.stdout.String())
	}
}

func TestPerformanceJSON(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"rows":[{"keys":["example search","MOBILE"],"clicks":42,"impressions":910,"ctr":0.0461,"position":6.3}],"responseAggregationType":"byProperty"}`)
	})
	h.signIn(t)
	code := h.run("performance", "--site", "sc-domain:example.com", "--days", "7", "--dimensions", "query,device", "--limit", "10", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, h.stdout.String())
	}
	env := h.envelope(t)
	if !env.OK {
		t.Fatalf("env = %s", h.stdout.String())
	}
	req := h.requests[0]
	if req["startDate"] != "2026-09-04" || req["endDate"] != "2026-09-10" || req["rowLimit"] != float64(10) || req["dataState"] != "final" {
		t.Errorf("request = %v", req)
	}
	if dims, _ := req["dimensions"].([]any); len(dims) != 2 || dims[0] != "query" || dims[1] != "device" {
		t.Errorf("dimensions = %v", req["dimensions"])
	}
	if !strings.Contains(h.paths[0], "/sites/sc-domain:example.com/searchAnalytics/query") {
		t.Errorf("path = %v", h.paths)
	}

	m := env.Meta
	checks := map[string]any{
		"site": "sc-domain:example.com", "dateMode": "days", "startDate": "2026-09-04", "endDate": "2026-09-10", "days": float64(7),
		"timezone": "America/Los_Angeles", "dataState": "final", "searchType": "web", "rowLimit": float64(10), "startRow": float64(0),
		"rowCount": float64(1), "pagesFetched": float64(1), "all": false, "filterLogic": "and",
		"paginationExhausted": true, "sourceMayBePartial": true, "aggregationType": "byProperty",
	}
	if f, ok := m["filters"].([]any); !ok || len(f) != 0 {
		t.Errorf("meta.filters must be an empty array: %v", m["filters"])
	}
	for _, absent := range []string{"requestedAggregationType", "firstIncompleteDate", "paginationStopReason"} {
		if _, has := m[absent]; has {
			t.Errorf("meta[%s] should be absent", absent)
		}
	}
	for k, want := range checks {
		if m[k] != want {
			t.Errorf("meta[%s] = %v, want %v", k, m[k], want)
		}
	}
	rows := env.Data["rows"].([]any)
	row := rows[0].(map[string]any)
	if row["query"] != "example search" || row["device"] != "MOBILE" || row["clicks"] != float64(42) || row["position"] != 6.3 {
		t.Errorf("row = %v", row)
	}
	// Flat row key order: dimensions first, then metrics.
	dataRaw := string(env.Raw["data"])
	rowsRaw := dataRaw[strings.Index(dataRaw, `"rows"`):]
	if strings.Index(dataRaw, `"rows"`) > strings.Index(dataRaw, `"returnedTotals"`) {
		t.Errorf("rows must precede returnedTotals: %s", dataRaw)
	}
	for _, pair := range [][2]string{{`"query"`, `"device"`}, {`"device"`, `"clicks"`}, {`"clicks"`, `"impressions"`}, {`"impressions"`, `"ctr"`}, {`"ctr"`, `"position"`}} {
		if strings.Index(rowsRaw, pair[0]) > strings.Index(rowsRaw, pair[1]) {
			t.Errorf("row key order: %s should precede %s in %s", pair[0], pair[1], rowsRaw)
		}
	}
	totals := env.Data["returnedTotals"].(map[string]any)
	if totals["clicks"] != float64(42) || totals["impressions"] != float64(910) || totals["rows"] != float64(1) {
		t.Errorf("returnedTotals = %v", totals)
	}
	codes := strings.Join(warningCodes(env), ",")
	if strings.Contains(codes, "TOP_ROWS_ONLY") || !strings.Contains(codes, "RECENT_DAYS_MAY_BE_EXCLUDED") || strings.Contains(codes, "ROW_LIMIT_REACHED") || strings.Contains(codes, "PRELIMINARY_DATA") {
		t.Errorf("warnings = %v", codes)
	}
	for _, absent := range []string{"firstObservedDate", "lastObservedDate"} {
		if _, has := m[absent]; has {
			t.Errorf("meta[%s] must be absent without the date dimension", absent)
		}
	}
}

// TestWarningPolicy pins the deterministic warning rules in performanceWarnings.
func TestWarningPolicy(t *testing.T) {
	now := time.Date(2026, 9, 12, 3, 0, 0, 0, time.UTC) // today PT = 2026-09-11
	dateRows := func(dates ...string) []searchconsole.Row {
		rows := make([]searchconsole.Row, 0, len(dates))
		for _, d := range dates {
			rows = append(rows, searchconsole.Row{Dimensions: []string{"date"}, Keys: map[string]string{"date": d}})
		}
		return rows
	}
	cases := []struct {
		name string
		res  searchconsole.PerformanceResult
		all  bool
		want []string
	}{
		{"28 days final, no date dim", searchconsole.PerformanceResult{StartDate: "2026-08-14", EndDate: "2026-09-10", DataState: "final", PaginationExhausted: true}, false, nil},
		{"7 days final, no date dim, recent", searchconsole.PerformanceResult{StartDate: "2026-09-04", EndDate: "2026-09-10", DataState: "final", PaginationExhausted: true}, false, []string{"RECENT_DAYS_MAY_BE_EXCLUDED"}},
		{"7 days final, old window", searchconsole.PerformanceResult{StartDate: "2026-07-01", EndDate: "2026-07-07", DataState: "final", PaginationExhausted: true}, false, nil},
		// Observed date rows never drive warnings: absence can mean no activity or top-rows truncation.
		{"28 days, date dim, rows end early", searchconsole.PerformanceResult{StartDate: "2026-08-14", EndDate: "2026-09-10", DataState: "final", PaginationExhausted: true, Rows: dateRows("2026-08-14", "2026-09-05")}, false, nil},
		{"7 days, date dim, rows end early (short-window rule only)", searchconsole.PerformanceResult{StartDate: "2026-09-04", EndDate: "2026-09-10", DataState: "final", PaginationExhausted: true, Rows: dateRows("2026-09-04", "2026-09-09")}, false, []string{"RECENT_DAYS_MAY_BE_EXCLUDED"}},
		{"90 days, rows start late", searchconsole.PerformanceResult{StartDate: "2026-06-14", EndDate: "2026-09-11", DataState: "final", PaginationExhausted: true, Rows: dateRows("2026-08-21", "2026-09-09")}, false, nil},
		{"final with Google incomplete date", searchconsole.PerformanceResult{StartDate: "2026-08-14", EndDate: "2026-09-10", DataState: "final", PaginationExhausted: true, FirstIncompleteDate: "2026-09-09"}, false, []string{"INCOMPLETE_DATA"}},
		{"all state", searchconsole.PerformanceResult{StartDate: "2026-09-04", EndDate: "2026-09-10", DataState: "all", PaginationExhausted: true}, false, []string{"PRELIMINARY_DATA"}},
		{"all state incomplete", searchconsole.PerformanceResult{StartDate: "2026-09-04", EndDate: "2026-09-10", DataState: "all", PaginationExhausted: true, FirstIncompleteDate: "2026-09-09"}, false, []string{"PRELIMINARY_DATA", "INCOMPLETE_DATA"}},
		{"--all exhausted", searchconsole.PerformanceResult{StartDate: "2026-08-14", EndDate: "2026-09-10", DataState: "final", PaginationExhausted: true}, true, []string{"TOP_ROWS_ONLY"}},
		{"limit reached", searchconsole.PerformanceResult{StartDate: "2026-08-14", EndDate: "2026-09-10", DataState: "final", RowLimit: 10}, false, []string{"ROW_LIMIT_REACHED"}},
		{"--all safety cap", searchconsole.PerformanceResult{StartDate: "2026-08-14", EndDate: "2026-09-10", DataState: "final", PaginationStopReason: "safety_cap"}, true, []string{"TOP_ROWS_ONLY", "PAGINATION_STOPPED"}},
	}
	for _, tc := range cases {
		got := performanceWarnings(&tc.res, tc.all, now)
		codes := make([]string, 0, len(got))
		for _, w := range got {
			codes = append(codes, w.Code)
		}
		if strings.Join(codes, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: got %v want %v", tc.name, codes, tc.want)
		}
	}
}

func TestPerformanceObservedDateRangeMeta(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"rows":[{"keys":["2026-08-21"],"clicks":1,"impressions":1,"ctr":1,"position":1},{"keys":["2026-09-09"],"clicks":1,"impressions":1,"ctr":1,"position":1}]}`)
	})
	h.signIn(t)
	if code := h.run("performance", "--site", "sc-domain:example.com", "--days", "90", "--dimensions", "date", "--json"); code != 0 {
		t.Fatal(h.stdout.String())
	}
	env := h.envelope(t)
	m := env.Meta
	if m["startDate"] != "2026-06-13" || m["endDate"] != "2026-09-10" || m["firstObservedDate"] != "2026-08-21" || m["lastObservedDate"] != "2026-09-09" {
		t.Errorf("meta = %v", m)
	}
	if len(env.Warnings) != 0 {
		t.Errorf("observed dates must not produce warnings: %v", env.Warnings)
	}
}

func TestPerformanceExplicitDatesAndOptions(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"rows":[],"responseAggregationType":"byPage","metadata":{"first_incomplete_date":"2026-09-10"}}`)
	})
	h.signIn(t)
	code := h.run("performance", "--site", "sc-domain:example.com", "--start", "2026-08-01", "--end", "2026-08-31",
		"--dimensions", "page,date", "--type", "image", "--data-state", "all", "--aggregation", "byPage",
		"--filter", "page contains /blog/", "--filter", "query notContains brand name", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, h.stdout.String())
	}
	env := h.envelope(t)
	req := h.requests[0]
	if req["startDate"] != "2026-08-01" || req["endDate"] != "2026-08-31" || req["type"] != "image" || req["dataState"] != "all" || req["aggregationType"] != "byPage" {
		t.Errorf("request = %v", req)
	}
	groups := req["dimensionFilterGroups"].([]any)
	g := groups[0].(map[string]any)
	filters := g["filters"].([]any)
	if len(groups) != 1 || g["groupType"] != "and" || len(filters) != 2 {
		t.Errorf("filter groups = %v", groups)
	}
	f1 := filters[1].(map[string]any)
	if f1["dimension"] != "query" || f1["operator"] != "notContains" || f1["expression"] != "brand name" {
		t.Errorf("filter[1] = %v", f1)
	}
	m := env.Meta
	if m["dateMode"] != "explicit" || m["startDate"] != "2026-08-01" || m["endDate"] != "2026-08-31" || m["searchType"] != "image" || m["dataState"] != "all" {
		t.Errorf("meta = %v", m)
	}
	if _, has := m["days"]; has {
		t.Errorf("days must be absent in explicit mode")
	}
	if m["requestedAggregationType"] != "byPage" || m["aggregationType"] != "byPage" || m["firstIncompleteDate"] != "2026-09-10" {
		t.Errorf("meta aggregation/incomplete = %v", m)
	}
	mf := m["filters"].([]any)
	if len(mf) != 2 || mf[0].(map[string]any)["expression"] != "/blog/" {
		t.Errorf("meta.filters = %v", mf)
	}
	codes := strings.Join(warningCodes(env), ",")
	if !strings.Contains(codes, "PRELIMINARY_DATA") || !strings.Contains(codes, "INCOMPLETE_DATA") || strings.Contains(codes, "RECENT_DAYS_MAY_BE_EXCLUDED") {
		t.Errorf("warnings = %s", codes)
	}
}

func TestPerformanceOldRangeHasNoRecentDaysWarning(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"rows":[]}`) })
	h.signIn(t)
	if code := h.run("performance", "--site", "sc-domain:example.com", "--start", "2026-07-01", "--end", "2026-07-31", "--json"); code != 0 {
		t.Fatal(h.stdout.String())
	}
	if codes := strings.Join(warningCodes(h.envelope(t)), ","); strings.Contains(codes, "RECENT_DAYS_MAY_BE_EXCLUDED") {
		t.Errorf("warnings = %s", codes)
	}
}

func TestPerformanceDateAndPaginationConflicts(t *testing.T) {
	h := newHarness(t, nil)
	h.signIn(t)
	cases := []struct {
		args []string
		code string
	}{
		{[]string{"--start", "2026-08-01"}, gscerr.CodeInvalidDateRange},
		{[]string{"--end", "2026-08-31"}, gscerr.CodeInvalidDateRange},
		{[]string{"--start", "2026-08-01", "--end", "2026-08-31", "--days", "7"}, gscerr.CodeInvalidDateRange},
		{[]string{"--start", "2026-08-31", "--end", "2026-08-01"}, gscerr.CodeInvalidDateRange},
		{[]string{"--start", "08/01/2026", "--end", "2026-08-31"}, gscerr.CodeInvalidDateRange},
		{[]string{"--all", "--limit", "5"}, gscerr.CodeInvalidArgument},
		{[]string{"--all", "--start-row", "5"}, gscerr.CodeInvalidArgument},
		{[]string{"--type", "shopping"}, gscerr.CodeInvalidArgument},
		{[]string{"--data-state", "fresh"}, gscerr.CodeInvalidArgument},
		{[]string{"--dimensions", "hour"}, gscerr.CodeInvalidDimensionCombination},
		{[]string{"--dimensions", "hour", "--data-state", "all"}, gscerr.CodeInvalidDimensionCombination},
		{[]string{"--aggregation", "bySite"}, gscerr.CodeInvalidArgument},
		{[]string{"--filter", "page contains"}, gscerr.CodeInvalidArgument},
		{[]string{"--filter", "date equals 2026-01-01"}, gscerr.CodeInvalidArgument},
	}
	for _, tc := range cases {
		args := append([]string{"performance", "--site", "sc-domain:example.com", "--json"}, tc.args...)
		code := h.run(args...)
		env := h.envelope(t)
		if code != gscerr.ExitUsage || env.Error["code"] != tc.code {
			t.Errorf("%v: exit=%d env=%s", tc.args, code, h.stdout.String())
		}
	}
}

func TestPerformanceHourWithHourlyAll(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"rows":[],"metadata":{"first_incomplete_hour":"2026-09-11T05:00:00-07:00"}}`)
	})
	h.signIn(t)
	if code := h.run("performance", "--site", "sc-domain:example.com", "--days", "2", "--dimensions", "hour", "--data-state", "hourly_all", "--json"); code != 0 {
		t.Fatal(h.stdout.String())
	}
	env := h.envelope(t)
	if h.requests[0]["dataState"] != "hourly_all" || env.Meta["firstIncompleteHour"] != "2026-09-11T05:00:00-07:00" {
		t.Errorf("req=%v meta=%v", h.requests[0], env.Meta)
	}
	if codes := strings.Join(warningCodes(env), ","); !strings.Contains(codes, "INCOMPLETE_DATA") || !strings.Contains(codes, "PRELIMINARY_DATA") {
		t.Errorf("warnings = %s", codes)
	}
}

func TestPerformanceAllJSON(t *testing.T) {
	page := 0
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		page++
		if page == 1 {
			var rows []string
			for i := 0; i < searchconsole.MaxRowLimit; i++ {
				rows = append(rows, `{"keys":["q`+strconv.Itoa(i)+`"],"clicks":1,"impressions":1,"ctr":1,"position":1}`)
			}
			_, _ = io.WriteString(w, `{"rows":[`+strings.Join(rows, ",")+`]}`)
			return
		}
		_, _ = io.WriteString(w, `{"rows":[{"keys":["last"],"clicks":1,"impressions":1,"ctr":1,"position":1}]}`)
	})
	h.signIn(t)
	if code := h.run("performance", "--site", "sc-domain:example.com", "--all", "--json"); code != 0 {
		t.Fatalf("exit %d: %.300s", code, h.stdout.String())
	}
	env := h.envelope(t)
	m := env.Meta
	if m["all"] != true || m["pagesFetched"] != float64(2) || m["rowCount"] != float64(searchconsole.MaxRowLimit+1) || m["paginationExhausted"] != true || m["sourceMayBePartial"] != true || m["rowLimit"] != float64(searchconsole.MaxRowLimit) {
		t.Errorf("meta = %v", m)
	}
	if h.requests[0]["rowLimit"] != float64(searchconsole.MaxRowLimit) || h.requests[1]["startRow"] != float64(searchconsole.MaxRowLimit) {
		t.Errorf("requests = %v", h.requests)
	}
	if codes := strings.Join(warningCodes(env), ","); strings.Contains(codes, "ROW_LIMIT_REACHED") || strings.Contains(codes, "PAGINATION_STOPPED") || !strings.Contains(codes, "TOP_ROWS_ONLY") {
		t.Errorf("warnings = %s", codes)
	}
}

func TestPerformanceAllSafetyCapJSON(t *testing.T) {
	var h *harness
	h = newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		// The harness has already recorded this request; derive the page from it.
		start := (len(h.requests) - 1) * searchconsole.MaxRowLimit
		var rows []string
		for i := 0; i < searchconsole.MaxRowLimit; i++ {
			rows = append(rows, `{"keys":["q`+strconv.Itoa(start+i)+`"],"clicks":1,"impressions":1,"ctr":1,"position":1}`)
		}
		_, _ = io.WriteString(w, `{"rows":[`+strings.Join(rows, ",")+`]}`)
	})
	h.maxAutoPages = 2
	h.signIn(t)
	if code := h.run("performance", "--site", "sc-domain:example.com", "--all", "--json"); code != 0 {
		t.Fatalf("exit %d: %.300s", code, h.stdout.String())
	}
	env := h.envelope(t)
	m := env.Meta
	if m["paginationExhausted"] != false || m["paginationStopReason"] != "safety_cap" || m["sourceMayBePartial"] != true {
		t.Errorf("safety cap must be unambiguous: exhausted=%v reason=%v partial=%v", m["paginationExhausted"], m["paginationStopReason"], m["sourceMayBePartial"])
	}
	if m["pagesFetched"] != float64(2) || m["rowCount"] != float64(2*searchconsole.MaxRowLimit) {
		t.Errorf("meta = %v", m)
	}
	if codes := strings.Join(warningCodes(env), ","); !strings.Contains(codes, "PAGINATION_STOPPED") {
		t.Errorf("warnings = %s", codes)
	}
}

func TestPerformanceAllErrorMidway(t *testing.T) {
	page := 0
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		page++
		if page == 1 {
			var rows []string
			for i := 0; i < searchconsole.MaxRowLimit; i++ {
				rows = append(rows, `{"keys":["q`+strconv.Itoa(i)+`"],"clicks":1,"impressions":1,"ctr":1,"position":1}`)
			}
			_, _ = io.WriteString(w, `{"rows":[`+strings.Join(rows, ",")+`]}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"code":403,"message":"Quota exceeded","errors":[{"reason":"quotaExceeded"}]}}`)
	})
	h.signIn(t)
	code := h.run("performance", "--site", "sc-domain:example.com", "--all", "--json")
	env := h.envelope(t)
	if code != gscerr.ExitFailure || env.Error["code"] != gscerr.CodeQuotaExceeded || !strings.Contains(env.Error["message"].(string), "page 2") {
		t.Errorf("exit=%d env=%s", code, h.stdout.String())
	}
}

func TestSitemapsCommands(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.EscapedPath(), "/sitemaps") {
			_, _ = io.WriteString(w, `{"sitemap":[{"path":"https://example.com/sitemap.xml","type":"sitemap","isPending":false,"isSitemapsIndex":false,"lastDownloaded":"2026-09-11T02:15:00.000Z","warnings":"1","errors":"0","contents":[{"type":"web","submitted":"120","indexed":"0"}]}]}`)
			return
		}
		if strings.Contains(r.URL.EscapedPath(), "missing") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":404,"message":"Sitemap not found."}}`)
			return
		}
		_, _ = io.WriteString(w, `{"path":"https://example.com/sitemap.xml","type":"sitemap","warnings":"1","errors":"0","contents":[{"type":"web","submitted":"120"}]}`)
	})
	h.signIn(t)

	if code := h.run("sitemaps", "--site", "sc-domain:example.com", "--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stdout.String())
	}
	env := h.envelope(t)
	list := env.Data["sitemaps"].([]any)
	s := list[0].(map[string]any)
	if env.Meta["count"] != float64(1) || s["path"] != "https://example.com/sitemap.xml" || s["submittedUrls"] != float64(120) || s["warnings"] != float64(1) {
		t.Errorf("env = %s", h.stdout.String())
	}
	if _, has := s["indexed"]; has {
		t.Errorf("deprecated indexed must not be exposed")
	}
	if len(env.Warnings) != 0 || env.Meta["readOnly"] != true || env.Meta["submittedCountsOnly"] != true {
		t.Errorf("warnings/meta = %v %v", env.Warnings, env.Meta)
	}
	if !strings.Contains(h.paths[0], "GET /webmasters/v3/sites/sc-domain:example.com/sitemaps") {
		t.Errorf("path = %v", h.paths)
	}

	if code := h.run("sitemaps", "--site", "sc-domain:example.com", "--index", "https://example.com/idx.xml", "--json"); code != 0 {
		t.Fatal(h.stdout.String())
	}
	if h.envelope(t).Meta["sitemapIndex"] != "https://example.com/idx.xml" {
		t.Errorf("meta.sitemapIndex missing")
	}

	if code := h.run("sitemaps", "--site", "sc-domain:example.com"); code != 0 || !strings.Contains(h.stdout.String(), "SITEMAP") || !strings.Contains(h.stdout.String(), "120") {
		t.Errorf("human list: %d %q", code, h.stdout.String())
	}

	if code := h.run("sitemap", "https://example.com/sitemap.xml", "--site", "sc-domain:example.com", "--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stdout.String())
	}
	env = h.envelope(t)
	if env.Data["path"] != "https://example.com/sitemap.xml" || env.Data["submittedUrls"] != float64(120) || env.Meta["sitemap"] != "https://example.com/sitemap.xml" {
		t.Errorf("detail env = %s", h.stdout.String())
	}
	if code := h.run("sitemap", "https://example.com/sitemap.xml", "--site", "sc-domain:example.com"); code != 0 || !strings.Contains(h.stdout.String(), "Submitted URLs") {
		t.Errorf("human detail: %d %q", code, h.stdout.String())
	}

	code := h.run("sitemap", "https://example.com/missing.xml", "--site", "sc-domain:example.com", "--json")
	env = h.envelope(t)
	if code != gscerr.ExitFailure || env.Error["code"] != gscerr.CodeSitemapNotFound {
		t.Errorf("missing: exit=%d env=%s", code, h.stdout.String())
	}

	if code := h.run("sitemap", "--site", "sc-domain:example.com", "--json"); code != gscerr.ExitUsage {
		t.Errorf("missing url arg: exit %d", code)
	}
	if code := h.run("sitemaps", "--json"); code != gscerr.ExitUsage {
		t.Errorf("missing site: exit %d", code)
	}
}

func TestPerformanceRowLimitReachedWarning(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"rows":[{"keys":["a"],"clicks":1,"impressions":1,"ctr":1,"position":1}]}`)
	})
	h.signIn(t)
	if code := h.run("performance", "--site", "sc-domain:example.com", "--limit", "1", "--json"); code != 0 {
		t.Fatal(h.stdout.String())
	}
	env := h.envelope(t)
	if env.Meta["paginationExhausted"] != false || !strings.Contains(strings.Join(warningCodes(env), ","), "ROW_LIMIT_REACHED") {
		t.Errorf("env = %s", h.stdout.String())
	}
}

func TestPerformanceHumanTable(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"rows":[{"keys":["example search"],"clicks":42,"impressions":910,"ctr":0.0461,"position":6.3}]}`)
	})
	h.signIn(t)
	if code := h.run("performance", "--site", "sc-domain:example.com"); code != 0 {
		t.Fatal(h.stderr.String())
	}
	out := h.stdout.String()
	if !strings.Contains(out, "QUERY") || !strings.Contains(out, "example search") || !strings.Contains(out, "4.6%") {
		t.Errorf("table = %q", out)
	}
	if strings.Contains(h.stderr.String(), "Warning:") || !strings.Contains(h.stderr.String(), "2026-09-10") {
		t.Errorf("stderr = %q", h.stderr.String())
	}
}

func TestPerformanceAPIErrorJSON(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"code":403,"message":"User does not have sufficient permission for site 'sc-domain:example.com'.","errors":[{"reason":"forbidden"}]}}`)
	})
	h.signIn(t)
	code := h.run("performance", "--site", "sc-domain:example.com", "--json")
	env := h.envelope(t)
	if code != gscerr.ExitFailure || env.Error["code"] != gscerr.CodePropertyAccessDenied {
		t.Errorf("exit=%d env=%s", code, h.stdout.String())
	}
	if !strings.Contains(env.Error["action"].(string), "gsc sites --json") {
		t.Errorf("action = %v", env.Error["action"])
	}
}

func TestInspectJSON(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"inspectionResult":{"inspectionResultLink":"https://search.google.com/x","indexStatusResult":{"verdict":"PASS","coverageState":"Submitted and indexed","googleCanonical":"https://example.com/page"}}}`)
	})
	h.signIn(t)
	code := h.run("inspect", "https://example.com/page", "--site", "sc-domain:example.com", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, h.stdout.String())
	}
	env := h.envelope(t)
	if h.paths[0] != "POST /v1/urlInspection/index:inspect" {
		t.Errorf("path = %v", h.paths)
	}
	if h.requests[0]["inspectionUrl"] != "https://example.com/page" || h.requests[0]["siteUrl"] != "sc-domain:example.com" {
		t.Errorf("request = %v", h.requests[0])
	}
	is := env.Data["indexStatus"].(map[string]any)
	if is["verdict"] != "PASS" || is["coverageState"] != "Submitted and indexed" {
		t.Errorf("indexStatus = %v", is)
	}
	if env.Meta["liveTest"] != false || env.Meta["inspectionType"] != "indexedVersion" {
		t.Errorf("meta = %v", env.Meta)
	}
	if len(env.Warnings) != 0 || env.Meta["inspectionUrl"] != "https://example.com/page" {
		t.Errorf("warnings/meta = %v %v", env.Warnings, env.Meta)
	}
}

func TestInspectBatch(t *testing.T) {
	var h *harness
	h = newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		body := h.requests[len(h.requests)-1]
		u, _ := body["inspectionUrl"].(string)
		switch {
		case strings.HasSuffix(u, "/quota"):
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":{"code":403,"message":"Quota exceeded","errors":[{"reason":"quotaExceeded"}]}}`)
		case strings.HasSuffix(u, "/bad"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"code":400,"message":"URL is not under the specified property"}}`)
		default:
			_, _ = io.WriteString(w, `{"inspectionResult":{"indexStatusResult":{"verdict":"PASS","coverageState":"Submitted and indexed"}}}`)
		}
	})
	h.signIn(t)
	list := filepath.Join(t.TempDir(), "urls.txt")
	if err := writeFile(list, "# comment\nhttps://example.com/a\n\nhttps://example.com/bad\nhttps://example.com/a\nhttps://other.com/x\nhttps://example.com/b\n"); err != nil {
		t.Fatal(err)
	}
	if code := h.run("inspect", "--site", "sc-domain:example.com", "--urls-file", list, "--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stdout.String())
	}
	env := h.envelope(t)
	results := env.Data["results"].([]any)
	if len(results) != 4 {
		t.Fatalf("results = %d", len(results))
	}
	r0 := results[0].(map[string]any)
	if r0["url"] != "https://example.com/a" || r0["ok"] != true || r0["inspection"].(map[string]any)["indexStatus"].(map[string]any)["verdict"] != "PASS" {
		t.Errorf("r0 = %v", r0)
	}
	r1 := results[1].(map[string]any)
	if r1["url"] != "https://example.com/bad" || r1["ok"] != false || r1["error"].(map[string]any)["code"] != gscerr.CodeURLOutsideProperty {
		t.Errorf("r1 = %v", r1)
	}
	r2 := results[2].(map[string]any)
	if r2["url"] != "https://other.com/x" || r2["ok"] != false || r2["error"].(map[string]any)["code"] != gscerr.CodeURLOutsideProperty {
		t.Errorf("r2 (local validation) = %v", r2)
	}
	if results[3].(map[string]any)["ok"] != true {
		t.Errorf("r3 = %v", results[3])
	}
	m := env.Meta
	if m["requested"] != float64(5) || m["duplicatesSkipped"] != float64(1) || m["inspected"] != float64(4) || m["succeeded"] != float64(2) || m["failed"] != float64(2) || m["liveTest"] != false {
		t.Errorf("meta = %v", m)
	}
	// Only 3 network calls: the outside-property URL never reached Google.
	if n := strings.Count(strings.Join(h.paths, "\n"), "index:inspect"); n != 3 {
		t.Errorf("network calls = %d", n)
	}

	// Quota failure aborts the batch with a structured error.
	list2 := filepath.Join(t.TempDir(), "urls2.txt")
	_ = writeFile(list2, "https://example.com/a\nhttps://example.com/quota\nhttps://example.com/c\n")
	code := h.run("inspect", "--site", "sc-domain:example.com", "--urls-file", list2, "--json")
	env = h.envelope(t)
	if code != gscerr.ExitFailure || env.Error["code"] != gscerr.CodeQuotaExceeded || !strings.Contains(env.Error["message"].(string), "URL 2 of 3") {
		t.Errorf("abort: exit=%d env=%s", code, h.stdout.String())
	}

	// stdin.
	h.deps.stdin = strings.NewReader("https://example.com/s1\nhttps://example.com/s2\n")
	if code := h.run("inspect", "--site", "sc-domain:example.com", "--urls-file", "-", "--json"); code != 0 {
		t.Fatalf("stdin exit %d: %s", code, h.stdout.String())
	}
	if h.envelope(t).Meta["inspected"] != float64(2) {
		t.Errorf("stdin meta = %v", h.envelope(t).Meta)
	}

	// Usage errors.
	if code := h.run("inspect", "https://example.com/a", "--site", "sc-domain:example.com", "--urls-file", list, "--json"); code != gscerr.ExitUsage {
		t.Errorf("both sources: exit %d", code)
	}
	if code := h.run("inspect", "--site", "sc-domain:example.com", "--json"); code != gscerr.ExitUsage {
		t.Errorf("no source: exit %d", code)
	}
	if code := h.run("inspect", "--site", "sc-domain:example.com", "--urls-file", filepath.Join(t.TempDir(), "missing.txt"), "--json"); code != gscerr.ExitUsage {
		t.Errorf("missing file: exit %d", code)
	}
	empty := filepath.Join(t.TempDir(), "empty.txt")
	_ = writeFile(empty, "\n# nothing\n")
	if code := h.run("inspect", "--site", "sc-domain:example.com", "--urls-file", empty, "--json"); code != gscerr.ExitUsage {
		t.Errorf("empty file: exit %d", code)
	}
}

func TestCompareCommand(t *testing.T) {
	var h *harness
	h = newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		body := h.requests[len(h.requests)-1]
		if body["startDate"] == "2026-09-04" {
			_, _ = io.WriteString(w, `{"rows":[{"keys":["/a"],"clicks":42,"impressions":1100,"ctr":0.038,"position":7.1},{"keys":["/new"],"clicks":3,"impressions":30,"ctr":0.1,"position":2}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"rows":[{"keys":["/a"],"clicks":30,"impressions":900,"ctr":0.033,"position":8.4}]}`)
	})
	h.signIn(t)
	code := h.run("compare", "--site", "sc-domain:example.com", "--days", "7", "--dimensions", "page", "--previous", "--filter", "page contains /", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, h.stdout.String())
	}
	env := h.envelope(t)
	if len(h.requests) != 2 || h.requests[0]["startDate"] != "2026-09-04" || h.requests[0]["endDate"] != "2026-09-10" || h.requests[1]["startDate"] != "2026-08-28" || h.requests[1]["endDate"] != "2026-09-03" {
		t.Errorf("requests = %v", h.requests)
	}
	for i, r := range h.requests {
		if r["rowLimit"] != float64(searchconsole.MaxRowLimit) || r["dimensionFilterGroups"] == nil {
			t.Errorf("request %d not fully paginated/filtered: %v", i, r)
		}
	}
	m := env.Meta
	cur := m["current"].(map[string]any)
	prev := m["previous"].(map[string]any)
	if m["previousMode"] != "previous" || m["dateMode"] != "days" || m["days"] != float64(7) || cur["startDate"] != "2026-09-04" || prev["endDate"] != "2026-09-03" || cur["days"] != float64(7) || prev["days"] != float64(7) {
		t.Errorf("meta windows = %v", m)
	}
	if m["joinedRows"] != float64(2) || m["rowCount"] != float64(2) || m["paginationExhausted"] != true || m["sourceMayBePartial"] != true || m["deltaSemantics"] == nil || m["sortedBy"] != searchconsole.SortOrder || m["filterLogic"] != "and" {
		t.Errorf("meta = %v", m)
	}
	if sm := m["sort"].(map[string]any); sm["key"] != "current-clicks" || sm["direction"] != "desc" {
		t.Errorf("meta.sort = %v", sm)
	}
	rows := env.Data["rows"].([]any)
	r0 := rows[0].(map[string]any)
	d := r0["delta"].(map[string]any)
	if r0["page"] != "/a" || d["clicks"] != float64(12) || d["clicksPct"] != float64(40) || d["position"] != -1.3 {
		t.Errorf("row0 = %v", r0)
	}
	r1 := rows[1].(map[string]any)
	if r1["previous"] != nil || r1["delta"].(map[string]any)["clicksPct"] != nil {
		t.Errorf("row1 = %v", r1)
	}
	totals := env.Data["returnedTotals"].(map[string]any)
	if totals["delta"].(map[string]any)["clicks"] != float64(15) {
		t.Errorf("totals = %v", totals)
	}
	codes := strings.Join(warningCodes(env), ",")
	if !strings.Contains(codes, "TOP_ROWS_ONLY") || !strings.Contains(codes, "RECENT_DAYS_MAY_BE_EXCLUDED") || strings.Contains(codes, "UNEQUAL_WINDOWS") {
		t.Errorf("warnings = %s", codes)
	}

	// Explicit windows, limit, and human output.
	if code := h.run("compare", "--site", "sc-domain:example.com", "--start", "2026-09-04", "--end", "2026-09-10", "--compare-start", "2026-08-28", "--compare-end", "2026-09-03", "--dimensions", "page", "--limit", "1", "--json"); code != 0 {
		t.Fatal(h.stdout.String())
	}
	env = h.envelope(t)
	if env.Meta["previousMode"] != "explicit" || env.Meta["rowCount"] != float64(1) || env.Meta["joinedRows"] != float64(2) || env.Meta["limit"] != float64(1) {
		t.Errorf("explicit meta = %v", env.Meta)
	}
	if _, has := env.Meta["days"]; has {
		t.Errorf("days must be absent in explicit mode")
	}
	if code := h.run("compare", "--site", "sc-domain:example.com", "--days", "7", "--dimensions", "page", "--previous"); code != 0 || !strings.Contains(h.stdout.String(), "+40.0%") || !strings.Contains(h.stdout.String(), "Totals:") {
		t.Errorf("human: %d %q", code, h.stdout.String())
	}

	// --sort impressions-delta --asc lists the smallest impression delta first: /new (+30) before /a (+200).
	if code := h.run("compare", "--site", "sc-domain:example.com", "--days", "7", "--dimensions", "page", "--previous", "--sort", "impressions-delta", "--asc", "--json"); code != 0 {
		t.Fatal(h.stdout.String())
	}
	env = h.envelope(t)
	rows = env.Data["rows"].([]any)
	if rows[0].(map[string]any)["page"] != "/new" || rows[1].(map[string]any)["page"] != "/a" {
		t.Errorf("sorted rows = %v", rows)
	}
	if sm := env.Meta["sort"].(map[string]any); sm["key"] != "impressions-delta" || sm["direction"] != "asc" || !strings.HasPrefix(env.Meta["sortedBy"].(string), "impressions-delta asc, then ") {
		t.Errorf("sort meta = %v %v", sm, env.Meta["sortedBy"])
	}
	if code := h.run("compare", "--site", "sc-domain:example.com", "--days", "7", "--dimensions", "page", "--previous", "--sort", "ctr", "--json"); code != gscerr.ExitUsage || h.envelope(t).Error["code"] != gscerr.CodeInvalidArgument {
		t.Errorf("bad sort key: exit %d %s", code, h.stdout.String())
	}

	// Unequal explicit windows warn.
	if code := h.run("compare", "--site", "sc-domain:example.com", "--start", "2026-09-04", "--end", "2026-09-10", "--compare-start", "2026-08-30", "--compare-end", "2026-09-03", "--dimensions", "page", "--json"); code != 0 {
		t.Fatal(h.stdout.String())
	}
	if codes := strings.Join(warningCodes(h.envelope(t)), ","); !strings.Contains(codes, "UNEQUAL_WINDOWS") {
		t.Errorf("unequal warnings = %s", codes)
	}
}

func TestCompareCommandErrors(t *testing.T) {
	h := newHarness(t, nil)
	h.signIn(t)
	cases := []struct {
		args []string
		code string
	}{
		{[]string{"--days", "7"}, gscerr.CodeInvalidDateRange}, // no comparison window
		{[]string{"--days", "7", "--previous", "--compare-start", "2026-08-01", "--compare-end", "2026-08-07"}, gscerr.CodeInvalidDateRange},
		{[]string{"--days", "7", "--compare-start", "2026-08-01"}, gscerr.CodeInvalidDateRange},
		{[]string{"--start", "2026-09-04", "--end", "2026-09-10", "--compare-start", "2026-09-01", "--compare-end", "2026-09-05"}, gscerr.CodeInvalidDateRange}, // overlap
		{[]string{"--days", "7", "--previous", "--limit", "-1"}, gscerr.CodeInvalidArgument},
		{[]string{"--days", "7", "--previous", "--dimensions", "hour"}, gscerr.CodeInvalidDimensionCombination},
		{[]string{"--days", "7", "--previous", "--filter", "bogus"}, gscerr.CodeInvalidArgument},
	}
	for _, tc := range cases {
		args := append([]string{"compare", "--site", "sc-domain:example.com", "--json"}, tc.args...)
		code := h.run(args...)
		env := h.envelope(t)
		if code != gscerr.ExitUsage || env.Error["code"] != tc.code {
			t.Errorf("%v: exit=%d env=%s", tc.args, code, h.stdout.String())
		}
	}
}

func TestCompareCommandIncompleteWindowFails(t *testing.T) {
	page := 0
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		page++
		var rows []string
		for i := 0; i < searchconsole.MaxRowLimit; i++ {
			rows = append(rows, `{"keys":["p`+strconv.Itoa(page)+`-`+strconv.Itoa(i)+`"],"clicks":1,"impressions":1,"ctr":1,"position":1}`)
		}
		_, _ = io.WriteString(w, `{"rows":[`+strings.Join(rows, ",")+`]}`)
	})
	h.maxAutoPages = 2
	h.signIn(t)
	code := h.run("compare", "--site", "sc-domain:example.com", "--days", "7", "--dimensions", "page", "--previous", "--json")
	env := h.envelope(t)
	if code != gscerr.ExitFailure || env.Error["code"] != gscerr.CodePaginationIncomplete {
		t.Errorf("exit=%d env=%.400s", code, h.stdout.String())
	}
}

func TestInspectHuman(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"inspectionResult":{"indexStatusResult":{"verdict":"NEUTRAL","coverageState":"URL is unknown to Google"}}}`)
	})
	h.signIn(t)
	if code := h.run("inspect", "https://example.com/page", "--site", "https://example.com/"); code != 0 {
		t.Fatal(h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "NEUTRAL") || !strings.Contains(h.stdout.String(), "URL is unknown to Google") {
		t.Errorf("stdout = %q", h.stdout.String())
	}
}

func TestAuthLifecycle(t *testing.T) {
	h := newHarness(t, nil)
	clientFile := filepath.Join(t.TempDir(), "client.json")
	if err := writeFile(clientFile, `{"installed":{"client_id":"cid.apps.googleusercontent.com","client_secret":"fixture","project_id":"demo"}}`); err != nil {
		t.Fatal(err)
	}

	if code := h.run("auth", "login", "--json"); code != 3 {
		t.Fatal("fresh login must require BYO")
	}
	if code := h.run("auth", "login", "--client-file", clientFile, "--json"); code != 0 {
		t.Fatalf("login exit %d: %s %s", code, h.stdout.String(), h.stderr.String())
	}
	env := h.envelope(t)
	if env.Data["authenticated"] != true || env.Data["clientId"] != "cid.apps.googleusercontent.com" {
		t.Errorf("login env = %s", h.stdout.String())
	}
	assertNoSecrets(t, h.stdout.String()+h.stderr.String())

	if code := h.run("auth", "status", "--json"); code != 0 {
		t.Fatalf("status exit %d: %s", code, h.stdout.String())
	}
	env = h.envelope(t)
	if env.Data["authenticated"] != true || env.Data["readOnly"] != true || env.Data["hasRefreshToken"] != true || env.Data["accessTokenValid"] != true {
		t.Errorf("status env = %s", h.stdout.String())
	}
	for _, key := range []string{"credentialsPath", "clientId", "projectId", "scopes", "readOnly", "hasRefreshToken", "accessTokenValid", "accessTokenExpiresAt", "createdAt", "updatedAt"} {
		if _, ok := env.Data[key]; !ok {
			t.Errorf("status JSON missing %q: %s", key, h.stdout.String())
		}
	}
	assertNoSecrets(t, h.stdout.String())

	if code := h.run("auth", "status"); code != 0 || !strings.Contains(h.stdout.String(), "OAuth:       Custom Google client") || strings.Contains(h.stdout.String(), "cid.apps.googleusercontent.com") {
		t.Errorf("human status: %d %q", code, h.stdout.String())
	}
	assertNoSecrets(t, h.stdout.String())

	if code := h.run("auth", "logout", "--json"); code != 0 {
		t.Fatalf("logout exit %d: %s", code, h.stdout.String())
	}
	env = h.envelope(t)
	if env.Data["revoked"] != true || env.Data["removedCredentials"] != true || env.Data["authenticated"] != false {
		t.Errorf("logout env = %s", h.stdout.String())
	}

	if code := h.run("auth", "status", "--json"); code != gscerr.ExitAuthRequired {
		t.Errorf("status after logout: exit %d", code)
	}
	// Logging out twice is not an error.
	if code := h.run("auth", "logout", "--json"); code != 0 {
		t.Errorf("second logout exit %d", code)
	}
	env = h.envelope(t)
	if env.Data["removedCredentials"] != false {
		t.Errorf("second logout env = %s", h.stdout.String())
	}
}

func TestAuthLogoutHumanWording(t *testing.T) {
	h := newHarness(t, nil)
	h.signIn(t)
	if code := h.run("auth", "logout"); code != 0 {
		t.Fatalf("logout exit %d: %s", code, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "Signed out of SearchProbe.") || !strings.Contains(h.stdout.String(), "Google access was revoked and local credentials were removed.") {
		t.Errorf("logout output = %q", h.stdout.String())
	}

	h.signIn(t)
	h.deps.revoke = func(ctx context.Context, token string) error { return io.ErrUnexpectedEOF }
	if code := h.run("auth", "logout"); code != 0 {
		t.Fatalf("logout with failed revoke exit %d: %s", code, h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "Signed out of SearchProbe.") || !strings.Contains(h.stdout.String(), "Google access could not be confirmed as revoked.") {
		t.Errorf("logout with failed revoke output = %q", h.stdout.String())
	}
	if !strings.Contains(h.stderr.String(), "Could not revoke the Google grant") {
		t.Errorf("logout warning = %q", h.stderr.String())
	}
}

func TestLogoutRevokeFailureIsWarning(t *testing.T) {
	h := newHarness(t, nil)
	h.signIn(t)
	h.deps.revoke = func(ctx context.Context, token string) error { return io.ErrUnexpectedEOF }
	if code := h.run("auth", "logout", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	env := h.envelope(t)
	if env.Data["revoked"] != false || env.Data["removedCredentials"] != true || strings.Join(warningCodes(env), ",") != "REVOKE_FAILED" {
		t.Errorf("env = %s", h.stdout.String())
	}
}

func assertNoSecrets(t *testing.T, out string) {
	t.Helper()
	for _, s := range []string{"access-fixture", "refresh-fixture", "\"fixture\""} {
		if strings.Contains(out, s) {
			t.Errorf("output leaked secret %q: %s", s, out)
		}
	}
}

func writeFile(path, content string) error {
	return writeFileMode(path, content)
}

func TestMalformedBYOOverridesDefault(t *testing.T) {
	h := newHarness(t, nil)
	p := filepath.Join(t.TempDir(), "client.json")
	if err := writeFile(p, "{access-fixture refresh-fixture"); err != nil {
		t.Fatal(err)
	}
	if code := h.run("auth", "login", "--client-file", p, "--json"); code == 0 {
		t.Fatal("malformed BYO accepted")
	}
	e := h.envelope(t)
	if e.Error["code"] != gscerr.CodeConfigError || e.Error["message"] != "The OAuth client file is not valid JSON." {
		t.Fatal("wrong malformed BYO error")
	}
	assertNoSecrets(t, h.stdout.String()+h.stderr.String())
	if code := h.run("auth", "login", "--client-file", "", "--json"); code == 0 {
		t.Fatal("empty explicit override fell back")
	}
}

func TestFailedLoginPreservesGrant(t *testing.T) {
	h := newHarness(t, nil)
	h.signIn(t)
	before, err := h.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	h.deps.login = func(context.Context, auth.LoginOptions) (*auth.Credentials, error) {
		return nil, gscerr.New(gscerr.CodeAuthFailed, "Sign-in failed.", "")
	}
	if h.run("auth", "login", "--json") == 0 {
		t.Fatal("expected failure")
	}
	after, err := h.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if before.Client != after.Client || before.Token != after.Token {
		t.Fatal("failed login replaced grant")
	}
	assertNoSecrets(t, h.stdout.String()+h.stderr.String())
}

func TestAuthLoginHelpFirstRun(t *testing.T) {
	h := newHarness(t, nil)
	if code := h.run("auth", "login", "--help"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	help := h.stdout.String()
	for _, want := range []string{"Google Cloud Desktop OAuth client", "saved client", "gsc setup", "--client-file", "PKCE (S256)", "127.0.0.1", "offline access"} {
		if !strings.Contains(help, want) {
			t.Errorf("help missing %q", want)
		}
	}
	for _, stale := range []string{"Testing", "test users", "verification is not complete"} {
		if strings.Contains(help, stale) {
			t.Errorf("help contains stale wording %q", stale)
		}
	}
}
