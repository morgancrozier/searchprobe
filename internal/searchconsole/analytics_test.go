package searchconsole

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

func code(err error) string {
	if err == nil {
		return ""
	}
	return gscerr.From(err).Code
}

func TestParseSearchType(t *testing.T) {
	for in, want := range map[string]string{"": "", "web": "web", "IMAGE": "image", "googlenews": "googleNews", "GoogleNews": "googleNews", "discover": "discover", "news": "news", "video": "video"} {
		got, err := ParseSearchType(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	if _, err := ParseSearchType("shopping"); code(err) != gscerr.CodeInvalidArgument {
		t.Errorf("bad type: %v", err)
	}
}

func TestParseDataState(t *testing.T) {
	for in, want := range map[string]string{"": "", "final": "final", "ALL": "all", "hourly_all": "hourly_all", "Hourly_All": "hourly_all"} {
		got, err := ParseDataState(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	if _, err := ParseDataState("fresh"); code(err) != gscerr.CodeInvalidArgument {
		t.Errorf("bad state: %v", err)
	}
}

func TestParseAggregationType(t *testing.T) {
	for in, want := range map[string]string{"": "", "auto": "auto", "bypage": "byPage", "byProperty": "byProperty", "byNewsShowcasePanel": "byNewsShowcasePanel"} {
		got, err := ParseAggregationType(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	if _, err := ParseAggregationType("bySite"); code(err) != gscerr.CodeInvalidArgument {
		t.Errorf("bad aggregation: %v", err)
	}
}

func TestParseFilter(t *testing.T) {
	cases := []struct {
		in   string
		want DimensionFilter
		code string
	}{
		{"page contains /blog/", DimensionFilter{"page", "contains", "/blog/"}, ""},
		{"query notContains brandname", DimensionFilter{"query", "notContains", "brandname"}, ""},
		{"QUERY NOTCONTAINS brand name with spaces  ", DimensionFilter{"query", "notContains", "brand name with spaces"}, ""},
		{"query includingRegex ^(buy|cheap) .*$", DimensionFilter{"query", "includingRegex", "^(buy|cheap) .*$"}, ""},
		{"query excludingregex \\bfoo\\b", DimensionFilter{"query", "excludingRegex", "\\bfoo\\b"}, ""},
		{"device equals MOBILE", DimensionFilter{"device", "equals", "MOBILE"}, ""},
		{"country notEquals usa", DimensionFilter{"country", "notEquals", "usa"}, ""},
		{"searchAppearance equals AMP_BLUE_LINK", DimensionFilter{"searchAppearance", "equals", "AMP_BLUE_LINK"}, ""},
		{"page contains", DimensionFilter{}, gscerr.CodeInvalidArgument},
		{"page", DimensionFilter{}, gscerr.CodeInvalidArgument},
		{"", DimensionFilter{}, gscerr.CodeInvalidArgument},
		{"date equals 2026-01-01", DimensionFilter{}, gscerr.CodeInvalidArgument},
		{"hour equals x", DimensionFilter{}, gscerr.CodeInvalidArgument},
		{"page matches /x/", DimensionFilter{}, gscerr.CodeInvalidArgument},
		{"page = /x/", DimensionFilter{}, gscerr.CodeInvalidArgument},
	}
	for _, tc := range cases {
		got, err := ParseFilter(tc.in)
		if code(err) != tc.code {
			t.Errorf("%q: err %v, want code %q", tc.in, err, tc.code)
			continue
		}
		if err == nil && got != tc.want {
			t.Errorf("%q: got %+v want %+v", tc.in, got, tc.want)
		}
		if err != nil && gscerr.From(err).Action == "" {
			t.Errorf("%q: action required", tc.in)
		}
	}
}

func TestValidateNewFields(t *testing.T) {
	base := PerformanceRequest{Site: "sc-domain:example.com", StartDate: "2026-09-01", EndDate: "2026-09-02"}
	cases := []struct {
		name string
		mut  func(*PerformanceRequest)
		code string
	}{
		{"hour without hourly_all", func(r *PerformanceRequest) { r.Dimensions = []string{"hour"}; r.DataState = "final" }, gscerr.CodeInvalidDimensionCombination},
		{"hour with all", func(r *PerformanceRequest) { r.Dimensions = []string{"hour"}; r.DataState = "all" }, gscerr.CodeInvalidDimensionCombination},
		{"hour with hourly_all", func(r *PerformanceRequest) { r.Dimensions = []string{"hour"}; r.DataState = "hourly_all" }, ""},
		{"bad search type", func(r *PerformanceRequest) { r.SearchType = "shopping" }, gscerr.CodeInvalidArgument},
		{"non-canonical search type", func(r *PerformanceRequest) { r.SearchType = "GoogleNews" }, gscerr.CodeInvalidArgument},
		{"bad data state", func(r *PerformanceRequest) { r.DataState = "fresh" }, gscerr.CodeInvalidArgument},
		{"bad aggregation", func(r *PerformanceRequest) { r.AggregationType = "bySite" }, gscerr.CodeInvalidArgument},
		{"filter on date", func(r *PerformanceRequest) { r.Filters = []DimensionFilter{{"date", "equals", "x"}} }, gscerr.CodeInvalidArgument},
		{"filter bad operator", func(r *PerformanceRequest) { r.Filters = []DimensionFilter{{"page", "matches", "x"}} }, gscerr.CodeInvalidArgument},
		{"filter empty expression", func(r *PerformanceRequest) { r.Filters = []DimensionFilter{{"page", "contains", " "}} }, gscerr.CodeInvalidArgument},
		{"good filter", func(r *PerformanceRequest) { r.Filters = []DimensionFilter{{"page", "contains", "/x/"}} }, ""},
		{"explicit dates", func(r *PerformanceRequest) { r.StartDate, r.EndDate = "2026-08-01", "2026-08-31" }, ""},
		{"bad explicit start", func(r *PerformanceRequest) { r.StartDate = "2026-8-1" }, gscerr.CodeInvalidDateRange},
		{"bad explicit end", func(r *PerformanceRequest) { r.EndDate = "31/08/2026" }, gscerr.CodeInvalidDateRange},
		{"reversed", func(r *PerformanceRequest) { r.StartDate, r.EndDate = "2026-09-02", "2026-09-01" }, gscerr.CodeInvalidDateRange},
	}
	for _, tc := range cases {
		r := base
		tc.mut(&r)
		if got := code(r.Validate()); got != tc.code {
			t.Errorf("%s: got %q want %q (%v)", tc.name, got, tc.code, r.Validate())
		}
	}
}

// TestRequestJSON checks the exact wire body sent to Google.
func TestRequestJSON(t *testing.T) {
	var body string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = io.WriteString(w, `{"rows":[]}`)
	})
	_, err := c.QueryPerformance(context.Background(), PerformanceRequest{
		Site: "sc-domain:example.com", StartDate: "2026-08-01", EndDate: "2026-08-31",
		Dimensions: []string{"query"}, SearchType: "image", DataState: "all", AggregationType: "byPage",
		Filters: []DimensionFilter{
			{Dimension: "page", Operator: "contains", Expression: "/blog/"},
			{Dimension: "query", Operator: "notContains", Expression: "brandname"},
		},
		RowLimit: 50, StartRow: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"startDate":"2026-08-01","endDate":"2026-08-31","dimensions":["query"],"type":"image","dimensionFilterGroups":[{"groupType":"and","filters":[{"dimension":"page","operator":"contains","expression":"/blog/"},{"dimension":"query","operator":"notContains","expression":"brandname"}]}],"aggregationType":"byPage","rowLimit":50,"startRow":100,"dataState":"all"}`
	if body != want {
		t.Errorf("request body:\n got %s\nwant %s", body, want)
	}
}

func TestRequestJSONDefaults(t *testing.T) {
	var body string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = io.WriteString(w, `{"rows":[]}`)
	})
	if _, err := c.QueryPerformance(context.Background(), PerformanceRequest{Site: "sc-domain:example.com", StartDate: "2026-08-01", EndDate: "2026-08-31"}); err != nil {
		t.Fatal(err)
	}
	want := `{"startDate":"2026-08-01","endDate":"2026-08-31","type":"web","rowLimit":1000,"dataState":"final"}`
	if body != want {
		t.Errorf("default body:\n got %s\nwant %s", body, want)
	}
}

func TestIncompleteMetadataPropagates(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"rows":[{"keys":["2026-09-11T00:00:00-07:00"],"clicks":1,"impressions":2,"ctr":0.5,"position":1}],"metadata":{"first_incomplete_date":"2026-09-10","first_incomplete_hour":"2026-09-11T05:00:00-07:00"}}`)
	})
	res, err := c.QueryPerformance(context.Background(), PerformanceRequest{Site: "sc-domain:example.com", StartDate: "2026-09-01", EndDate: "2026-09-11", Dimensions: []string{"hour"}, DataState: "hourly_all"})
	if err != nil {
		t.Fatal(err)
	}
	if res.FirstIncompleteDate != "2026-09-10" || res.FirstIncompleteHour != "2026-09-11T05:00:00-07:00" || res.DataState != "hourly_all" {
		t.Errorf("result = %+v", res)
	}
}

// pagedServer serves deterministic pages keyed by startRow.
func pagedServer(t *testing.T, totalRows int, failOnPage int) (http.HandlerFunc, *[]int) {
	t.Helper()
	var startRows []int
	return func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		start := 0
		if v, ok := req["startRow"].(float64); ok {
			start = int(v)
		}
		limit := int(req["rowLimit"].(float64))
		startRows = append(startRows, start)
		if failOnPage > 0 && start/limit+1 == failOnPage {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"code":429,"message":"Quota exceeded for quota metric 'Queries' and limit 'Queries per minute per user'","status":"RESOURCE_EXHAUSTED"}}`)
			return
		}
		var rows []string
		for i := start; i < start+limit && i < totalRows; i++ {
			rows = append(rows, fmt.Sprintf(`{"keys":["row-%06d"],"clicks":%d,"impressions":%d,"ctr":0.1,"position":1.5}`, i, totalRows-i, (totalRows-i)*10))
		}
		_, _ = io.WriteString(w, `{"rows":[`+strings.Join(rows, ",")+`],"responseAggregationType":"byProperty"}`)
	}, &startRows
}

func allReq() PerformanceRequest {
	return PerformanceRequest{Site: "sc-domain:example.com", StartDate: "2026-08-01", EndDate: "2026-08-31", Dimensions: []string{"query"}}
}

func assertNoDuplicates(t *testing.T, rows []Row) {
	t.Helper()
	seen := map[string]bool{}
	for _, r := range rows {
		k := r.Keys["query"]
		if seen[k] {
			t.Fatalf("duplicate row %q", k)
		}
		seen[k] = true
	}
}

func TestQueryPerformanceAllOnePage(t *testing.T) {
	h, startRows := pagedServer(t, 3, 0)
	c, _ := newTestClient(t, h)
	c.Sleep = func(d time.Duration) {}
	res, err := c.QueryPerformanceAll(context.Background(), allReq())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 3 || !res.PaginationExhausted || res.PagesFetched != 1 || res.PaginationStopReason != "" {
		t.Errorf("result: rows=%d exhausted=%v pages=%d reason=%q", len(res.Rows), res.PaginationExhausted, res.PagesFetched, res.PaginationStopReason)
	}
	if len(*startRows) != 1 || (*startRows)[0] != 0 {
		t.Errorf("startRows = %v", *startRows)
	}
	if res.RowLimit != MaxRowLimit || res.StartRow != 0 || res.ResponseAggregationType != "byProperty" {
		t.Errorf("result meta = %+v", res)
	}
}

func TestQueryPerformanceAllExactlyFullPageThenEmpty(t *testing.T) {
	h, startRows := pagedServer(t, MaxRowLimit, 0)
	c, _ := newTestClient(t, h)
	res, err := c.QueryPerformanceAll(context.Background(), allReq())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != MaxRowLimit || !res.PaginationExhausted || res.PagesFetched != 2 {
		t.Errorf("rows=%d exhausted=%v pages=%d", len(res.Rows), res.PaginationExhausted, res.PagesFetched)
	}
	if len(*startRows) != 2 || (*startRows)[1] != MaxRowLimit {
		t.Errorf("startRows = %v", *startRows)
	}
	assertNoDuplicates(t, res.Rows)
}

func TestQueryPerformanceAllMultiPagePreservesOrder(t *testing.T) {
	total := 2*MaxRowLimit + 7
	h, startRows := pagedServer(t, total, 0)
	c, _ := newTestClient(t, h)
	res, err := c.QueryPerformanceAll(context.Background(), allReq())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != total || !res.PaginationExhausted || res.PagesFetched != 3 {
		t.Errorf("rows=%d exhausted=%v pages=%d", len(res.Rows), res.PaginationExhausted, res.PagesFetched)
	}
	if got := fmt.Sprint(*startRows); got != fmt.Sprint([]int{0, MaxRowLimit, 2 * MaxRowLimit}) {
		t.Errorf("startRows = %s", got)
	}
	// Google ordering (clicks desc here) must be preserved page to page.
	for i := 1; i < len(res.Rows); i++ {
		if res.Rows[i].Clicks > res.Rows[i-1].Clicks {
			t.Fatalf("ordering broken at %d", i)
		}
	}
	if res.Rows[0].Keys["query"] != "row-000000" || res.Rows[total-1].Keys["query"] != fmt.Sprintf("row-%06d", total-1) {
		t.Errorf("first/last = %q/%q", res.Rows[0].Keys["query"], res.Rows[total-1].Keys["query"])
	}
	assertNoDuplicates(t, res.Rows)
}

func TestQueryPerformanceAllErrorOnLaterPage(t *testing.T) {
	h, _ := pagedServer(t, 3*MaxRowLimit, 2)
	c, _ := newTestClient(t, h)
	_, err := c.QueryPerformanceAll(context.Background(), allReq())
	ge := gscerr.From(err)
	if ge == nil || ge.Code != gscerr.CodeRateLimited || !ge.Retryable {
		t.Fatalf("want RATE_LIMITED retryable, got %v", err)
	}
	if !strings.Contains(ge.Message, "page 2") || !strings.Contains(ge.Message, fmt.Sprintf("%d rows", MaxRowLimit)) {
		t.Errorf("message lacks page context: %s", ge.Message)
	}
	if ge.Action == "" {
		t.Errorf("action required")
	}
}

func TestQueryPerformanceAllRepeatedPageStops(t *testing.T) {
	calls := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var rows []string
		for i := 0; i < MaxRowLimit; i++ {
			rows = append(rows, fmt.Sprintf(`{"keys":["same-%d"],"clicks":1,"impressions":1,"ctr":1,"position":1}`, i))
		}
		_, _ = io.WriteString(w, `{"rows":[`+strings.Join(rows, ",")+`]}`)
	})
	res, err := c.QueryPerformanceAll(context.Background(), allReq())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(res.Rows) != MaxRowLimit || res.PaginationExhausted || res.PaginationStopReason != StopRepeatedPage {
		t.Errorf("calls=%d rows=%d exhausted=%v reason=%q", calls, len(res.Rows), res.PaginationExhausted, res.PaginationStopReason)
	}
	assertNoDuplicates(t, res.Rows)
}

func TestQueryPerformanceAllSafetyCap(t *testing.T) {
	const cap = 3
	h, startRows := pagedServer(t, (cap+2)*MaxRowLimit, 0)
	c, _ := newTestClient(t, h)
	c.MaxAutoPages = cap
	res, err := c.QueryPerformanceAll(context.Background(), allReq())
	if err != nil {
		t.Fatal(err)
	}
	if res.PagesFetched != cap || len(*startRows) != cap || len(res.Rows) != cap*MaxRowLimit {
		t.Errorf("pages=%d requests=%d rows=%d", res.PagesFetched, len(*startRows), len(res.Rows))
	}
	// A safety cap must never look like exhaustion.
	if res.PaginationExhausted || res.PaginationStopReason != StopSafetyCap {
		t.Errorf("exhausted=%v reason=%q", res.PaginationExhausted, res.PaginationStopReason)
	}
	assertNoDuplicates(t, res.Rows)
}

func TestDefaultMaxAutoPagesIsBugGuard(t *testing.T) {
	c := New(nil)
	if c.maxAutoPages() != DefaultMaxAutoPages || DefaultMaxAutoPages*MaxRowLimit < 10_000_000 {
		t.Errorf("default guard %d pages (%d rows) is too low to be a bug guard", c.maxAutoPages(), c.maxAutoPages()*MaxRowLimit)
	}
}

func TestQueryPerformanceAllRejectsLimitAndStartRow(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("no request expected") })
	r := allReq()
	r.RowLimit = 10
	if _, err := c.QueryPerformanceAll(context.Background(), r); code(err) != gscerr.CodeInvalidArgument {
		t.Errorf("limit: %v", err)
	}
	r = allReq()
	r.StartRow = 5
	if _, err := c.QueryPerformanceAll(context.Background(), r); code(err) != gscerr.CodeInvalidArgument {
		t.Errorf("start row: %v", err)
	}
}

func TestListSitemaps(t *testing.T) {
	var gotPath, gotQuery string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.EscapedPath(), r.URL.RawQuery
		_, _ = w.Write(fixture(t, "sitemaps_list.json"))
	})
	sitemaps, err := c.ListSitemaps(context.Background(), "https://example.com/", "")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/webmasters/v3/sites/https:%2F%2Fexample.com%2F/sitemaps" || gotQuery != "" {
		t.Errorf("path=%s query=%s", gotPath, gotQuery)
	}
	if len(sitemaps) != 2 {
		t.Fatalf("len = %d", len(sitemaps))
	}
	// Sorted by path: news-sitemap before sitemap_index.
	s0, s1 := sitemaps[0], sitemaps[1]
	if s0.Path != "https://example.com/news-sitemap.xml" || !s0.IsPending || s0.Errors != 1 || s0.Warnings != 2 || s0.SubmittedURLs != 15 || s0.LastDownloaded != "" {
		t.Errorf("s0 = %+v", s0)
	}
	if s1.Path != "https://example.com/sitemap_index.xml" || !s1.IsSitemapsIndex || s1.SubmittedURLs != 1540 || len(s1.Contents) != 2 || s1.Contents[1].Type != "image" || s1.Contents[1].Submitted != 340 {
		t.Errorf("s1 = %+v", s1)
	}
	b, _ := json.Marshal(s1)
	if strings.Contains(string(b), "indexed") || !strings.Contains(string(b), `"submittedUrls":1540`) || !strings.Contains(string(b), `"warnings":0`) {
		t.Errorf("json = %s", b)
	}
	b, _ = json.Marshal(s0)
	if strings.Contains(string(b), `"lastDownloaded"`) {
		t.Errorf("absent lastDownloaded must be omitted: %s", b)
	}
}

func TestListSitemapsWithIndexAndEmpty(t *testing.T) {
	var gotQuery string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, `{}`)
	})
	sitemaps, err := c.ListSitemaps(context.Background(), "sc-domain:example.com", "https://example.com/sitemap_index.xml")
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "sitemapIndex=https%3A%2F%2Fexample.com%2Fsitemap_index.xml" {
		t.Errorf("query = %s", gotQuery)
	}
	if sitemaps == nil || len(sitemaps) != 0 {
		t.Errorf("want empty non-nil, got %#v", sitemaps)
	}
}

func TestListSitemapsNumericLongs(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"sitemap":[{"path":"https://example.com/s.xml","type":"sitemap","warnings":4,"errors":0,"contents":[{"type":"web","submitted":10}]}]}`)
	})
	sitemaps, err := c.ListSitemaps(context.Background(), "sc-domain:example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if sitemaps[0].Warnings != 4 || sitemaps[0].SubmittedURLs != 10 {
		t.Errorf("numeric longs: %+v", sitemaps[0])
	}
}

