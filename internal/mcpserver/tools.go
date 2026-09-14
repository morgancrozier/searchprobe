package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
	"github.com/morgancrozier/searchprobe/internal/output"
	"github.com/morgancrozier/searchprobe/internal/searchconsole"
)

const (
	defaultDays       = 28
	maxInspectionURLs = 2000
	deltaSemantics    = "current minus previous; for position, lower is better, so a negative delta means the average rank improved"
	finalDataLagDays  = 3
)

// Result is the structured result shared by every SearchProbe MCP tool.
// Successful results contain data, meta, and a warnings array. Tool failures
// contain a normalized SearchProbe error and set the MCP isError flag.
type Result struct {
	OK       bool             `json:"ok"`
	Data     any              `json:"data,omitempty"`
	Meta     any              `json:"meta,omitempty"`
	Warnings []output.Warning `json:"warnings"`
	Error    *gscerr.Error    `json:"error,omitempty"`
}

type SitesInput struct {
	Match string `json:"match,omitempty" jsonschema:"Optional case-insensitive substring used to filter canonical property identifiers."`
}

type FilterInput struct {
	Dimension  string `json:"dimension" jsonschema:"Filter dimension: query, page, country, device, or searchAppearance."`
	Operator   string `json:"operator" jsonschema:"Filter operator: contains, equals, notContains, notEquals, includingRegex, or excludingRegex."`
	Expression string `json:"expression" jsonschema:"Value or regular expression to match. All filters are ANDed."`
}

type PerformanceInput struct {
	Site            string        `json:"site" jsonschema:"Canonical Search Console property identifier exactly as returned by the sites tool."`
	Days            *int          `json:"days,omitempty" jsonschema:"Pacific Time days ending yesterday. Defaults to 28. Do not combine with startDate and endDate."`
	StartDate       string        `json:"startDate,omitempty" jsonschema:"Explicit start date in YYYY-MM-DD Pacific Time. Requires endDate and excludes days."`
	EndDate         string        `json:"endDate,omitempty" jsonschema:"Explicit end date in YYYY-MM-DD Pacific Time. Requires startDate and excludes days."`
	Dimensions      []string      `json:"dimensions,omitempty" jsonschema:"Breakdown dimensions. Omit for the default [query]; pass an empty array for property totals. Values: query, page, country, device, date, searchAppearance, hour."`
	SearchType      string        `json:"searchType,omitempty" jsonschema:"Search type. Defaults to web. Values: web, image, video, news, discover, googleNews."`
	DataState       string        `json:"dataState,omitempty" jsonschema:"Data freshness. Defaults to final. Values: final, all, hourly_all. The hour dimension requires hourly_all."`
	Filters         []FilterInput `json:"filters,omitempty" jsonschema:"Optional dimension filters. Every filter must match."`
	AggregationType string        `json:"aggregationType,omitempty" jsonschema:"Optional aggregation: auto, byPage, byProperty, or byNewsShowcasePanel."`
	Limit           *int          `json:"limit,omitempty" jsonschema:"Maximum rows for one request, from 1 to 25000. Defaults to 1000. Excludes all."`
	StartRow        *int          `json:"startRow,omitempty" jsonschema:"Zero-based row offset for manual pagination. Defaults to 0. Excludes all."`
	All             bool          `json:"all,omitempty" jsonschema:"Fetch every page the API exposes. This still does not prove source completeness. Excludes limit and startRow."`
}

