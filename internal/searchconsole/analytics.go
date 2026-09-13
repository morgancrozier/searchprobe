package searchconsole

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

// Search Analytics limits documented by Google at
// https://developers.google.com/webmaster-tools/v1/searchanalytics/query
const (
	DefaultRowLimit = 1000
	MaxRowLimit     = 25000
	DateFormat      = "2006-01-02"

	// DefaultMaxAutoPages is a defensive bug guard for automatic pagination,
	// not a product limit: 1,000 pages is 25,000,000 rows. Normal termination
	// is a short page (the API query is exhausted), a repeated page, context
	// cancellation, or an upstream error. Reaching this guard is reported as
	// PaginationStopReason StopSafetyCap and never as exhaustion.
	DefaultMaxAutoPages = 1000

	DataStateFinal     = "final"
	DataStateAll       = "all"
	DataStateHourlyAll = "hourly_all"

	SearchTypeWeb = "web"
)

// Canonical names accepted by Google. Keys are lower-cased user input.
var (
	canonicalDimensions = map[string]string{
		"query":            "query",
		"page":             "page",
		"country":          "country",
		"device":           "device",
		"date":             "date",
		"searchappearance": "searchAppearance",
		"hour":             "hour",
	}
	canonicalFilterDimensions = map[string]string{
		"query":            "query",
		"page":             "page",
		"country":          "country",
		"device":           "device",
		"searchappearance": "searchAppearance",
	}
	canonicalFilterOperators = map[string]string{
		"contains":       "contains",
		"equals":         "equals",
		"notcontains":    "notContains",
		"notequals":      "notEquals",
		"includingregex": "includingRegex",
		"excludingregex": "excludingRegex",
	}
	canonicalSearchTypes = map[string]string{
		"web":        "web",
		"image":      "image",
		"video":      "video",
		"news":       "news",
		"discover":   "discover",
		"googlenews": "googleNews",
	}
	canonicalDataStates = map[string]string{
		"final":      DataStateFinal,
		"all":        DataStateAll,
		"hourly_all": DataStateHourlyAll,
	}
	canonicalAggregationTypes = map[string]string{
		"auto":                "auto",
		"bypage":              "byPage",
		"byproperty":          "byProperty",
		"bynewsshowcasepanel": "byNewsShowcasePanel",
	}
)

// Documented value lists, used in error actions and help text.
const (
	DimensionList       = "query, page, country, device, date, searchAppearance, hour"
	FilterDimensionList = "query, page, country, device, searchAppearance"
	FilterOperatorList  = "contains, equals, notContains, notEquals, includingRegex, excludingRegex"
	SearchTypeList      = "web, image, video, news, discover, googleNews"
	DataStateList       = "final, all, hourly_all"
	AggregationTypeList = "auto, byPage, byProperty, byNewsShowcasePanel"
)

func canonicalize(table map[string]string, raw, what, list string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", nil
	}
	canon, ok := table[strings.ToLower(v)]
	if !ok {
		return "", gscerr.New(gscerr.CodeInvalidArgument,
			fmt.Sprintf("Unknown %s %q.", what, v),
			fmt.Sprintf("Use one of: %s.", list))
	}
	return canon, nil
}

// ParseDimensions normalizes a comma-separated, case-insensitive dimension
// list into Google's canonical names, rejecting unknowns and duplicates.
func ParseDimensions(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		canon, err := canonicalize(canonicalDimensions, part, "dimension", DimensionList)
		if err != nil {
			return nil, err
		}
		if seen[canon] {
			return nil, gscerr.New(gscerr.CodeInvalidArgument,
				fmt.Sprintf("Dimension %q was given more than once.", canon),
				"List each dimension once.")
		}
		seen[canon] = true
		out = append(out, canon)
	}
	return out, nil
}

// ParseSearchType validates a search type (web, image, ...). Empty means web.
func ParseSearchType(raw string) (string, error) {
	return canonicalize(canonicalSearchTypes, raw, "search type", SearchTypeList)
}

// ParseDataState validates a data state. Empty means final.
func ParseDataState(raw string) (string, error) {
	return canonicalize(canonicalDataStates, raw, "data state", DataStateList)
}

