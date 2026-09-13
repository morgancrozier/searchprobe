package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
	"github.com/morgancrozier/searchprobe/internal/output"
	"github.com/morgancrozier/searchprobe/internal/searchconsole"
)

// performanceFlags holds parsed CLI input for gsc performance.
type performanceFlags struct {
	site        string
	days        int
	start, end  string
	dimensions  string
	searchType  string
	dataState   string
	filters     []string
	aggregation string
	limit       int
	startRow    int
	all         bool
}

func newPerformanceCmd(a *app) *cobra.Command {
	var f performanceFlags
	cmd := &cobra.Command{
		Use:   "performance",
		Short: "Query Search Analytics (clicks, impressions, CTR, position)",
		Long: `Query clicks, impressions, CTR and position for a property from gsc sites.
Use --json for structured output, including meta and warnings.

Dates are Pacific Time: --days N ends yesterday; --start/--end selects exact
calendar dates. final data (default) may omit recent days. all includes fresh,
revisable data; hourly_all is required for the hour dimension.

Use --dimensions query or page for breakdowns; --dimensions "" for aggregates.
Summing dimension rows may not reproduce aggregates: Google returns top rows
and omits anonymized queries from query rows. --all paginates exposed rows;
pagination completion never proves source completeness.

--limit caps one request (max 25000); --start-row offsets it. Neither combines
with --all. --filter "<dimension> <operator> <expression>" is repeatable (AND).
Dimensions: ` + searchconsole.FilterDimensionList + `.
Operators: ` + searchconsole.FilterOperatorList + `.

meta dates describe the requested window. With the date dimension, observed
dates describe returned rows only; missing dates do not prove missing activity.`,
		Example: `  gsc performance --site sc-domain:example.com --days 28 --dimensions query --json
  gsc performance --site sc-domain:example.com --days 28 --dimensions "" --json
  gsc performance --site sc-domain:example.com --start 2026-08-01 --end 2026-08-31 --dimensions page --all --json
  gsc performance --site sc-domain:example.com --dimensions query --filter "page contains /blog/" --filter "query notContains brand" --json
  gsc performance --site sc-domain:example.com --type image --dimensions page --limit 50
  gsc performance --site sc-domain:example.com --days 3 --data-state hourly_all --dimensions hour --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			req, dateMode, err := buildPerformanceRequest(cmd, f, a.now())
			if err != nil {
				return err
			}
			client, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			var res *searchconsole.PerformanceResult
			if f.all {
				res, err = client.QueryPerformanceAll(cmd.Context(), req)
			} else {
				res, err = client.QueryPerformance(cmd.Context(), req)
			}
			if err != nil {
				return err
			}
			meta := performanceMeta(res, dateMode, f.days, f.all)
			warnings := performanceWarnings(res, f.all, a.now())
			return a.emit(newPerformanceData(res), meta, warnings, func(w io.Writer) {
				writePerformanceTable(w, res)
				fmt.Fprintf(a.stderr, "Site %s, %s to %s (%s), type %s, data state %s, %d row(s) from %d request(s).\n",
					res.Site, res.StartDate, res.EndDate, searchconsole.Timezone, res.SearchType, res.DataState, len(res.Rows), res.PagesFetched)
			})
		},
	}
	fl := cmd.Flags()
	registerQueryFlags(cmd, &f)
	fl.IntVar(&f.limit, "limit", searchconsole.DefaultRowLimit, fmt.Sprintf("maximum rows for one request (1-%d)", searchconsole.MaxRowLimit))
	fl.IntVar(&f.startRow, "start-row", 0, "zero-based row offset for manual pagination")
	fl.BoolVar(&f.all, "all", false, "page through all rows the API exposes (cannot combine with --limit/--start-row)")
	return cmd
}

// registerQueryFlags adds the Search Analytics query flags shared by
// performance and compare.
func registerQueryFlags(cmd *cobra.Command, f *performanceFlags) {
	fl := cmd.Flags()
	fl.StringVar(&f.site, "site", "", "Search Console property exactly as listed by gsc sites (required)")
	fl.IntVar(&f.days, "days", 28, "number of Pacific-Time days to include, ending yesterday")
	fl.StringVar(&f.start, "start", "", "start date YYYY-MM-DD (Pacific Time); requires --end, excludes --days")
	fl.StringVar(&f.end, "end", "", "end date YYYY-MM-DD (Pacific Time); requires --start, excludes --days")
	fl.StringVar(&f.dimensions, "dimensions", "query", "comma-separated dimensions: "+searchconsole.DimensionList+" (\"\" for property-level totals)")
	fl.StringVar(&f.searchType, "type", "web", "search type: "+searchconsole.SearchTypeList)
	fl.StringVar(&f.dataState, "data-state", "final", "data state: "+searchconsole.DataStateList)
	fl.StringArrayVar(&f.filters, "filter", nil, "dimension filter \"<dimension> <operator> <expression>\"; repeatable, all must match")
	fl.StringVar(&f.aggregation, "aggregation", "", "aggregation type: "+searchconsole.AggregationTypeList+" (default: Google chooses)")
	_ = cmd.MarkFlagRequired("site")
}

// buildPerformanceRequest turns flags into a validated core request. It
// returns the date mode ("days" or "explicit") for metadata.
func buildPerformanceRequest(cmd *cobra.Command, f performanceFlags, now time.Time) (searchconsole.PerformanceRequest, string, error) {
	var req searchconsole.PerformanceRequest
	changed := cmd.Flags().Changed

	dims, err := searchconsole.ParseDimensions(f.dimensions)
	if err != nil {
		return req, "", err
	}
	searchType, err := searchconsole.ParseSearchType(f.searchType)
	if err != nil {
		return req, "", err
	}
	dataState, err := searchconsole.ParseDataState(f.dataState)
	if err != nil {
		return req, "", err
	}
	aggregation, err := searchconsole.ParseAggregationType(f.aggregation)
	if err != nil {
		return req, "", err
	}
	filters := make([]searchconsole.DimensionFilter, 0, len(f.filters))
	for _, raw := range f.filters {
		df, err := searchconsole.ParseFilter(raw)
		if err != nil {
			return req, "", err
		}
		filters = append(filters, df)
	}

	// Date mode.
	var dateMode, start, end string
	switch {
	case f.start != "" || f.end != "":
		if f.start == "" || f.end == "" {
			return req, "", gscerr.New(gscerr.CodeInvalidDateRange, "--start and --end must be given together.", "Pass both --start YYYY-MM-DD and --end YYYY-MM-DD, or use --days.")
		}
		if changed("days") {
			return req, "", gscerr.New(gscerr.CodeInvalidDateRange, "--days cannot be combined with --start/--end.", "Use either --days N or --start/--end.")
		}
		dateMode, start, end = "explicit", f.start, f.end
	default:
		dateMode = "days"
		start, end, err = searchconsole.DateRangeForDays(now, f.days)
		if err != nil {
			return req, "", err
		}
	}

	// Pagination mode. The --limit/--start-row conflict applies only to
	// commands that expose manual pagination (performance); compare reuses
	// --limit for the number of joined rows to emit.
	if f.all {
		if cmd.Flags().Lookup("start-row") != nil && (changed("limit") || changed("start-row")) {
			return req, "", gscerr.New(gscerr.CodeInvalidArgument, "--all cannot be combined with --limit or --start-row.", "Drop --limit and --start-row to fetch every page, or drop --all to page manually.")
		}
		req.RowLimit, req.StartRow = 0, 0
	} else {
		req.RowLimit, req.StartRow = f.limit, f.startRow
	}

	req.Site = f.site
	req.StartDate, req.EndDate = start, end
	req.Dimensions = dims
	req.SearchType = searchType
	req.DataState = dataState
	req.Filters = filters
	req.AggregationType = aggregation
	// Validate before any credentials are loaded so usage errors surface
	// first; a zero RowLimit is valid here and means "auto-paginate".
	if err := req.Validate(); err != nil {
		return req, "", err
	}
	return req, dateMode, nil
}

// performanceData is the JSON "data" block. returnedTotals sums only the
// rows Google returned; it is not a property total.
type performanceData struct {
	Rows           []searchconsole.Row `json:"rows"`
	ReturnedTotals returnedTotals      `json:"returnedTotals"`
}

type returnedTotals struct {
	Rows        int     `json:"rows"`
	Clicks      float64 `json:"clicks"`
	Impressions float64 `json:"impressions"`
}

func newPerformanceData(res *searchconsole.PerformanceResult) performanceData {
	clicks, impressions := res.Totals()
	return performanceData{
		Rows:           res.Rows,
		ReturnedTotals: returnedTotals{Rows: len(res.Rows), Clicks: clicks, Impressions: impressions},
	}
}

func performanceMeta(res *searchconsole.PerformanceResult, dateMode string, days int, all bool) map[string]any {
	meta := map[string]any{
		"site":       res.Site,
		"dateMode":   dateMode,
		"startDate":  res.StartDate,
		"endDate":    res.EndDate,
		"timezone":   searchconsole.Timezone,
		"searchType": res.SearchType,
		"dataState":  res.DataState,
		"dimensions": res.Dimensions,
		// filters are ANDed: every filter must match a row (Google's only group type).
		"filters":      res.Filters,
		"filterLogic":  "and",
		"all":          all,
		"rowLimit":     res.RowLimit,
		"startRow":     res.StartRow,
		"rowCount":     len(res.Rows),
		"pagesFetched": res.PagesFetched,
		// paginationExhausted: this API query has no further pages.
		// sourceMayBePartial: Google returns top rows only, so even an
		// exhausted query does not prove the underlying data is complete.
		"paginationExhausted": res.PaginationExhausted,
		"sourceMayBePartial":  true,
	}
	if dateMode == "days" {
		meta["days"] = days
	}
	if first, last, ok := searchconsole.ObservedDateRange(res.Rows); ok {
		meta["firstObservedDate"] = first
		meta["lastObservedDate"] = last
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

// Warning policy (see docs/ARCHITECTURE.md): warnings are reserved for
// conditions that materially change how a result should be read. Routine
// caveats live in meta (sourceMayBePartial, dataState, dateMode).
//
//   - TOP_ROWS_ONLY only for fully paginated results (--all, compare), where
//     an exhausted query is most likely to be mistaken for complete data.
//   - RECENT_DAYS_MAY_BE_EXCLUDED only for data state final on a window of 7
//     days or fewer that ends within 3 days of today (PT), where two or three
//     missing days are material. It is a conservative statement about
//     Google's finalization lag, never an inference from which date rows
//     were returned; observed dates are metadata only.
//   - INCOMPLETE_DATA whenever Google itself reports a first incomplete
//     date/hour (the only explicit signal); PRELIMINARY_DATA for non-final
//     data states; ROW_LIMIT_REACHED and PAGINATION_STOPPED when a result
//     is cut short.
const finalDataLagDays = 3

func performanceWarnings(res *searchconsole.PerformanceResult, all bool, now time.Time) []output.Warning {
	var warnings []output.Warning
	if all {
		warnings = append(warnings, output.Warning{Code: "TOP_ROWS_ONLY", Message: "Every page of this API query was fetched, but Search Analytics returns top rows only; Google does not guarantee every underlying row is exposed."})
	}
	windowDays, _ := searchconsole.WindowDays(res.StartDate, res.EndDate)
	switch res.DataState {
	case searchconsole.DataStateFinal:
		if windowDays <= 7 && endsWithinDays(res.EndDate, now, finalDataLagDays) {
			warnings = append(warnings, output.Warning{Code: "RECENT_DAYS_MAY_BE_EXCLUDED", Message: "Only finalized data is requested and this short window ends within the last few days; Google typically finalizes data two or three days after the fact, so the most recent days may not be included yet."})
		}
	default:
		warnings = append(warnings, output.Warning{Code: "PRELIMINARY_DATA", Message: fmt.Sprintf("Data state %s includes fresh data that Google may still revise; do not compare it directly with finalized results.", res.DataState)})
	}
	if res.FirstIncompleteDate != "" {
		warnings = append(warnings, output.Warning{Code: "INCOMPLETE_DATA", Message: "Data from " + res.FirstIncompleteDate + " onward is still being collected and may change."})
	}
	if res.FirstIncompleteHour != "" {
		warnings = append(warnings, output.Warning{Code: "INCOMPLETE_DATA", Message: "Data from " + res.FirstIncompleteHour + " onward is still being collected and may change."})
	}
	if !all && !res.PaginationExhausted {
		warnings = append(warnings, output.Warning{Code: "ROW_LIMIT_REACHED", Message: fmt.Sprintf("Returned rows hit the requested limit of %d; more rows may be available via --start-row or --all.", res.RowLimit)})
	}
	switch res.PaginationStopReason {
	case searchconsole.StopSafetyCap:
		warnings = append(warnings, output.Warning{Code: "PAGINATION_STOPPED", Message: fmt.Sprintf("Stopped at the %d-page safety cap (%d rows) without reaching the end of the API query; the result is incomplete. This should not happen in normal use; narrow the query with filters or a shorter date range and report it if it recurs.", res.PagesFetched, len(res.Rows))})
	case searchconsole.StopRepeatedPage:
		warnings = append(warnings, output.Warning{Code: "PAGINATION_STOPPED", Message: "Google returned the same page twice; pagination stopped early and the result may be truncated."})
	}
	return warnings
}

// endsWithinDays reports whether endDate (YYYY-MM-DD) is within n days of today (PT).
func endsWithinDays(endDate string, now time.Time, n int) bool {
	end, err := time.Parse(searchconsole.DateFormat, endDate)
	if err != nil {
		return true
	}
	today, err := searchconsole.TodayPT(now)
	if err != nil {
		return true
	}
	cutoff := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -n)
	return !end.Before(cutoff)
}

func writePerformanceTable(w io.Writer, res *searchconsole.PerformanceResult) {
	if len(res.Rows) == 0 {
		fmt.Fprintln(w, "No rows returned for this range.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	header := make([]string, 0, len(res.Dimensions)+4)
	for _, d := range res.Dimensions {
		header = append(header, strings.ToUpper(d))
	}
	header = append(header, "CLICKS", "IMPRESSIONS", "CTR", "POSITION")
	fmt.Fprintln(tw, strings.Join(header, "\t")+"\t")
	for _, r := range res.Rows {
		cells := make([]string, 0, len(header))
		for _, d := range res.Dimensions {
			cells = append(cells, r.Keys[d])
		}
		cells = append(cells,
			fmt.Sprintf("%.0f", r.Clicks),
			fmt.Sprintf("%.0f", r.Impressions),
			fmt.Sprintf("%.1f%%", r.CTR*100),
			fmt.Sprintf("%.1f", r.Position),
		)
		fmt.Fprintln(tw, strings.Join(cells, "\t")+"\t")
	}
	tw.Flush()
}