type CompareInput struct {
	Site             string        `json:"site" jsonschema:"Canonical Search Console property identifier exactly as returned by the sites tool."`
	Days             *int          `json:"days,omitempty" jsonschema:"Current Pacific Time window ending yesterday. Defaults to 28. Do not combine with startDate and endDate."`
	StartDate        string        `json:"startDate,omitempty" jsonschema:"Explicit current-window start date in YYYY-MM-DD Pacific Time. Requires endDate and excludes days."`
	EndDate          string        `json:"endDate,omitempty" jsonschema:"Explicit current-window end date in YYYY-MM-DD Pacific Time. Requires startDate and excludes days."`
	Dimensions       []string      `json:"dimensions,omitempty" jsonschema:"Breakdown dimensions. Omit for the default [query]; pass an empty array for property totals."`
	SearchType       string        `json:"searchType,omitempty" jsonschema:"Search type. Defaults to web."`
	DataState        string        `json:"dataState,omitempty" jsonschema:"Data freshness. Defaults to final. Values: final, all, hourly_all."`
	Filters          []FilterInput `json:"filters,omitempty" jsonschema:"Optional filters applied identically to both windows. Every filter must match."`
	AggregationType  string        `json:"aggregationType,omitempty" jsonschema:"Optional aggregation: auto, byPage, byProperty, or byNewsShowcasePanel."`
	Previous         bool          `json:"previous,omitempty" jsonschema:"Compare with the immediately preceding equal-length window. Excludes compareStartDate and compareEndDate."`
	CompareStartDate string        `json:"compareStartDate,omitempty" jsonschema:"Explicit comparison-window start date in YYYY-MM-DD Pacific Time. Requires compareEndDate and excludes previous."`
	CompareEndDate   string        `json:"compareEndDate,omitempty" jsonschema:"Explicit comparison-window end date in YYYY-MM-DD Pacific Time. Requires compareStartDate and excludes previous."`
	Limit            *int          `json:"limit,omitempty" jsonschema:"Maximum joined rows returned after sorting. Defaults to 1000; zero returns all joined rows."`
	Sort             string        `json:"sort,omitempty" jsonschema:"Sort key. Defaults to current-clicks. Values: current-clicks, current-impressions, clicks-delta, impressions-delta, position-delta."`
	Ascending        bool          `json:"ascending,omitempty" jsonschema:"Sort ascending so losses or negative deltas appear first."`
}

type InspectInput struct {
	Site string   `json:"site" jsonschema:"Canonical Search Console property containing every URL."`
	URLs []string `json:"urls" jsonschema:"One or more fully-qualified URLs to inspect, up to 2000. Duplicates are inspected once."`
}

type SitemapsInput struct {
	Site         string `json:"site" jsonschema:"Canonical Search Console property identifier exactly as returned by the sites tool."`
	SitemapIndex string `json:"sitemapIndex,omitempty" jsonschema:"Optional sitemap index URL used to list only its child sitemaps."`
}

type SitemapInput struct {
	Site       string `json:"site" jsonschema:"Canonical Search Console property identifier exactly as returned by the sites tool."`
	SitemapURL string `json:"sitemapUrl" jsonschema:"Submitted sitemap URL exactly as returned by the sitemaps tool."`
}