// ParseAggregationType validates an aggregation type. Empty lets Google choose (auto).
func ParseAggregationType(raw string) (string, error) {
	return canonicalize(canonicalAggregationTypes, raw, "aggregation type", AggregationTypeList)
}

// DimensionFilter is one Search Analytics filter. Field names and values are
// Google's, so it serializes directly into dimensionFilterGroups[].filters[].
type DimensionFilter struct {
	Dimension  string `json:"dimension"`
	Operator   string `json:"operator"`
	Expression string `json:"expression"`
}

// ParseFilter parses the CLI filter syntax "<dimension> <operator> <expression>",
// for example "page contains /blog/" or "query includingRegex ^buy .*".
// The expression is everything after the operator, with surrounding
// whitespace trimmed, so it may contain spaces.
func ParseFilter(raw string) (DimensionFilter, error) {
	usage := "Write filters as \"<dimension> <operator> <expression>\", for example \"page contains /blog/\"."
	fields := strings.Fields(raw)
	if len(fields) < 3 {
		return DimensionFilter{}, gscerr.New(gscerr.CodeInvalidArgument,
			fmt.Sprintf("Filter %q is incomplete.", raw), usage)
	}
	dim, err := canonicalize(canonicalFilterDimensions, fields[0], "filter dimension", FilterDimensionList)
	if err != nil {
		return DimensionFilter{}, err
	}
	op, err := canonicalize(canonicalFilterOperators, fields[1], "filter operator", FilterOperatorList)
	if err != nil {
		return DimensionFilter{}, err
	}
	// Recover the expression from the original string so internal spacing is preserved.
	trimmed := strings.TrimSpace(raw)
	rest := strings.TrimSpace(trimmed[len(fields[0]):])
	rest = strings.TrimSpace(rest[len(fields[1]):])
	if rest == "" {
		return DimensionFilter{}, gscerr.New(gscerr.CodeInvalidArgument,
			fmt.Sprintf("Filter %q has no expression.", raw), usage)
	}
	return DimensionFilter{Dimension: dim, Operator: op, Expression: rest}, nil
}

// PerformanceRequest describes one Search Analytics query.
type PerformanceRequest struct {
	Site       string
	StartDate  string // YYYY-MM-DD, Pacific Time
	EndDate    string // YYYY-MM-DD, Pacific Time
	Dimensions []string
	// SearchType is Google's "type": web (default), image, video, news, discover, googleNews.
	SearchType string
	// DataState is final (default), all, or hourly_all.
	DataState string
	// Filters are ANDed together in a single dimensionFilterGroup. Google
	// only documents groupType "and", and multiple groups are also ANDed, so
	// one group expresses everything the API can.
	Filters []DimensionFilter
	// AggregationType is Google's aggregationType; empty lets Google choose (auto).
	AggregationType string
	RowLimit        int // 1..25000; 0 means DefaultRowLimit
	StartRow        int
}

func (r *PerformanceRequest) applyDefaults() {
	if r.RowLimit == 0 {
		r.RowLimit = DefaultRowLimit
	}
	if r.SearchType == "" {
		r.SearchType = SearchTypeWeb
	}
	if r.DataState == "" {
		r.DataState = DataStateFinal
	}
}

