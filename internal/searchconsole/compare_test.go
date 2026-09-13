package searchconsole

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

func TestPreviousWindow(t *testing.T) {
	cases := []struct{ start, end, wantStart, wantEnd string }{
		{"2026-09-03", "2026-09-09", "2026-08-27", "2026-09-02"},
		{"2026-09-09", "2026-09-09", "2026-09-08", "2026-09-08"},
		{"2026-03-01", "2026-03-28", "2026-02-01", "2026-02-28"},
		{"2026-01-01", "2026-01-31", "2025-12-01", "2025-12-31"},
	}
	for _, tc := range cases {
		ps, pe, err := PreviousWindow(tc.start, tc.end)
		if err != nil || ps != tc.wantStart || pe != tc.wantEnd {
			t.Errorf("%s..%s: got %s..%s (%v), want %s..%s", tc.start, tc.end, ps, pe, err, tc.wantStart, tc.wantEnd)
		}
		d1, _ := WindowDays(tc.start, tc.end)
		d2, _ := WindowDays(ps, pe)
		if d1 != d2 {
			t.Errorf("%s..%s: unequal lengths %d vs %d", tc.start, tc.end, d1, d2)
		}
	}
	if _, _, err := PreviousWindow("2026-09-09", "2026-09-03"); gscerr.From(err).Code != gscerr.CodeInvalidDateRange {
		t.Errorf("reversed: %v", err)
	}
}

func TestObservedDateRange(t *testing.T) {
	rows := []Row{
		{Keys: map[string]string{"date": "2026-09-01"}},
		{Keys: map[string]string{"date": "2026-08-21"}},
		{Keys: map[string]string{"date": "2026-09-09"}},
	}
	first, last, ok := ObservedDateRange(rows)
	if !ok || first != "2026-08-21" || last != "2026-09-09" {
		t.Errorf("got %s %s %v", first, last, ok)
	}
	if _, _, ok := ObservedDateRange([]Row{{Keys: map[string]string{"query": "x"}}}); ok {
		t.Errorf("no date dimension must not be observed")
	}
	if _, _, ok := ObservedDateRange(nil); ok {
		t.Errorf("empty must not be observed")
	}
}

func TestCompareRequestValidate(t *testing.T) {
	base := PerformanceRequest{Site: "sc-domain:example.com", StartDate: "2026-09-03", EndDate: "2026-09-09"}
	cases := []struct {
		name string
		req  CompareRequest
		code string
	}{
		{"ok previous", CompareRequest{Base: base, PreviousStartDate: "2026-08-27", PreviousEndDate: "2026-09-02"}, ""},
		{"ok later window", CompareRequest{Base: base, PreviousStartDate: "2026-09-10", PreviousEndDate: "2026-09-16"}, ""},
		{"overlap", CompareRequest{Base: base, PreviousStartDate: "2026-09-01", PreviousEndDate: "2026-09-05"}, gscerr.CodeInvalidDateRange},
		{"identical", CompareRequest{Base: base, PreviousStartDate: "2026-09-03", PreviousEndDate: "2026-09-09"}, gscerr.CodeInvalidDateRange},
		{"touching end", CompareRequest{Base: base, PreviousStartDate: "2026-09-09", PreviousEndDate: "2026-09-15"}, gscerr.CodeInvalidDateRange},
		{"bad previous", CompareRequest{Base: base, PreviousStartDate: "2026-9-1", PreviousEndDate: "2026-09-02"}, gscerr.CodeInvalidDateRange},
		{"reversed previous", CompareRequest{Base: base, PreviousStartDate: "2026-09-02", PreviousEndDate: "2026-08-27"}, gscerr.CodeInvalidDateRange},
		{"row limit set", CompareRequest{Base: PerformanceRequest{Site: "s", StartDate: "2026-09-03", EndDate: "2026-09-09", RowLimit: 5}, PreviousStartDate: "2026-08-27", PreviousEndDate: "2026-09-02"}, gscerr.CodeInvalidArgument},
	}
	for _, tc := range cases {
		if got := code(tc.req.Validate()); got != tc.code {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.code)
		}
	}
}

