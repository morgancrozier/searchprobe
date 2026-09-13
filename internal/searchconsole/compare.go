package searchconsole

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

// CompareRequest runs the same Search Analytics query over two date windows.
// Base carries the current window in StartDate/EndDate plus every other
// parameter (site, dimensions, filters, type, data state, aggregation), all
// of which are reused unchanged for the previous window. RowLimit and
// StartRow must be zero: both windows are fetched with automatic pagination.
type CompareRequest struct {
	Base              PerformanceRequest
	PreviousStartDate string
	PreviousEndDate   string
}

// Metrics is one period's metrics for a joined row.
type Metrics struct {
	Clicks      float64 `json:"clicks"`
	Impressions float64 `json:"impressions"`
	CTR         float64 `json:"ctr"`
	Position    float64 `json:"position"`
}

// Delta is current minus previous. Clicks and impressions treat a missing
// period as zero. CTR and Position are nil unless both periods have the
// row (an average of nothing is undefined). Percentages are nil when the
// previous value is missing or zero. Position: lower is better, so a
// negative delta means the average rank improved.
type Delta struct {
	Clicks         float64  `json:"clicks"`
	Impressions    float64  `json:"impressions"`
	CTR            *float64 `json:"ctr"`
	Position       *float64 `json:"position"`
	ClicksPct      *float64 `json:"clicksPct"`
	ImpressionsPct *float64 `json:"impressionsPct"`
}

// CompareRow is one joined row. Current or Previous is nil when the row
// appeared in only one period.
type CompareRow struct {
	Dimensions []string
	Keys       map[string]string
	Current    *Metrics
	Previous   *Metrics
	Delta      Delta
}

// MarshalJSON emits dimension values first (request order), then current,
// previous, and delta.
func (r CompareRow) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for _, d := range r.Dimensions {
		k, _ := json.Marshal(d)
		v, _ := json.Marshal(r.Keys[d])
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
		b.WriteByte(',')
	}
	cur, _ := json.Marshal(r.Current)
	prev, _ := json.Marshal(r.Previous)
	delta, _ := json.Marshal(r.Delta)
	fmt.Fprintf(&b, `"current":%s,"previous":%s,"delta":%s}`, cur, prev, delta)
	return []byte(b.String()), nil
}

// Totals sums the additive metrics of one period's returned rows.
type Totals struct {
	Rows        int     `json:"rows"`
	Clicks      float64 `json:"clicks"`
	Impressions float64 `json:"impressions"`
}

// TotalsDelta is current totals minus previous totals.
type TotalsDelta struct {
	Clicks         float64  `json:"clicks"`
	Impressions    float64  `json:"impressions"`
	ClicksPct      *float64 `json:"clicksPct"`
	ImpressionsPct *float64 `json:"impressionsPct"`
}

// Window describes one fetched period.
type Window struct {
	StartDate               string
	EndDate                 string
	Days                    int
	RowCount                int
	PagesFetched            int
	ResponseAggregationType string
	FirstIncompleteDate     string
	FirstIncompleteHour     string
	// FirstObservedDate/LastObservedDate are the earliest and latest date
	// values among the rows Google returned (only when the date dimension
	// was requested). They describe returned rows, not data availability:
	// a missing date row can mean no activity or top-rows truncation.
	FirstObservedDate string
	LastObservedDate  string
}

// CompareResult is the joined comparison.
type CompareResult struct {
	Site            string
	Dimensions      []string
	SearchType      string
	DataState       string
	Filters         []DimensionFilter
	AggregationType string
	Current         Window
	Previous        Window
	// Rows is sorted by current clicks desc, current impressions desc,
	// previous clicks desc, then dimension values asc.
	Rows          []CompareRow
	CurrentTotals Totals
	PrevTotals    Totals
	TotalsDelta   TotalsDelta
}

// Sort keys for CompareRows. SortCurrentClicks (descending) is the default
// and its full chain is also the tiebreaker for every other key.
const (
	SortCurrentClicks      = "current-clicks"
	SortCurrentImpressions = "current-impressions"
	SortClicksDelta        = "clicks-delta"
	SortImpressionsDelta   = "impressions-delta"
	SortPositionDelta      = "position-delta"

	// SortKeyList is the documented list for help and errors.
	SortKeyList = "current-clicks, current-impressions, clicks-delta, impressions-delta, position-delta"
	// TieBreakOrder is applied after the chosen key.
	TieBreakOrder = "current.clicks desc, current.impressions desc, previous.clicks desc, dimension values asc"
)

// SortOrder documents the default CompareResult.Rows ordering for metadata.
const SortOrder = TieBreakOrder

// ParseSortKey validates a --sort value. Empty means SortCurrentClicks.
func ParseSortKey(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "":
		return SortCurrentClicks, nil
	case SortCurrentClicks, SortCurrentImpressions, SortClicksDelta, SortImpressionsDelta, SortPositionDelta:
		return v, nil
	}
	return "", gscerr.New(gscerr.CodeInvalidArgument, fmt.Sprintf("Unknown sort key %q.", raw), "Use one of: "+SortKeyList+".")
}