func registerTools(server *mcp.Server, opts Options) {
	annotations := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	mcp.AddTool(server, &mcp.Tool{
		Name: "sites", Description: "List Google Search Console properties accessible to the authenticated user. Use this before other tools when the correct property is unknown.", Annotations: annotations,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SitesInput) (*mcp.CallToolResult, Result, error) {
		client, fail := authenticatedClient(ctx, opts.Client)
		if fail != nil {
			return failResult(fail)
		}
		sites, err := client.ListSites(ctx)
		if err != nil {
			return failResult(err)
		}
		filtered := filterSites(sites, in.Match)
		meta := map[string]any{"count": len(filtered), "total": len(sites)}
		if strings.TrimSpace(in.Match) != "" {
			meta["match"] = in.Match
		}
		return success(map[string]any{"sites": filtered}, meta, nil)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "performance", Description: "Query Search Console search analytics for clicks, impressions, CTR, and average position. Use it for totals or breakdowns by query, page, date, device, country, appearance, or hour.", Annotations: annotations,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in PerformanceInput) (*mcp.CallToolResult, Result, error) {
		req, dateMode, days, err := buildPerformanceRequest(in, opts.Now())
		if err != nil {
			return failResult(err)
		}
		client, err := opts.Client(ctx)
		if err != nil {
			return failResult(err)
		}
		var res *searchconsole.PerformanceResult
		if in.All {
			res, err = client.QueryPerformanceAll(ctx, req)
		} else {
			res, err = client.QueryPerformance(ctx, req)
		}
		if err != nil {
			return failResult(err)
		}
		clicks, impressions := res.Totals()
		data := map[string]any{
			"rows":           res.Rows,
			"returnedTotals": map[string]any{"rows": len(res.Rows), "clicks": clicks, "impressions": impressions},
		}
		return success(data, performanceMeta(res, dateMode, days, in.All), performanceWarnings(res, in.All, opts.Now()))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "compare", Description: "Compare equivalent Search Console periods and return deterministic metric deltas. Use it to find gains, losses, and ranking changes between two non-overlapping windows.", Annotations: annotations,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CompareInput) (*mcp.CallToolResult, Result, error) {
		req, dateMode, previousMode, days, limit, sortBy, err := buildCompareRequest(in, opts.Now())
		if err != nil {
			return failResult(err)
		}
		client, err := opts.Client(ctx)
		if err != nil {
			return failResult(err)
		}
		res, err := client.ComparePerformance(ctx, req)
		if err != nil {
			return failResult(err)
		}
		rows := res.Rows
		searchconsole.SortRows(rows, res.Dimensions, sortBy, in.Ascending)
		if limit > 0 && len(rows) > limit {
			rows = rows[:limit]
		}
		data := map[string]any{
			"rows":           rows,
			"returnedTotals": map[string]any{"current": res.CurrentTotals, "previous": res.PrevTotals, "delta": res.TotalsDelta},
		}
		meta := compareMeta(res, dateMode, previousMode, days, len(rows), limit, sortBy, in.Ascending)
		return success(data, meta, compareWarnings(res, opts.Now()))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "inspect", Description: "Inspect Google's indexed version of one or more URLs, including coverage, crawl, canonical, rich-result, mobile, and AMP information. Use it for indexing diagnostics; it cannot run a live test or request indexing.", Annotations: annotations,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in InspectInput) (*mcp.CallToolResult, Result, error) {
		return runInspect(ctx, opts.Client, in)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "sitemaps", Description: "List submitted sitemaps for a Search Console property. Use it to review Google's recorded sitemap processing state, submitted URL counts, errors, and warnings.", Annotations: annotations,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SitemapsInput) (*mcp.CallToolResult, Result, error) {
		if strings.TrimSpace(in.Site) == "" {
			return failResult(gscerr.New(gscerr.CodeInvalidArgument, "A Search Console property is required.", "Use the sites tool and pass one canonical property identifier."))
		}
		client, err := opts.Client(ctx)
		if err != nil {
			return failResult(err)
		}
		sitemaps, err := client.ListSitemaps(ctx, in.Site, in.SitemapIndex)
		if err != nil {
			return failResult(err)
		}
		meta := map[string]any{"site": in.Site, "count": len(sitemaps), "readOnly": true, "submittedCountsOnly": true}
		if in.SitemapIndex != "" {
			meta["sitemapIndex"] = in.SitemapIndex
		}
		return success(map[string]any{"sitemaps": sitemaps}, meta, nil)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "sitemap", Description: "Get Google's record for one submitted sitemap. Use it after sitemaps when you need detailed processing times, content counts, errors, or warnings.", Annotations: annotations,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SitemapInput) (*mcp.CallToolResult, Result, error) {
		if strings.TrimSpace(in.Site) == "" || strings.TrimSpace(in.SitemapURL) == "" {
			return failResult(gscerr.New(gscerr.CodeInvalidArgument, "A Search Console property and sitemap URL are required.", "Use the sitemaps tool and pass its site and sitemap URL exactly."))
		}
		client, err := opts.Client(ctx)
		if err != nil {
			return failResult(err)
		}
		sitemap, err := client.GetSitemap(ctx, in.Site, in.SitemapURL)
		if err != nil {
			return failResult(err)
		}
		meta := map[string]any{"site": in.Site, "sitemap": sitemap.Path, "readOnly": true, "submittedCountsOnly": true}
		return success(sitemap, meta, nil)
	})
}

func authenticatedClient(ctx context.Context, factory ClientFactory) (Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return factory(ctx)
}

func success(data, meta any, warnings []output.Warning) (*mcp.CallToolResult, Result, error) {
	if warnings == nil {
		warnings = []output.Warning{}
	}
	return &mcp.CallToolResult{}, Result{OK: true, Data: data, Meta: meta, Warnings: warnings}, nil
}

func failResult(err error) (*mcp.CallToolResult, Result, error) {
	ge := gscerr.From(err)
	clean := &gscerr.Error{Code: ge.Code, Message: ge.Message, Action: ge.Action, Retryable: ge.Retryable}
	switch clean.Code {
	case gscerr.CodeAuthRequired, gscerr.CodeAuthRevoked, gscerr.CodeAuthScopeInsufficient, gscerr.CodeAuthFailed:
		clean.Action = "Run `gsc setup` in a terminal, then retry the tool."
	}
	return &mcp.CallToolResult{IsError: true}, Result{OK: false, Warnings: []output.Warning{}, Error: clean}, nil
}