// compareServer answers with fixed rows per window (keyed by startDate) and
// records request bodies.
func compareServer(t *testing.T, byStart map[string]string) (http.HandlerFunc, *[]map[string]any) {
	t.Helper()
	var reqs []map[string]any
	return func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		reqs = append(reqs, req)
		body, ok := byStart[req["startDate"].(string)]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"code":400,"message":"unexpected window"}}`)
			return
		}
		_, _ = io.WriteString(w, body)
	}, &reqs
}

func row(key string, clicks, impressions, ctr, position float64) string {
	return fmt.Sprintf(`{"keys":["%s"],"clicks":%v,"impressions":%v,"ctr":%v,"position":%v}`, key, clicks, impressions, ctr, position)
}

func TestComparePerformanceJoinAndDeltas(t *testing.T) {
	h, reqs := compareServer(t, map[string]string{
		"2026-09-03": `{"rows":[` + row("/both", 42, 1100, 0.038, 7.1) + `,` + row("/new", 5, 50, 0.1, 3) + `,` + row("/zero-prev", 4, 40, 0.1, 2) + `],"responseAggregationType":"byPage"}`,
		"2026-08-27": `{"rows":[` + row("/both", 30, 900, 0.033, 8.4) + `,` + row("/gone", 9, 90, 0.1, 5) + `,` + row("/zero-prev", 0, 0, 0, 1) + `]}`,
	})
	c, _ := newTestClient(t, h)
	res, err := c.ComparePerformance(context.Background(), CompareRequest{
		Base: PerformanceRequest{
			Site: "sc-domain:example.com", StartDate: "2026-09-03", EndDate: "2026-09-09",
			Dimensions: []string{"page"}, SearchType: "image", DataState: "all", AggregationType: "byPage",
			Filters: []DimensionFilter{{Dimension: "page", Operator: "contains", Expression: "/blog/"}},
		},
		PreviousStartDate: "2026-08-27", PreviousEndDate: "2026-09-02",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Both windows receive identical parameters except dates, fully paginated.
	if len(*reqs) != 2 {
		t.Fatalf("requests = %d", len(*reqs))
	}
	for i, r := range *reqs {
		if r["type"] != "image" || r["dataState"] != "all" || r["aggregationType"] != "byPage" || r["rowLimit"] != float64(MaxRowLimit) {
			t.Errorf("request %d = %v", i, r)
		}
		groups := r["dimensionFilterGroups"].([]any)
		if len(groups) != 1 || groups[0].(map[string]any)["filters"].([]any)[0].(map[string]any)["expression"] != "/blog/" {
			t.Errorf("request %d filters = %v", i, r["dimensionFilterGroups"])
		}
	}
	if (*reqs)[0]["startDate"] != "2026-09-03" || (*reqs)[0]["endDate"] != "2026-09-09" || (*reqs)[1]["startDate"] != "2026-08-27" || (*reqs)[1]["endDate"] != "2026-09-02" {
		t.Errorf("dates = %v / %v", (*reqs)[0], (*reqs)[1])
	}

	if res.Current.Days != 7 || res.Previous.Days != 7 || res.Current.RowCount != 3 || res.Previous.RowCount != 3 || res.Current.ResponseAggregationType != "byPage" {
		t.Errorf("windows = %+v / %+v", res.Current, res.Previous)
	}
	if len(res.Rows) != 4 {
		t.Fatalf("joined rows = %d", len(res.Rows))
	}
	byKey := map[string]CompareRow{}
	for _, r := range res.Rows {
		byKey[r.Keys["page"]] = r
	}

	both := byKey["/both"]
	if both.Current == nil || both.Previous == nil || both.Delta.Clicks != 12 || both.Delta.Impressions != 200 {
		t.Errorf("/both = %+v", both)
	}
	if both.Delta.CTR == nil || *both.Delta.CTR != 0.005 || both.Delta.Position == nil || *both.Delta.Position != -1.3 {
		t.Errorf("/both ctr/position delta = %v %v", both.Delta.CTR, both.Delta.Position)
	}
	if both.Delta.ClicksPct == nil || *both.Delta.ClicksPct != 40 || both.Delta.ImpressionsPct == nil || *both.Delta.ImpressionsPct != 22.22 {
		t.Errorf("/both pct = %v %v", both.Delta.ClicksPct, both.Delta.ImpressionsPct)
	}

	// Only in current: previous nil, deltas from zero, pct undefined.
	nw := byKey["/new"]
	if nw.Previous != nil || nw.Delta.Clicks != 5 || nw.Delta.Impressions != 50 || nw.Delta.ClicksPct != nil || nw.Delta.CTR != nil || nw.Delta.Position != nil {
		t.Errorf("/new = %+v", nw)
	}
	// Only in previous: current nil, negative deltas, pct -100.
	gone := byKey["/gone"]
	if gone.Current != nil || gone.Delta.Clicks != -9 || gone.Delta.Impressions != -90 || gone.Delta.ClicksPct == nil || *gone.Delta.ClicksPct != -100 || gone.Delta.Position != nil {
		t.Errorf("/gone = %+v", gone)
	}
	// Zero baseline present in both: absolute deltas, pct undefined, position delta defined.
	zp := byKey["/zero-prev"]
	if zp.Delta.Clicks != 4 || zp.Delta.ClicksPct != nil || zp.Delta.ImpressionsPct != nil || zp.Delta.Position == nil || *zp.Delta.Position != 1 {
		t.Errorf("/zero-prev = %+v", zp)
	}

	// Sorted by current clicks desc: /both 42, /new 5, /zero-prev 4, /gone (none).
	order := []string{res.Rows[0].Keys["page"], res.Rows[1].Keys["page"], res.Rows[2].Keys["page"], res.Rows[3].Keys["page"]}
	if strings.Join(order, ",") != "/both,/new,/zero-prev,/gone" {
		t.Errorf("order = %v", order)
	}

	// Totals.
	if res.CurrentTotals.Clicks != 51 || res.PrevTotals.Clicks != 39 || res.TotalsDelta.Clicks != 12 || res.TotalsDelta.ClicksPct == nil || *res.TotalsDelta.ClicksPct != 30.77 {
		t.Errorf("totals = %+v %+v %+v", res.CurrentTotals, res.PrevTotals, res.TotalsDelta)
	}

	// JSON shape: dimension first, then current/previous/delta with nulls where undefined.
	b, _ := json.Marshal(nw)
	want := `{"page":"/new","current":{"clicks":5,"impressions":50,"ctr":0.1,"position":3},"previous":null,"delta":{"clicks":5,"impressions":50,"ctr":null,"position":null,"clicksPct":null,"impressionsPct":null}}`
	if string(b) != want {
		t.Errorf("row json:\n got %s\nwant %s", b, want)
	}
	if strings.Contains(string(b), "Inf") || strings.Contains(string(b), "NaN") {
		t.Errorf("non-finite values in %s", b)
	}
}

func TestCompareTotalsOnlyAndObservedDates(t *testing.T) {
	h, _ := compareServer(t, map[string]string{
		"2026-09-03": `{"rows":[{"keys":[],"clicks":100,"impressions":1000,"ctr":0.1,"position":5}]}`,
		"2026-08-27": `{"rows":[{"keys":[],"clicks":80,"impressions":900,"ctr":0.09,"position":6}]}`,
	})
	c, _ := newTestClient(t, h)
	res, err := c.ComparePerformance(context.Background(), CompareRequest{
		Base:              PerformanceRequest{Site: "sc-domain:example.com", StartDate: "2026-09-03", EndDate: "2026-09-09"},
		PreviousStartDate: "2026-08-27", PreviousEndDate: "2026-09-02",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0].Delta.Clicks != 20 || *res.Rows[0].Delta.Position != -1 {
		t.Errorf("totals-only rows = %+v", res.Rows)
	}
	if res.Current.FirstObservedDate != "" {
		t.Errorf("no date dimension must leave observed range empty")
	}

	h2, _ := compareServer(t, map[string]string{
		"2026-09-03": `{"rows":[{"keys":["2026-09-05"],"clicks":1,"impressions":1,"ctr":1,"position":1},{"keys":["2026-09-07"],"clicks":1,"impressions":1,"ctr":1,"position":1}]}`,
		"2026-08-27": `{"rows":[]}`,
	})
	c2, _ := newTestClient(t, h2)
	res, err = c2.ComparePerformance(context.Background(), CompareRequest{
		Base:              PerformanceRequest{Site: "sc-domain:example.com", StartDate: "2026-09-03", EndDate: "2026-09-09", Dimensions: []string{"date"}},
		PreviousStartDate: "2026-08-27", PreviousEndDate: "2026-09-02",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Current.FirstObservedDate != "2026-09-05" || res.Current.LastObservedDate != "2026-09-07" || res.Previous.FirstObservedDate != "" {
		t.Errorf("observed = %+v / %+v", res.Current, res.Previous)
	}
}

func TestComparePerformanceFailures(t *testing.T) {
	// Upstream error on the previous window is labelled and propagated.
	h, _ := compareServer(t, map[string]string{"2026-09-03": `{"rows":[]}`})
	c, _ := newTestClient(t, h)
	_, err := c.ComparePerformance(context.Background(), CompareRequest{
		Base:              PerformanceRequest{Site: "sc-domain:example.com", StartDate: "2026-09-03", EndDate: "2026-09-09"},
		PreviousStartDate: "2026-08-27", PreviousEndDate: "2026-09-02",
	})
	ge := gscerr.From(err)
	if ge == nil || ge.Code != gscerr.CodeInvalidArgument || !strings.Contains(ge.Message, "previous window (2026-08-27..2026-09-02)") {
		t.Errorf("upstream error: %+v", ge)
	}

	// A window that hits the safety guard fails the comparison.
	full := make([]string, MaxRowLimit)
	for i := range full {
		full[i] = row(fmt.Sprintf("/p%d", i), 1, 1, 1, 1)
	}
	body := `{"rows":[` + strings.Join(full, ",") + `]}`
	pages := 0
	c2, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		pages++
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		start, _ := req["startRow"].(float64)
		// Distinct rows per page so repeated-page detection does not fire first.
		_, _ = io.WriteString(w, strings.ReplaceAll(body, `"/p`, fmt.Sprintf(`"/s%d-p`, int(start))))
	})
	c2.MaxAutoPages = 2
	_, err = c2.ComparePerformance(context.Background(), CompareRequest{
		Base:              PerformanceRequest{Site: "sc-domain:example.com", StartDate: "2026-09-03", EndDate: "2026-09-09", Dimensions: []string{"page"}},
		PreviousStartDate: "2026-08-27", PreviousEndDate: "2026-09-02",
	})
	ge = gscerr.From(err)
	if ge == nil || ge.Code != gscerr.CodePaginationIncomplete || !strings.Contains(ge.Message, "safety_cap") || ge.Action == "" {
		t.Errorf("safety cap: %+v (pages=%d)", ge, pages)
	}
	if pages != 2 {
		t.Errorf("previous window must not be fetched after the current one fails: pages=%d", pages)
	}
}

func m(clicks, impressions, ctr, position float64) *Metrics {
	return &Metrics{Clicks: clicks, Impressions: impressions, CTR: ctr, Position: position}
}

// sortFixture returns joined rows covering gains, losses, ties, and rows
// present in only one period.
func sortFixture() ([]CompareRow, []string) {
	dims := []string{"page"}
	mk := func(key string, cur, prev *Metrics) CompareRow {
		return CompareRow{Dimensions: dims, Keys: map[string]string{"page": key}, Current: cur, Previous: prev, Delta: deltaOf(cur, prev)}
	}
	rows := []CompareRow{
		mk("/steady", m(50, 500, 0.1, 5), m(50, 500, 0.1, 5)),  // deltas 0, position 0
		mk("/gainer", m(40, 900, 0.04, 3), m(10, 100, 0.1, 9)), // +30 clicks, +800 impr, position -6 (improved)
		mk("/loser", m(2, 20, 0.1, 30), m(60, 1000, 0.06, 4)),  // -58 clicks, -980 impr, position +26 (worse)
		mk("/gone", nil, m(20, 400, 0.05, 6)),                  // only previous: -20, -400, no position delta
		mk("/new", m(5, 150, 0.03, 12), nil),                   // only current: +5, +150, no position delta
		mk("/tie-a", m(5, 150, 0.03, 12), m(5, 50, 0.1, 12)),   // +100 impr; ties with /tie-b on impr delta
		mk("/tie-b", m(5, 150, 0.03, 12), m(5, 50, 0.1, 12)),   // identical to /tie-a except key
		mk("/tie-c", m(6, 150, 0.04, 12), m(1, 50, 0.1, 13)),   // +100 impr, but more current clicks than tie-a/b
	}
	return rows, dims
}

func keysOf(rows []CompareRow) string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Keys["page"]
	}
	return strings.Join(out, " ")
}

func TestSortRowsDefaultUnchanged(t *testing.T) {
	rows, dims := sortFixture()
	// Default: current clicks desc, current impressions desc, previous clicks desc, key asc.
	SortRows(rows, dims, SortCurrentClicks, false)
	want := "/steady /gainer /tie-c /tie-a /tie-b /new /loser /gone"
	if got := keysOf(rows); got != want {
		t.Errorf("default order:\n got %s\nwant %s", got, want)
	}
	// joinRows must produce the same default order.
	cur := []Row{{Dimensions: dims, Keys: map[string]string{"page": "/b"}, Clicks: 1, Impressions: 9}, {Dimensions: dims, Keys: map[string]string{"page": "/a"}, Clicks: 1, Impressions: 9}}
	joined := joinRows(dims, cur, nil)
	if got := keysOf(joined); got != "/a /b" {
		t.Errorf("joinRows default order = %s", got)
	}
}

func TestSortRowsImpressionsDelta(t *testing.T) {
	rows, dims := sortFixture()
	SortRows(rows, dims, SortImpressionsDelta, false)
	// Biggest positive first: +800, +150 (/new), +100 x3 (tie-c has more current clicks; a before b by key), 0, -400, -980.
	want := "/gainer /new /tie-c /tie-a /tie-b /steady /gone /loser"
	if got := keysOf(rows); got != want {
		t.Errorf("impressions-delta desc:\n got %s\nwant %s", got, want)
	}
	SortRows(rows, dims, SortImpressionsDelta, true)
	// Biggest losses first; ties still break by the fixed descending chain.
	want = "/loser /gone /steady /tie-c /tie-a /tie-b /new /gainer"
	if got := keysOf(rows); got != want {
		t.Errorf("impressions-delta asc:\n got %s\nwant %s", got, want)
	}
}

func TestSortRowsClicksDeltaAndCurrentImpressions(t *testing.T) {
	rows, dims := sortFixture()
	SortRows(rows, dims, SortClicksDelta, true)
	if got := keysOf(rows); !strings.HasPrefix(got, "/loser /gone ") || !strings.HasSuffix(got, " /gainer") {
		t.Errorf("clicks-delta asc = %s", got)
	}
	SortRows(rows, dims, SortCurrentImpressions, false)
	// 900, 500, 150 x4 (tie chain: current clicks desc -> tie-c 6 first; then a, b, new by previous clicks desc then key), 20, 0.
	want := "/gainer /steady /tie-c /tie-a /tie-b /new /loser /gone"
	if got := keysOf(rows); got != want {
		t.Errorf("current-impressions desc:\n got %s\nwant %s", got, want)
	}
}

func TestSortRowsPositionDeltaMissingLast(t *testing.T) {
	rows, dims := sortFixture()
	SortRows(rows, dims, SortPositionDelta, true)
	// Most improved (most negative) first; rows without a position delta last.
	got := keysOf(rows)
	if !strings.HasPrefix(got, "/gainer ") || !strings.HasSuffix(got, " /new /gone") {
		t.Errorf("position-delta asc = %s", got)
	}
	SortRows(rows, dims, SortPositionDelta, false)
	got = keysOf(rows)
	if !strings.HasPrefix(got, "/loser ") || !strings.HasSuffix(got, " /new /gone") {
		t.Errorf("position-delta desc = %s", got)
	}
}

func TestSortRowsDeterministic(t *testing.T) {
	for _, key := range []string{SortCurrentClicks, SortCurrentImpressions, SortClicksDelta, SortImpressionsDelta, SortPositionDelta} {
		for _, asc := range []bool{false, true} {
			a, dims := sortFixture()
			b, _ := sortFixture()
			// Reverse the input of b; a total order must give the same result.
			for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
				b[i], b[j] = b[j], b[i]
			}
			SortRows(a, dims, key, asc)
			SortRows(b, dims, key, asc)
			if keysOf(a) != keysOf(b) {
				t.Errorf("%s asc=%v not deterministic: %s vs %s", key, asc, keysOf(a), keysOf(b))
			}
		}
	}
}

func TestParseSortKey(t *testing.T) {
	for in, want := range map[string]string{"": SortCurrentClicks, "current-clicks": SortCurrentClicks, "Impressions-Delta": SortImpressionsDelta, "position-delta": SortPositionDelta} {
		got, err := ParseSortKey(in)
		if err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	if _, err := ParseSortKey("ctr"); code(err) != gscerr.CodeInvalidArgument {
		t.Errorf("bad key: %v", err)
	}
}