// SortRows orders rows in place by key. Descending is the natural order
// (largest current values or largest positive deltas first); ascending
// puts the smallest or most negative first, so "impressions-delta"
// ascending lists the biggest impression losses first. Ties fall through
// to TieBreakOrder, which is total, so the result is deterministic. For
// position-delta, rows without a position delta (present in only one
// period) always sort last, in either direction.
func SortRows(rows []CompareRow, dims []string, key string, ascending bool) {
	sort.SliceStable(rows, func(a, b int) bool {
		if key != SortCurrentClicks {
			va, oka := sortValue(rows[a], key)
			vb, okb := sortValue(rows[b], key)
			switch {
			case oka && !okb:
				return true
			case !oka && okb:
				return false
			case va != vb:
				if ascending {
					return va < vb
				}
				return va > vb
			}
		}
		return tieBreakLess(rows[a], rows[b], dims, ascending && key == SortCurrentClicks)
	})
}

// sortValue returns the value for key and whether it is defined.
func sortValue(r CompareRow, key string) (float64, bool) {
	switch key {
	case SortCurrentImpressions:
		return metricsOrZero(r.Current).Impressions, true
	case SortClicksDelta:
		return r.Delta.Clicks, true
	case SortImpressionsDelta:
		return r.Delta.Impressions, true
	case SortPositionDelta:
		if r.Delta.Position == nil {
			return 0, false
		}
		return *r.Delta.Position, true
	default:
		return metricsOrZero(r.Current).Clicks, true
	}
}

// tieBreakLess implements TieBreakOrder. When reversed is true (the default
// key sorted ascending) the numeric comparisons flip; dimension values stay
// ascending so the chain remains total.
func tieBreakLess(x, y CompareRow, dims []string, reversed bool) bool {
	gt := func(a, b float64) bool {
		if reversed {
			return a < b
		}
		return a > b
	}
	cx, cy := metricsOrZero(x.Current), metricsOrZero(y.Current)
	if cx.Clicks != cy.Clicks {
		return gt(cx.Clicks, cy.Clicks)
	}
	if cx.Impressions != cy.Impressions {
		return gt(cx.Impressions, cy.Impressions)
	}
	px, py := metricsOrZero(x.Previous), metricsOrZero(y.Previous)
	if px.Clicks != py.Clicks {
		return gt(px.Clicks, py.Clicks)
	}
	return rowKey(dims, x.Keys) < rowKey(dims, y.Keys)
}

// Validate checks the comparison windows. The base request is validated by
// QueryPerformanceAll.
func (r CompareRequest) Validate() error {
	if r.Base.RowLimit != 0 || r.Base.StartRow != 0 {
		return gscerr.New(gscerr.CodeInvalidArgument, "Comparison always paginates both windows fully; a row limit or start row cannot be set on the query.", "")
	}
	if _, err := WindowDays(r.Base.StartDate, r.Base.EndDate); err != nil {
		return err
	}
	if _, err := WindowDays(r.PreviousStartDate, r.PreviousEndDate); err != nil {
		e := gscerr.From(err)
		return gscerr.New(gscerr.CodeInvalidDateRange, "Comparison window: "+e.Message, "Pass --compare-start and --compare-end as YYYY-MM-DD with start before end.")
	}
	if r.PreviousStartDate <= r.Base.EndDate && r.PreviousEndDate >= r.Base.StartDate {
		return gscerr.New(gscerr.CodeInvalidDateRange,
			fmt.Sprintf("Comparison window %s..%s overlaps the current window %s..%s.", r.PreviousStartDate, r.PreviousEndDate, r.Base.StartDate, r.Base.EndDate),
			"Choose non-overlapping windows, or use --previous for the immediately preceding window of equal length.")
	}
	return nil
}

// ComparePerformance fetches both windows (fully paginated) and joins rows
// by their dimension values. If either window stops before the API query is
// exhausted, it fails rather than return a deceptively complete comparison.
func (c *Client) ComparePerformance(ctx context.Context, req CompareRequest) (*CompareResult, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	cur, err := c.compareWindow(ctx, req.Base, "current")
	if err != nil {
		return nil, err
	}
	prevReq := req.Base
	prevReq.StartDate, prevReq.EndDate = req.PreviousStartDate, req.PreviousEndDate
	prev, err := c.compareWindow(ctx, prevReq, "previous")
	if err != nil {
		return nil, err
	}

	res := &CompareResult{
		Site:            cur.Site,
		Dimensions:      cur.Dimensions,
		SearchType:      cur.SearchType,
		DataState:       cur.DataState,
		Filters:         cur.Filters,
		AggregationType: cur.AggregationType,
		Current:         windowOf(cur),
		Previous:        windowOf(prev),
		Rows:            joinRows(cur.Dimensions, cur.Rows, prev.Rows),
	}
	res.CurrentTotals = totalsOf(cur)
	res.PrevTotals = totalsOf(prev)
	res.TotalsDelta = TotalsDelta{
		Clicks:         res.CurrentTotals.Clicks - res.PrevTotals.Clicks,
		Impressions:    res.CurrentTotals.Impressions - res.PrevTotals.Impressions,
		ClicksPct:      pctChange(res.CurrentTotals.Clicks, res.PrevTotals.Clicks, true),
		ImpressionsPct: pctChange(res.CurrentTotals.Impressions, res.PrevTotals.Impressions, true),
	}
	return res, nil
}