func filterSites(sites []searchconsole.Site, match string) []searchconsole.Site {
	out := make([]searchconsole.Site, 0, len(sites))
	needle := strings.ToLower(strings.TrimSpace(match))
	for _, site := range sites {
		if needle == "" || strings.Contains(strings.ToLower(site.SiteURL), needle) {
			out = append(out, site)
		}
	}
	return out
}

func buildPerformanceRequest(in PerformanceInput, now time.Time) (searchconsole.PerformanceRequest, string, int, error) {
	var req searchconsole.PerformanceRequest
	dimensions := in.Dimensions
	if dimensions == nil {
		dimensions = []string{"query"}
	}
	dims, err := searchconsole.ParseDimensions(strings.Join(dimensions, ","))
	if err != nil {
		return req, "", 0, err
	}
	searchType, err := searchconsole.ParseSearchType(defaultString(in.SearchType, searchconsole.SearchTypeWeb))
	if err != nil {
		return req, "", 0, err
	}
	dataState, err := searchconsole.ParseDataState(defaultString(in.DataState, searchconsole.DataStateFinal))
	if err != nil {
		return req, "", 0, err
	}
	aggregation, err := searchconsole.ParseAggregationType(in.AggregationType)
	if err != nil {
		return req, "", 0, err
	}
	filters := make([]searchconsole.DimensionFilter, 0, len(in.Filters))
	for _, filter := range in.Filters {
		parsed, err := searchconsole.ParseFilter(strings.Join([]string{filter.Dimension, filter.Operator, filter.Expression}, " "))
		if err != nil {
			return req, "", 0, err
		}
		filters = append(filters, parsed)
	}

	days := defaultDays
	if in.Days != nil {
		days = *in.Days
	}
	dateMode := "days"
	start, end := in.StartDate, in.EndDate
	if start != "" || end != "" {
		if start == "" || end == "" {
			return req, "", 0, gscerr.New(gscerr.CodeInvalidDateRange, "startDate and endDate must be given together.", "Pass both dates, or use days.")
		}
		if in.Days != nil {
			return req, "", 0, gscerr.New(gscerr.CodeInvalidDateRange, "days cannot be combined with startDate and endDate.", "Use either days or an explicit date range.")
		}
		dateMode = "explicit"
	} else {
		start, end, err = searchconsole.DateRangeForDays(now, days)
		if err != nil {
			return req, "", 0, err
		}
	}
	if in.All && (in.Limit != nil || in.StartRow != nil) {
		return req, "", 0, gscerr.New(gscerr.CodeInvalidArgument, "all cannot be combined with limit or startRow.", "Drop limit and startRow, or set all to false.")
	}
	if !in.All {
		if in.Limit == nil {
			req.RowLimit = searchconsole.DefaultRowLimit
		} else {
			req.RowLimit = *in.Limit
		}
		if in.StartRow != nil {
			req.StartRow = *in.StartRow
		}
	}
	req.Site = strings.TrimSpace(in.Site)
	req.StartDate, req.EndDate = start, end
	req.Dimensions = dims
	req.SearchType = searchType
	req.DataState = dataState
	req.Filters = filters
	req.AggregationType = aggregation
	if err := req.Validate(); err != nil {
		return req, "", 0, err
	}
	return req, dateMode, days, nil
}