// Validate checks the request against documented API constraints. Values are
// expected in canonical form (see the Parse* helpers).
func (r PerformanceRequest) Validate() error {
	if strings.TrimSpace(r.Site) == "" {
		return gscerr.New(gscerr.CodeInvalidArgument, "A Search Console property is required.", "Pass --site with a value from `gsc sites --json`.")
	}
	start, err := time.Parse(DateFormat, r.StartDate)
	if err != nil {
		return gscerr.New(gscerr.CodeInvalidDateRange, fmt.Sprintf("Start date %q is not YYYY-MM-DD.", r.StartDate), "Use --start YYYY-MM-DD, for example --start 2026-08-01.")
	}
	end, err := time.Parse(DateFormat, r.EndDate)
	if err != nil {
		return gscerr.New(gscerr.CodeInvalidDateRange, fmt.Sprintf("End date %q is not YYYY-MM-DD.", r.EndDate), "Use --end YYYY-MM-DD, for example --end 2026-08-31.")
	}
	if end.Before(start) {
		return gscerr.New(gscerr.CodeInvalidDateRange, fmt.Sprintf("End date %s is before start date %s.", r.EndDate, r.StartDate), "Swap --start and --end.")
	}
	if r.RowLimit < 0 || r.RowLimit > MaxRowLimit {
		return gscerr.New(gscerr.CodeInvalidArgument, fmt.Sprintf("Row limit must be between 1 and %d.", MaxRowLimit), "Adjust --limit.")
	}
	if r.StartRow < 0 {
		return gscerr.New(gscerr.CodeInvalidArgument, "Start row must be zero or greater.", "Adjust --start-row.")
	}
	if r.SearchType != "" && canonicalSearchTypes[strings.ToLower(r.SearchType)] != r.SearchType {
		return gscerr.New(gscerr.CodeInvalidArgument, fmt.Sprintf("Unknown search type %q.", r.SearchType), "Use one of: "+SearchTypeList+".")
	}
	if r.DataState != "" && canonicalDataStates[strings.ToLower(r.DataState)] != r.DataState {
		return gscerr.New(gscerr.CodeInvalidArgument, fmt.Sprintf("Unknown data state %q.", r.DataState), "Use one of: "+DataStateList+".")
	}
	if r.AggregationType != "" && canonicalAggregationTypes[strings.ToLower(r.AggregationType)] != r.AggregationType {
		return gscerr.New(gscerr.CodeInvalidArgument, fmt.Sprintf("Unknown aggregation type %q.", r.AggregationType), "Use one of: "+AggregationTypeList+".")
	}
	for _, d := range r.Dimensions {
		if canonicalDimensions[strings.ToLower(d)] != d {
			return gscerr.New(gscerr.CodeInvalidArgument, fmt.Sprintf("Unknown dimension %q.", d), "Use one of: "+DimensionList+".")
		}
		if d == "hour" && r.DataState != DataStateHourlyAll {
			return gscerr.New(gscerr.CodeInvalidDimensionCombination,
				"The hour dimension is only available with data state hourly_all.",
				"Add --data-state hourly_all, or use the date dimension.")
		}
	}
	for _, f := range r.Filters {
		if canonicalFilterDimensions[strings.ToLower(f.Dimension)] != f.Dimension {
			return gscerr.New(gscerr.CodeInvalidArgument, fmt.Sprintf("Filters cannot use dimension %q.", f.Dimension), "Filter on one of: "+FilterDimensionList+".")
		}
		if canonicalFilterOperators[strings.ToLower(f.Operator)] != f.Operator {
			return gscerr.New(gscerr.CodeInvalidArgument, fmt.Sprintf("Unknown filter operator %q.", f.Operator), "Use one of: "+FilterOperatorList+".")
		}
		if strings.TrimSpace(f.Expression) == "" {
			return gscerr.New(gscerr.CodeInvalidArgument, "A filter expression is empty.", "Provide a value after the operator.")
		}
	}
	return nil
}

// Wire types documented at
// https://developers.google.com/webmaster-tools/v1/searchanalytics/query
type searchAnalyticsRequest struct {
	StartDate             string                 `json:"startDate"`
	EndDate               string                 `json:"endDate"`
	Dimensions            []string               `json:"dimensions,omitempty"`
	Type                  string                 `json:"type,omitempty"`
	DimensionFilterGroups []dimensionFilterGroup `json:"dimensionFilterGroups,omitempty"`
	AggregationType       string                 `json:"aggregationType,omitempty"`
	RowLimit              int                    `json:"rowLimit"`
	StartRow              int                    `json:"startRow,omitempty"`
	DataState             string                 `json:"dataState"`
}

type dimensionFilterGroup struct {
	GroupType string            `json:"groupType"`
	Filters   []DimensionFilter `json:"filters"`
}