func TestGetSitemap(t *testing.T) {
	var gotPath string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write(fixture(t, "sitemap_get.json"))
	})
	s, err := c.GetSitemap(context.Background(), "sc-domain:example.com", "https://example.com/sitemap.xml")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/webmasters/v3/sites/sc-domain:example.com/sitemaps/https:%2F%2Fexample.com%2Fsitemap.xml" {
		t.Errorf("path = %s", gotPath)
	}
	if s.Path != "https://example.com/sitemap.xml" || s.Warnings != 3 || s.SubmittedURLs != 1200 || s.LastSubmitted == "" {
		t.Errorf("sitemap = %+v", s)
	}
}

func TestSitemapErrors(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":404,"message":"Sitemap not found."}}`)
	})
	_, err := c.GetSitemap(context.Background(), "sc-domain:example.com", "https://example.com/missing.xml")
	ge := gscerr.From(err)
	if ge.Code != gscerr.CodeSitemapNotFound || !strings.Contains(ge.Action, "gsc sitemaps --site sc-domain:example.com") {
		t.Errorf("404: %+v", ge)
	}

	c, _ = newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write(fixture(t, "error_403_permission.json"))
	})
	if _, err := c.ListSitemaps(context.Background(), "sc-domain:example.com", ""); code(err) != gscerr.CodePropertyAccessDenied {
		t.Errorf("403: %v", err)
	}
	if _, err := c.ListSitemaps(context.Background(), "", ""); code(err) != gscerr.CodeInvalidArgument {
		t.Errorf("empty site: %v", err)
	}
	if _, err := c.GetSitemap(context.Background(), "sc-domain:example.com", ""); code(err) != gscerr.CodeInvalidArgument {
		t.Errorf("empty sitemap: %v", err)
	}
}