func buildCompareRequest(in CompareInput, now time.Time) (searchconsole.CompareRequest, string, string, int, int, string, error) {
	p := PerformanceInput{
		Site: in.Site, Days: in.Days, StartDate: in.StartDate, EndDate: in.EndDate,
		Dimensions: in.Dimensions, SearchType: in.SearchType, DataState: in.DataState,
		Filters: in.Filters, AggregationType: in.AggregationType, All: true,
	}
	base, dateMode, days, err := buildPerformanceRequest(p, now)
	if err != nil {
		return searchconsole.CompareRequest{}, "", "", 0, 0, "", err
	}
	req := searchconsole.CompareRequest{Base: base}
	previousMode := ""
	switch {
	case in.Previous && (in.CompareStartDate != "" || in.CompareEndDate != ""):
		err = gscerr.New(gscerr.CodeInvalidDateRange, "previous cannot be combined with an explicit comparison window.", "Use either previous or compareStartDate and compareEndDate.")
	case in.Previous:
		req.PreviousStartDate, req.PreviousEndDate, err = searchconsole.PreviousWindow(base.StartDate, base.EndDate)
		previousMode = "previous"
	case in.CompareStartDate != "" && in.CompareEndDate != "":
		req.PreviousStartDate, req.PreviousEndDate = in.CompareStartDate, in.CompareEndDate
		previousMode = "explicit"
	case in.CompareStartDate != "" || in.CompareEndDate != "":
		err = gscerr.New(gscerr.CodeInvalidDateRange, "compareStartDate and compareEndDate must be given together.", "Pass both dates, or set previous to true.")
	default:
		err = gscerr.New(gscerr.CodeInvalidDateRange, "A comparison window is required.", "Set previous to true, or pass compareStartDate and compareEndDate.")
	}
	if err != nil {
		return req, "", "", 0, 0, "", err
	}
	limit := searchconsole.DefaultRowLimit
	if in.Limit != nil {
		limit = *in.Limit
	}
	if limit < 0 {
		return req, "", "", 0, 0, "", gscerr.New(gscerr.CodeInvalidArgument, "limit must be zero or positive.", "Use zero for all joined rows or a positive limit.")
	}
	sortBy, err := searchconsole.ParseSortKey(in.Sort)
	if err != nil {
		return req, "", "", 0, 0, "", err
	}
	if err := req.Validate(); err != nil {
		return req, "", "", 0, 0, "", err
	}
	return req, dateMode, previousMode, days, limit, sortBy, nil
}

type inspectionItem struct {
	URL        string                          `json:"url"`
	OK         bool                            `json:"ok"`
	Inspection *searchconsole.InspectionResult `json:"inspection,omitempty"`
	Error      *gscerr.Error                   `json:"error,omitempty"`
}

func runInspect(ctx context.Context, factory ClientFactory, in InspectInput) (*mcp.CallToolResult, Result, error) {
	if len(in.URLs) == 0 {
		return failResult(gscerr.New(gscerr.CodeInvalidArgument, "At least one URL is required.", "Pass one or more fully-qualified URLs in urls."))
	}
	unique := make([]string, 0, len(in.URLs))
	seen := map[string]bool{}
	for _, raw := range in.URLs {
		u := strings.TrimSpace(raw)
		if !seen[u] {
			seen[u] = true
			unique = append(unique, u)
		}
	}
	if len(unique) > maxInspectionURLs {
		return failResult(gscerr.New(gscerr.CodeInvalidArgument, fmt.Sprintf("%d URLs exceed the inspection limit of %d.", len(unique), maxInspectionURLs), "Split the request into smaller batches."))
	}
	if len(unique) == 1 {
		req := searchconsole.InspectionRequest{Site: in.Site, InspectionURL: unique[0]}
		if err := req.Validate(); err != nil {
			return failResult(err)
		}
		client, err := factory(ctx)
		if err != nil {
			return failResult(err)
		}
		inspection, err := client.InspectURL(ctx, req)
		if err != nil {
			return failResult(err)
		}
		meta := map[string]any{
			"site": in.Site, "inspectionType": "indexedVersion", "liveTest": false,
			"inspectionUrl": inspection.InspectionURL,
		}
		return success(inspection, meta, nil)
	}
	client, err := factory(ctx)
	if err != nil {
		return failResult(err)
	}
	items := make([]inspectionItem, 0, len(unique))
	succeeded := 0
	for i, url := range unique {
		if err := ctx.Err(); err != nil {
			return failResult(gscerr.Wrap(err, gscerr.CodeNetworkError, fmt.Sprintf("Inspection cancelled after %d of %d URLs.", i, len(unique)), "Retry the tool."))
		}
		req := searchconsole.InspectionRequest{Site: in.Site, InspectionURL: url}
		res, err := client.InspectURL(ctx, req)
		if err != nil {
			ge := gscerr.From(err)
			if abortInspection(ge.Code) {
				return failResult(&gscerr.Error{Code: ge.Code, Message: fmt.Sprintf("%s (inspection stopped at URL %d of %d: %s)", ge.Message, i+1, len(unique), url), Action: ge.Action, Retryable: ge.Retryable, Cause: err})
			}
			items = append(items, inspectionItem{URL: url, OK: false, Error: &gscerr.Error{Code: ge.Code, Message: ge.Message, Action: ge.Action, Retryable: ge.Retryable}})
			continue
		}
		succeeded++
		items = append(items, inspectionItem{URL: url, OK: true, Inspection: res})
	}
	meta := map[string]any{
		"site": in.Site, "inspectionType": "indexedVersion", "liveTest": false,
		"requested": len(in.URLs), "duplicatesSkipped": len(in.URLs) - len(unique),
		"inspected": len(unique), "succeeded": succeeded, "failed": len(unique) - succeeded,
	}
	return success(map[string]any{"results": items}, meta, nil)
}