type searchAnalyticsResponse struct {
	Rows []struct {
		Keys        []string `json:"keys"`
		Clicks      float64  `json:"clicks"`
		Impressions float64  `json:"impressions"`
		CTR         float64  `json:"ctr"`
		Position    float64  `json:"position"`
	} `json:"rows"`
	ResponseAggregationType string `json:"responseAggregationType"`
	Metadata                struct {
		FirstIncompleteDate string `json:"first_incomplete_date"`
		FirstIncompleteHour string `json:"first_incomplete_hour"`
	} `json:"metadata"`
}

func (r PerformanceRequest) wire() searchAnalyticsRequest {
	w := searchAnalyticsRequest{
		StartDate:       r.StartDate,
		EndDate:         r.EndDate,
		Dimensions:      r.Dimensions,
		Type:            r.SearchType,
		AggregationType: r.AggregationType,
		RowLimit:        r.RowLimit,
		StartRow:        r.StartRow,
		DataState:       r.DataState,
	}
	if len(r.Filters) > 0 {
		w.DimensionFilterGroups = []dimensionFilterGroup{{GroupType: "and", Filters: r.Filters}}
	}
	return w
}

// Row is one normalized Search Analytics row. Dimension values are keyed by
// dimension name; metrics are typed numbers. It marshals flat, with
// dimensions first in request order, then clicks, impressions, ctr, position.
type Row struct {
	Dimensions  []string
	Keys        map[string]string
	Clicks      float64
	Impressions float64
	CTR         float64
	Position    float64
}

// MarshalJSON emits a flat object with deterministic key order.
func (r Row) MarshalJSON() ([]byte, error) {
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
	fmt.Fprintf(&b, `"clicks":%s,"impressions":%s,"ctr":%s,"position":%s}`,
		jsonNumber(r.Clicks), jsonNumber(r.Impressions), jsonNumber(r.CTR), jsonNumber(r.Position))
	return []byte(b.String()), nil
}