func (c *Client) compareWindow(ctx context.Context, req PerformanceRequest, label string) (*PerformanceResult, error) {
	res, err := c.QueryPerformanceAll(ctx, req)
	if err != nil {
		ge := gscerr.From(err)
		return nil, &gscerr.Error{Code: ge.Code, Message: fmt.Sprintf("%s window (%s..%s): %s", label, req.StartDate, req.EndDate, ge.Message), Action: ge.Action, Retryable: ge.Retryable, Cause: err}
	}
	if !res.PaginationExhausted {
		return nil, gscerr.New(gscerr.CodePaginationIncomplete,
			fmt.Sprintf("The %s window (%s..%s) stopped after %d pages without exhausting the API query (%s), so the comparison would be incomplete.", label, req.StartDate, req.EndDate, res.PagesFetched, res.PaginationStopReason),
			"Narrow the query with --filter or fewer dimensions and retry.")
	}
	return res, nil
}

func windowOf(p *PerformanceResult) Window {
	days, _ := WindowDays(p.StartDate, p.EndDate)
	w := Window{
		StartDate:               p.StartDate,
		EndDate:                 p.EndDate,
		Days:                    days,
		RowCount:                len(p.Rows),
		PagesFetched:            p.PagesFetched,
		ResponseAggregationType: p.ResponseAggregationType,
		FirstIncompleteDate:     p.FirstIncompleteDate,
		FirstIncompleteHour:     p.FirstIncompleteHour,
	}
	if first, last, ok := ObservedDateRange(p.Rows); ok {
		w.FirstObservedDate, w.LastObservedDate = first, last
	}
	return w
}

func totalsOf(p *PerformanceResult) Totals {
	clicks, impressions := p.Totals()
	return Totals{Rows: len(p.Rows), Clicks: clicks, Impressions: impressions}
}

func rowKey(dims []string, keys map[string]string) string {
	parts := make([]string, len(dims))
	for i, d := range dims {
		parts[i] = keys[d]
	}
	return strings.Join(parts, "\x00")
}

func joinRows(dims []string, cur, prev []Row) []CompareRow {
	type joined struct {
		keys map[string]string
		cur  *Metrics
		prev *Metrics
	}
	index := map[string]*joined{}
	var order []string
	for _, r := range cur {
		k := rowKey(dims, r.Keys)
		index[k] = &joined{keys: r.Keys, cur: &Metrics{r.Clicks, r.Impressions, r.CTR, r.Position}}
		order = append(order, k)
	}
	for _, r := range prev {
		k := rowKey(dims, r.Keys)
		j, ok := index[k]
		if !ok {
			j = &joined{keys: r.Keys}
			index[k] = j
			order = append(order, k)
		}
		j.prev = &Metrics{r.Clicks, r.Impressions, r.CTR, r.Position}
	}
	out := make([]CompareRow, 0, len(order))
	for _, k := range order {
		j := index[k]
		out = append(out, CompareRow{Dimensions: dims, Keys: j.keys, Current: j.cur, Previous: j.prev, Delta: deltaOf(j.cur, j.prev)})
	}
	SortRows(out, dims, SortCurrentClicks, false)
	return out
}

func metricsOrZero(m *Metrics) Metrics {
	if m == nil {
		return Metrics{}
	}
	return *m
}

func deltaOf(cur, prev *Metrics) Delta {
	c, p := metricsOrZero(cur), metricsOrZero(prev)
	d := Delta{
		Clicks:         c.Clicks - p.Clicks,
		Impressions:    c.Impressions - p.Impressions,
		ClicksPct:      pctChange(c.Clicks, p.Clicks, prev != nil),
		ImpressionsPct: pctChange(c.Impressions, p.Impressions, prev != nil),
	}
	if cur != nil && prev != nil {
		ctr := round(c.CTR-p.CTR, 6)
		pos := round(c.Position-p.Position, 2)
		d.CTR, d.Position = &ctr, &pos
	}
	return d
}

// pctChange returns (cur-prev)/prev*100 rounded to two decimals, or nil when
// the previous value is absent or zero (no meaningful baseline).
func pctChange(cur, prev float64, prevPresent bool) *float64 {
	if !prevPresent || prev == 0 {
		return nil
	}
	v := round((cur-prev)/prev*100, 2)
	return &v
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	r := math.Round(v*p) / p
	if r == 0 {
		return 0 // normalize -0
	}
	return r
}