func abortInspection(code string) bool {
	switch code {
	case gscerr.CodeAuthRequired, gscerr.CodeAuthRevoked, gscerr.CodeAuthScopeInsufficient,
		gscerr.CodeAuthFailed, gscerr.CodeQuotaExceeded, gscerr.CodeRateLimited, gscerr.CodeNetworkError:
		return true
	default:
		return false
	}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func performanceMeta(res *searchconsole.PerformanceResult, dateMode string, days int, all bool) map[string]any {
	meta := map[string]any{
		"site": res.Site, "dateMode": dateMode, "startDate": res.StartDate, "endDate": res.EndDate,
		"timezone": searchconsole.Timezone, "searchType": res.SearchType, "dataState": res.DataState,
		"dimensions": res.Dimensions, "filters": res.Filters, "filterLogic": "and", "all": all,
		"rowLimit": res.RowLimit, "startRow": res.StartRow, "rowCount": len(res.Rows),
		"pagesFetched": res.PagesFetched, "paginationExhausted": res.PaginationExhausted, "sourceMayBePartial": true,
	}
	if dateMode == "days" {
		meta["days"] = days
	}
	if first, last, ok := searchconsole.ObservedDateRange(res.Rows); ok {
		meta["firstObservedDate"], meta["lastObservedDate"] = first, last
	}
	if res.AggregationType != "" {
		meta["requestedAggregationType"] = res.AggregationType
	}
	if res.ResponseAggregationType != "" {
		meta["aggregationType"] = res.ResponseAggregationType
	}
	if res.FirstIncompleteDate != "" {
		meta["firstIncompleteDate"] = res.FirstIncompleteDate
	}
	if res.FirstIncompleteHour != "" {
		meta["firstIncompleteHour"] = res.FirstIncompleteHour
	}
	if res.PaginationStopReason != "" {
		meta["paginationStopReason"] = res.PaginationStopReason
	}
	return meta
}

func performanceWarnings(res *searchconsole.PerformanceResult, all bool, now time.Time) []output.Warning {
	var warnings []output.Warning
	if all {
		warnings = append(warnings, output.Warning{Code: "TOP_ROWS_ONLY", Message: "Every page of this API query was fetched, but Search Analytics returns top rows only; Google does not guarantee every underlying row is exposed."})
	}
	windowDays, _ := searchconsole.WindowDays(res.StartDate, res.EndDate)
	if res.DataState == searchconsole.DataStateFinal {
		if windowDays <= 7 && endsWithinDays(res.EndDate, now, finalDataLagDays) {
			warnings = append(warnings, output.Warning{Code: "RECENT_DAYS_MAY_BE_EXCLUDED", Message: "Only finalized data is requested and this short window ends within the last few days; Google typically finalizes data two or three days after the fact, so the most recent days may not be included yet."})
		}
	} else {
		warnings = append(warnings, output.Warning{Code: "PRELIMINARY_DATA", Message: fmt.Sprintf("Data state %s includes fresh data that Google may still revise; do not compare it directly with finalized results.", res.DataState)})
	}
	if res.FirstIncompleteDate != "" {
		warnings = append(warnings, output.Warning{Code: "INCOMPLETE_DATA", Message: "Data from " + res.FirstIncompleteDate + " onward is still being collected and may change."})
	}
	if res.FirstIncompleteHour != "" {
		warnings = append(warnings, output.Warning{Code: "INCOMPLETE_DATA", Message: "Data from " + res.FirstIncompleteHour + " onward is still being collected and may change."})
	}
	if !all && !res.PaginationExhausted {
		warnings = append(warnings, output.Warning{Code: "ROW_LIMIT_REACHED", Message: fmt.Sprintf("Returned rows hit the requested limit of %d; more rows may be available with startRow or all.", res.RowLimit)})
	}
	switch res.PaginationStopReason {
	case searchconsole.StopSafetyCap:
		warnings = append(warnings, output.Warning{Code: "PAGINATION_STOPPED", Message: fmt.Sprintf("Stopped at the %d-page safety cap (%d rows) without reaching the end of the API query; the result is incomplete. Narrow the query with filters or a shorter date range and report it if it recurs.", res.PagesFetched, len(res.Rows))})
	case searchconsole.StopRepeatedPage:
		warnings = append(warnings, output.Warning{Code: "PAGINATION_STOPPED", Message: "Google returned the same page twice; pagination stopped early and the result may be truncated."})
	}
	return warnings
}

func endsWithinDays(endDate string, now time.Time, days int) bool {
	end, err := time.Parse(searchconsole.DateFormat, endDate)
	if err != nil {
		return true
	}
	today, err := searchconsole.TodayPT(now)
	if err != nil {
		return true
	}
	cutoff := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -days)
	return !end.Before(cutoff)
}