func jsonNumber(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func (r Row) sameAs(o Row) bool {
	if r.Clicks != o.Clicks || r.Impressions != o.Impressions || r.CTR != o.CTR || r.Position != o.Position || len(r.Keys) != len(o.Keys) {
		return false
	}
	for k, v := range r.Keys {
		if o.Keys[k] != v {
			return false
		}
	}
	return true
}

// Pagination stop reasons for PerformanceResult.PaginationStopReason.
const (
	StopSafetyCap    = "safety_cap"
	StopRepeatedPage = "repeated_page"
)

// PerformanceResult is the normalized Search Analytics response.
type PerformanceResult struct {
	Site            string
	StartDate       string
	EndDate         string
	Dimensions      []string
	SearchType      string
	DataState       string
	Filters         []DimensionFilter
	AggregationType string // requested; "" means auto
	RowLimit        int    // per-request row limit (page size under --all)
	StartRow        int
	Rows            []Row
	// ResponseAggregationType is Google's responseAggregationType.
	ResponseAggregationType string
	// FirstIncompleteDate/Hour are set when Google flags preliminary data.
	FirstIncompleteDate string
	FirstIncompleteHour string
	// PagesFetched is the number of API requests made (1 unless auto-paginating).
	PagesFetched int
	// PaginationExhausted is true when the API query has no further pages.
	// It says nothing about whether Google exposed every underlying row; the
	// Search Analytics API returns top rows only, so the source data may
	// still be partial.
	PaginationExhausted bool
	// PaginationStopReason is set when auto-pagination stopped early
	// (StopSafetyCap or StopRepeatedPage); empty otherwise.
	PaginationStopReason string
}

// Totals sums clicks and impressions over the returned rows only.
func (p *PerformanceResult) Totals() (clicks, impressions float64) {
	for _, r := range p.Rows {
		clicks += r.Clicks
		impressions += r.Impressions
	}
	return clicks, impressions
}

// QueryPerformance runs one Search Analytics request (no automatic pagination).
func (c *Client) QueryPerformance(ctx context.Context, req PerformanceRequest) (*PerformanceResult, error) {
	req.applyDefaults()
	if err := req.Validate(); err != nil {
		return nil, err
	}
	resp, err := c.queryPage(ctx, req)
	if err != nil {
		return nil, err
	}
	res := newPerformanceResult(req)
	res.Rows = normalizeRows(req.Dimensions, resp)
	res.ResponseAggregationType = resp.ResponseAggregationType
	res.FirstIncompleteDate = resp.Metadata.FirstIncompleteDate
	res.FirstIncompleteHour = resp.Metadata.FirstIncompleteHour
	res.PagesFetched = 1
	res.PaginationExhausted = len(res.Rows) < req.RowLimit
	return res, nil
}

// QueryPerformanceAll pages through every row the API exposes for the query,
// using the maximum page size, and returns one merged result. RowLimit and
// StartRow must be zero. It stops when a short page arrives (exhausted), if
// Google returns the same page twice, or at the defensive page guard
// (Client.MaxAutoPages); the latter two are reported via
// PaginationStopReason with PaginationExhausted left false.
func (c *Client) QueryPerformanceAll(ctx context.Context, req PerformanceRequest) (*PerformanceResult, error) {
	if req.RowLimit != 0 || req.StartRow != 0 {
		return nil, gscerr.New(gscerr.CodeInvalidArgument,
			"Automatic pagination cannot be combined with a row limit or start row.",
			"Drop --limit and --start-row when using --all, or drop --all to page manually.")
	}
	req.RowLimit = MaxRowLimit
	req.applyDefaults()
	if err := req.Validate(); err != nil {
		return nil, err
	}

	res := newPerformanceResult(req)
	var prev []Row
	for page := 1; ; page++ {
		pageReq := req
		pageReq.StartRow = len(res.Rows)
		resp, err := c.queryPage(ctx, pageReq)
		if err != nil {
			ge := gscerr.From(err)
			return nil, &gscerr.Error{
				Code:      ge.Code,
				Message:   fmt.Sprintf("%s (while fetching page %d after %d rows)", ge.Message, page, len(res.Rows)),
				Action:    ge.Action,
				Retryable: ge.Retryable,
				Cause:     err,
			}
		}
		rows := normalizeRows(req.Dimensions, resp)
		res.PagesFetched = page
		if page == 1 {
			res.ResponseAggregationType = resp.ResponseAggregationType
			res.FirstIncompleteDate = resp.Metadata.FirstIncompleteDate
			res.FirstIncompleteHour = resp.Metadata.FirstIncompleteHour
		}
		if len(rows) > 0 && len(prev) > 0 && len(rows) == len(prev) && rows[0].sameAs(prev[0]) && rows[len(rows)-1].sameAs(prev[len(prev)-1]) {
			// Google returned the previous page again; do not append duplicates.
			res.PaginationStopReason = StopRepeatedPage
			return res, nil
		}
		res.Rows = append(res.Rows, rows...)
		if len(rows) < MaxRowLimit {
			res.PaginationExhausted = true
			return res, nil
		}
		if page >= c.maxAutoPages() {
			res.PaginationStopReason = StopSafetyCap
			return res, nil
		}
		prev = rows
	}
}

func (c *Client) queryPage(ctx context.Context, req PerformanceRequest) (*searchAnalyticsResponse, error) {
	endpoint := c.BaseURL + "/sites/" + url.PathEscape(req.Site) + "/searchAnalytics/query"
	var resp searchAnalyticsResponse
	if err := c.do(ctx, "POST", endpoint, req.wire(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func newPerformanceResult(req PerformanceRequest) *PerformanceResult {
	dims := req.Dimensions
	if dims == nil {
		dims = []string{}
	}
	filters := req.Filters
	if filters == nil {
		filters = []DimensionFilter{}
	}
	return &PerformanceResult{
		Site:            req.Site,
		StartDate:       req.StartDate,
		EndDate:         req.EndDate,
		Dimensions:      dims,
		SearchType:      req.SearchType,
		DataState:       req.DataState,
		Filters:         filters,
		AggregationType: req.AggregationType,
		RowLimit:        req.RowLimit,
		StartRow:        req.StartRow,
		Rows:            []Row{},
	}
}

func normalizeRows(dims []string, resp *searchAnalyticsResponse) []Row {
	if dims == nil {
		dims = []string{}
	}
	rows := make([]Row, 0, len(resp.Rows))
	for _, r := range resp.Rows {
		keys := make(map[string]string, len(dims))
		for i, d := range dims {
			if i < len(r.Keys) {
				keys[d] = r.Keys[i]
			}
		}
		rows = append(rows, Row{
			Dimensions:  dims,
			Keys:        keys,
			Clicks:      r.Clicks,
			Impressions: r.Impressions,
			CTR:         r.CTR,
			Position:    r.Position,
		})
	}
	return rows
}

// DateRangeForDays computes the inclusive [start, end] window of n days in
// Search Console's fixed Pacific time zone, ending yesterday (PT). Yesterday
// is chosen because Search Analytics dates are PT calendar days and today's
// data is never final.
func DateRangeForDays(now time.Time, days int) (start, end string, err error) {
	if days < 1 {
		return "", "", gscerr.New(gscerr.CodeInvalidDateRange, "Days must be at least 1.", "Pass --days with a positive integer.")
	}
	loc, lerr := time.LoadLocation(Timezone)
	if lerr != nil {
		return "", "", gscerr.Wrap(lerr, gscerr.CodeInternal, "Could not load the America/Los_Angeles time zone.", "")
	}
	pt := now.In(loc)
	endDay := time.Date(pt.Year(), pt.Month(), pt.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
	startDay := endDay.AddDate(0, 0, -(days - 1))
	return startDay.Format(DateFormat), endDay.Format(DateFormat), nil
}

// TodayPT returns today's date in Search Console's Pacific time zone.
func TodayPT(now time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(Timezone)
	if err != nil {
		return time.Time{}, gscerr.Wrap(err, gscerr.CodeInternal, "Could not load the America/Los_Angeles time zone.", "")
	}
	pt := now.In(loc)
	return time.Date(pt.Year(), pt.Month(), pt.Day(), 0, 0, 0, 0, loc), nil
}

func (c *Client) maxAutoPages() int {
	if c.MaxAutoPages > 0 {
		return c.MaxAutoPages
	}
	return DefaultMaxAutoPages
}

// ObservedDateRange returns the earliest and latest "date" dimension values
// present in rows. ok is false when rows carry no date dimension or are
// empty. It describes returned rows only: a date absent from the result may
// have had no activity or been dropped by Google's top-rows behavior, so it
// must not be read as a data-availability range. It never triggers extra
// requests.
func ObservedDateRange(rows []Row) (first, last string, ok bool) {
	for _, r := range rows {
		d, has := r.Keys["date"]
		if !has || d == "" {
			continue
		}
		if !ok || d < first {
			first = d
		}
		if !ok || d > last {
			last = d
		}
		ok = true
	}
	return first, last, ok
}

// WindowDays returns the inclusive length in days of a YYYY-MM-DD window.
func WindowDays(start, end string) (int, error) {
	s, err := time.Parse(DateFormat, start)
	if err != nil {
		return 0, gscerr.New(gscerr.CodeInvalidDateRange, fmt.Sprintf("Start date %q is not YYYY-MM-DD.", start), "")
	}
	e, err := time.Parse(DateFormat, end)
	if err != nil {
		return 0, gscerr.New(gscerr.CodeInvalidDateRange, fmt.Sprintf("End date %q is not YYYY-MM-DD.", end), "")
	}
	if e.Before(s) {
		return 0, gscerr.New(gscerr.CodeInvalidDateRange, fmt.Sprintf("End date %s is before start date %s.", end, start), "")
	}
	return int(e.Sub(s).Hours()/24) + 1, nil
}

// PreviousWindow returns the window of equal length that ends the day
// before start: for Sep 3-9 it returns Aug 27-Sep 2. The windows never overlap.
func PreviousWindow(start, end string) (prevStart, prevEnd string, err error) {
	days, err := WindowDays(start, end)
	if err != nil {
		return "", "", err
	}
	s, _ := time.Parse(DateFormat, start)
	pe := s.AddDate(0, 0, -1)
	ps := pe.AddDate(0, 0, -(days - 1))
	return ps.Format(DateFormat), pe.Format(DateFormat), nil
}
