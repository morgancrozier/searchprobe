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

const (
	deltaSemantics    = "current minus previous; for position, lower is better, so a negative delta means the average rank improved"
	compareDefaultTop = searchconsole.DefaultRowLimit
)

func newCompareCmd(a *app) *cobra.Command {
	var (
		f            performanceFlags
		previous     bool
		compareStart string
		compareEnd   string
		limit        int
		sortKey      string
		ascending    bool
	)
	cmd := &cobra.Command{
		Use:   "compare",
		Short: "Compare Search Analytics between two date windows",
		Long: `Compare the same query across two Pacific Time windows. Use --site exactly
as listed by gsc sites and --json for structured results, meta and warnings.

--days (ending yesterday) or --start/--end selects the current window.
Choose --previous for the preceding equal-length window, or specify both
--compare-start/--compare-end (non-overlapping). Default final data may omit
recent days. Use --dimensions "" for aggregates, query or page for breakdowns.

Both windows are fully paginated before joining and sorting; fetch failures
fail the command. Google still exposes only top rows, so this does not prove
source completeness. --limit caps emitted joined rows, not API fetching.
Totals cover joined rows and are not a replacement for aggregate queries.

Deltas are current minus previous. Missing rows count as 0 for clicks and
impressions, not proof of zero activity. CTR/position deltas require both rows;
percent changes are null when the previous value is missing or zero.
Lower position is better (negative delta = improved).

Sort defaults to current-clicks descending. --sort clicks-delta --asc shows
largest losses first; --sort position-delta --asc shows rank improvements.
See --sort for keys; ties are deterministic.`,
		Example: `  gsc compare --site sc-domain:example.com --days 7 --dimensions page --previous --json
  gsc compare --site sc-domain:example.com --start 2026-09-03 --end 2026-09-09 --compare-start 2026-08-27 --compare-end 2026-09-02 --dimensions query --json
  gsc compare --site sc-domain:example.com --days 28 --dimensions "" --previous --json
  gsc compare --site sc-domain:example.com --days 7 --dimensions page --previous --sort impressions-delta --asc --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			f.all = true
			base, dateMode, err := buildPerformanceRequest(cmd, f, a.now())
			if err != nil {
				return err
			}
			req := searchconsole.CompareRequest{Base: base}
			var previousMode string
			switch {
			case previous && (compareStart != "" || compareEnd != ""):
				return gscerr.New(gscerr.CodeInvalidDateRange, "--previous cannot be combined with --compare-start/--compare-end.", "Use either --previous or an explicit comparison window.")
			case previous:
				req.PreviousStartDate, req.PreviousEndDate, err = searchconsole.PreviousWindow(base.StartDate, base.EndDate)
				if err != nil {
					return err
				}
				previousMode = "previous"
			case compareStart != "" && compareEnd != "":
				req.PreviousStartDate, req.PreviousEndDate = compareStart, compareEnd
				previousMode = "explicit"
			case compareStart != "" || compareEnd != "":
				return gscerr.New(gscerr.CodeInvalidDateRange, "--compare-start and --compare-end must be given together.", "Pass both, or use --previous.")
			default:
				return gscerr.New(gscerr.CodeInvalidDateRange, "A comparison window is required.", "Pass --previous, or --compare-start and --compare-end.")
			}
			if limit < 0 {
				return gscerr.New(gscerr.CodeInvalidArgument, "--limit must be zero (all rows) or positive.", "")
			}
			sortBy, err := searchconsole.ParseSortKey(sortKey)
			if err != nil {
				return err
			}
			if err := req.Validate(); err != nil {
				return err
			}
			client, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			res, err := client.ComparePerformance(cmd.Context(), req)
			if err != nil {
				return err
			}
			rows := res.Rows
			searchconsole.SortRows(rows, res.Dimensions, sortBy, ascending)
			if limit > 0 && len(rows) > limit {
				rows = rows[:limit]
			}
			data := compareData{Rows: rows, Totals: compareTotals{Current: res.CurrentTotals, Previous: res.PrevTotals, Delta: res.TotalsDelta}}
			meta := compareMeta(res, dateMode, previousMode, f.days, len(rows), limit, sortBy, ascending)
			warnings := compareWarnings(res, a.now())
			return a.emit(data, meta, warnings, func(w io.Writer) {
				writeCompareTable(w, res, rows)
				fmt.Fprintf(a.stderr, "Site %s, current %s..%s vs previous %s..%s (%s), %d joined row(s)%s.\n",
					res.Site, res.Current.StartDate, res.Current.EndDate, res.Previous.StartDate, res.Previous.EndDate, searchconsole.Timezone, len(res.Rows), limitNote(len(rows), len(res.Rows)))
			})
		},
	}
	registerQueryFlags(cmd, &f)
	fl := cmd.Flags()
	fl.BoolVar(&previous, "previous", false, "compare against the immediately preceding window of equal length")
	fl.StringVar(&compareStart, "compare-start", "", "explicit comparison window start YYYY-MM-DD (Pacific Time)")
	fl.StringVar(&compareEnd, "compare-end", "", "explicit comparison window end YYYY-MM-DD (Pacific Time)")
	fl.IntVar(&limit, "limit", compareDefaultTop, "maximum joined rows to emit after sorting (0 = all)")
	fl.StringVar(&sortKey, "sort", searchconsole.SortCurrentClicks, "primary sort key: "+searchconsole.SortKeyList)
	fl.BoolVar(&ascending, "asc", false, "sort ascending (smallest or most negative first) instead of descending")
	return cmd
}

type compareData struct {
	Rows   []searchconsole.CompareRow `json:"rows"`
	Totals compareTotals              `json:"returnedTotals"`
}

// compareTotals sums returned rows per period; they are not property totals
// unless --dimensions "" was used.
type compareTotals struct {
	Current  searchconsole.Totals      `json:"current"`
	Previous searchconsole.Totals      `json:"previous"`
	Delta    searchconsole.TotalsDelta `json:"delta"`
}

func windowMeta(w searchconsole.Window) map[string]any {
	m := map[string]any{
		"startDate":    w.StartDate,
		"endDate":      w.EndDate,
		"days":         w.Days,
		"rowCount":     w.RowCount,
		"pagesFetched": w.PagesFetched,
	}
	if w.FirstObservedDate != "" {
		m["firstObservedDate"] = w.FirstObservedDate
		m["lastObservedDate"] = w.LastObservedDate
	}
	if w.FirstIncompleteDate != "" {
		m["firstIncompleteDate"] = w.FirstIncompleteDate
	}
	if w.FirstIncompleteHour != "" {
		m["firstIncompleteHour"] = w.FirstIncompleteHour
	}
	return m
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
		"site":                res.Site,
		"timezone":            searchconsole.Timezone,
		"dateMode":            dateMode,
		"previousMode":        previousMode,
		"current":             windowMeta(res.Current),
		"previous":            windowMeta(res.Previous),
		"searchType":          res.SearchType,
		"dataState":           res.DataState,
		"dimensions":          res.Dimensions,
		"filters":             res.Filters,
		"filterLogic":         "and",
		"rowCount":            emitted,
		"joinedRows":          len(res.Rows),
		"limit":               limit,
		"sort":                map[string]any{"key": sortBy, "direction": direction},
		"sortedBy":            sortedBy,
		"deltaSemantics":      deltaSemantics,
		"paginationExhausted": true,
		"sourceMayBePartial":  true,
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

func compareWarnings(res *searchconsole.CompareResult, now time.Time) []output.Warning {
	// Reuse the performance policy for each window, then de-duplicate codes
	// and add comparison-specific conditions.
	seen := map[string]bool{}
	var warnings []output.Warning
	add := func(w output.Warning) {
		if !seen[w.Code] {
			seen[w.Code] = true
			warnings = append(warnings, w)
		}
	}
	for _, w := range windowWarnings(res, res.Current, "current", now) {
		add(w)
	}
	for _, w := range windowWarnings(res, res.Previous, "previous", now) {
		add(w)
	}
	if res.Current.Days != res.Previous.Days {
		add(output.Warning{Code: "UNEQUAL_WINDOWS", Message: fmt.Sprintf("The current window is %d day(s) and the previous window is %d day(s); absolute deltas are not like-for-like.", res.Current.Days, res.Previous.Days)})
	}
	return warnings
}

func windowWarnings(res *searchconsole.CompareResult, w searchconsole.Window, label string, now time.Time) []output.Warning {
	pr := &searchconsole.PerformanceResult{
		StartDate:           w.StartDate,
		EndDate:             w.EndDate,
		DataState:           res.DataState,
		FirstIncompleteDate: w.FirstIncompleteDate,
		FirstIncompleteHour: w.FirstIncompleteHour,
		PaginationExhausted: true,
	}
	out := performanceWarnings(pr, true, now)
	for i := range out {
		if out[i].Code == "RECENT_DAYS_MAY_BE_EXCLUDED" {
			out[i].Message = "The " + label + " window may be missing its final days (finalized data lags two or three days); deltas can understate the " + label + " period. " + out[i].Message
		}
	}
	return out
}

func limitNote(emitted, total int) string {
	if emitted < total {
		return fmt.Sprintf(", showing top %d", emitted)
	}
	return ""
}

func writeCompareTable(w io.Writer, res *searchconsole.CompareResult, rows []searchconsole.CompareRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "No rows returned in either window.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	header := make([]string, 0, len(res.Dimensions)+7)
	for _, d := range res.Dimensions {
		header = append(header, strings.ToUpper(d))
	}
	header = append(header, "CLICKS", "PREV", "DELTA", "DELTA%", "IMPR", "PREV", "POS DELTA")
	fmt.Fprintln(tw, strings.Join(header, "\t")+"\t")
	for _, r := range rows {
		cells := make([]string, 0, len(header))
		for _, d := range res.Dimensions {
			cells = append(cells, r.Keys[d])
		}
		cells = append(cells,
			fmtMetric(r.Current, func(m *searchconsole.Metrics) float64 { return m.Clicks }),
			fmtMetric(r.Previous, func(m *searchconsole.Metrics) float64 { return m.Clicks }),
			fmt.Sprintf("%+.0f", r.Delta.Clicks),
			fmtPct(r.Delta.ClicksPct),
			fmtMetric(r.Current, func(m *searchconsole.Metrics) float64 { return m.Impressions }),
			fmtMetric(r.Previous, func(m *searchconsole.Metrics) float64 { return m.Impressions }),
			fmtSigned(r.Delta.Position),
		)
		fmt.Fprintln(tw, strings.Join(cells, "\t")+"\t")
	}
	tw.Flush()
	fmt.Fprintf(w, "\nTotals: clicks %.0f vs %.0f (%+.0f%s), impressions %.0f vs %.0f (%+.0f%s)\n",
		res.CurrentTotals.Clicks, res.PrevTotals.Clicks, res.TotalsDelta.Clicks, pctSuffix(res.TotalsDelta.ClicksPct),
		res.CurrentTotals.Impressions, res.PrevTotals.Impressions, res.TotalsDelta.Impressions, pctSuffix(res.TotalsDelta.ImpressionsPct))
}

func fmtMetric(m *searchconsole.Metrics, pick func(*searchconsole.Metrics) float64) string {
	if m == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f", pick(m))
}

func fmtPct(p *float64) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%+.1f%%", *p)
}

func fmtSigned(p *float64) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%+.1f", *p)
}

func pctSuffix(p *float64) string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf(", %+.1f%%", *p)
}