func compareMeta(res *searchconsole.CompareResult, dateMode, previousMode string, days, emitted, limit int, sortBy string, ascending bool) map[string]any {
	direction := "desc"
	if ascending {
		direction = "asc"
	}
	sortedBy := sortBy + " " + direction + ", then " + searchconsole.TieBreakOrder
	if sortBy == searchconsole.SortCurrentClicks && !ascending {
		sortedBy = searchconsole.SortOrder
	}
	meta := map[string]any{
		"site": res.Site, "timezone": searchconsole.Timezone, "dateMode": dateMode, "previousMode": previousMode,
		"current": windowMeta(res.Current), "previous": windowMeta(res.Previous), "searchType": res.SearchType,
		"dataState": res.DataState, "dimensions": res.Dimensions, "filters": res.Filters, "filterLogic": "and",
		"rowCount": emitted, "joinedRows": len(res.Rows), "limit": limit,
		"sort": map[string]any{"key": sortBy, "direction": direction}, "sortedBy": sortedBy,
		"deltaSemantics": deltaSemantics, "paginationExhausted": true, "sourceMayBePartial": true,
	}
	if dateMode == "days" {
		meta["days"] = days
	}
	if res.AggregationType != "" {
		meta["requestedAggregationType"] = res.AggregationType
	}
	if res.Current.ResponseAggregationType != "" {
		meta["aggregationType"] = res.Current.ResponseAggregationType
	}
	return meta
}

func windowMeta(window searchconsole.Window) map[string]any {
	meta := map[string]any{"startDate": window.StartDate, "endDate": window.EndDate, "days": window.Days, "rowCount": window.RowCount, "pagesFetched": window.PagesFetched}
	if window.FirstObservedDate != "" {
		meta["firstObservedDate"], meta["lastObservedDate"] = window.FirstObservedDate, window.LastObservedDate
	}
	if window.FirstIncompleteDate != "" {
		meta["firstIncompleteDate"] = window.FirstIncompleteDate
	}
	if window.FirstIncompleteHour != "" {
		meta["firstIncompleteHour"] = window.FirstIncompleteHour
	}
	return meta
}

func compareWarnings(res *searchconsole.CompareResult, now time.Time) []output.Warning {
	seen := map[string]bool{}
	var warnings []output.Warning
	add := func(w output.Warning) {
		if !seen[w.Code] {
			seen[w.Code] = true
			warnings = append(warnings, w)
		}
	}
	for _, pair := range []struct {
		window searchconsole.Window
		label  string
	}{{res.Current, "current"}, {res.Previous, "previous"}} {
		partial := &searchconsole.PerformanceResult{
			StartDate: pair.window.StartDate, EndDate: pair.window.EndDate, DataState: res.DataState,
			FirstIncompleteDate: pair.window.FirstIncompleteDate, FirstIncompleteHour: pair.window.FirstIncompleteHour,
			PaginationExhausted: true,
		}
		for _, warning := range performanceWarnings(partial, true, now) {
			if warning.Code == "RECENT_DAYS_MAY_BE_EXCLUDED" {
				warning.Message = "The " + pair.label + " window may be missing its final days (finalized data lags two or three days); deltas can understate the " + pair.label + " period. " + warning.Message
			}
			add(warning)
		}
	}
	if res.Current.Days != res.Previous.Days {
		add(output.Warning{Code: "UNEQUAL_WINDOWS", Message: fmt.Sprintf("The current window is %d day(s) and the previous window is %d day(s); absolute deltas are not like-for-like.", res.Current.Days, res.Previous.Days)})
	}
	return warnings
}
