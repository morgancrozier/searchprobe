package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
	"github.com/morgancrozier/searchprobe/internal/searchconsole"
)

// maxBatchURLs matches Google's per-site daily URL Inspection quota so one
// batch can never exceed it.
const maxBatchURLs = 2000

func newInspectCmd(a *app) *cobra.Command {
	var site, urlsFile string
	cmd := &cobra.Command{
		Use:   "inspect [<url>]",
		Short: "Show the indexed status of a URL (Google's indexed version only)",
		Long: `Inspect a URL with the Search Console URL Inspection API.

The API reports the version of the page in Google's index: verdict, coverage
state, robots.txt and indexing state, last crawl time, canonical URLs, and
any rich results Google detected. It cannot run the live URL test from the
Search Console UI, and it cannot request indexing (meta.liveTest is always
false). The URL must belong to the property given with --site.

Batch mode
  --urls-file reads one URL per line (blank lines and lines starting with #
  are skipped, duplicates inspected once; "-" reads stdin). URLs are
  inspected sequentially and each gets its own result or error, so one bad
  URL does not fail the batch. Authentication, quota, rate-limit, or network
  failures stop the batch because continuing would be unsafe. A batch is
  limited to 2000 URLs, the per-site daily inspection quota.

Output shape (--json, single URL): data.indexStatus is Google's
indexStatusResult normalized:
  {"verdict":"PASS","coverageState":"Submitted and indexed",
   "robotsTxtState":"ALLOWED","indexingState":"INDEXING_ALLOWED",
   "lastCrawlTime":"...","pageFetchState":"SUCCESSFUL",
   "googleCanonical":"...","userCanonical":"...","crawledAs":"MOBILE",
   "sitemaps":[...],"referringUrls":[...]}
plus richResults, mobileUsability, and amp when Google returns them.
In batch mode data.results[] holds {url, ok, inspection} or {url, ok, error}.`,
		Example: `  gsc inspect https://example.com/page --site sc-domain:example.com --json
  gsc inspect --site sc-domain:example.com --urls-file urls.txt --json
  cat urls.txt | gsc inspect --site sc-domain:example.com --urls-file - --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(args) == 1 && urlsFile != "":
				return gscerr.New(gscerr.CodeInvalidArgument, "Pass either a single URL or --urls-file, not both.", "")
			case len(args) == 0 && urlsFile == "":
				return gscerr.New(gscerr.CodeInvalidArgument, "A URL to inspect is required.", "Pass a URL, or --urls-file <path> (\"-\" for stdin) for a batch.")
			}
			if len(args) == 1 {
				return inspectOne(cmd, a, site, strings.TrimSpace(args[0]))
			}
			return inspectBatch(cmd, a, site, urlsFile)
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "Search Console property containing the URL, exactly as listed by gsc sites (required)")
	cmd.Flags().StringVar(&urlsFile, "urls-file", "", "file with one URL per line to inspect sequentially (\"-\" for stdin)")
	_ = cmd.MarkFlagRequired("site")
	return cmd
}

func inspectMeta(site string) map[string]any {
	// inspectionType/liveTest replace a per-call warning: the API only ever
	// describes Google's indexed version and cannot request indexing.
	return map[string]any{"site": site, "inspectionType": "indexedVersion", "liveTest": false}
}

func inspectOne(cmd *cobra.Command, a *app, site, url string) error {
	req := searchconsole.InspectionRequest{Site: site, InspectionURL: url}
	if err := req.Validate(); err != nil {
		return err
	}
	client, err := a.client(cmd.Context())
	if err != nil {
		return err
	}
	res, err := client.InspectURL(cmd.Context(), req)
	if err != nil {
		return err
	}
	meta := inspectMeta(site)
	meta["inspectionUrl"] = res.InspectionURL
	return a.emit(res, meta, nil, func(w io.Writer) { writeInspection(w, res) })
}

// batchItem is one URL's outcome in batch mode.
type batchItem struct {
	URL        string                          `json:"url"`
	OK         bool                            `json:"ok"`
	Inspection *searchconsole.InspectionResult `json:"inspection,omitempty"`
	Error      *gscerr.Error                   `json:"error,omitempty"`
}

// batchAbortCodes are failures after which continuing would waste quota or
// cannot succeed; the batch stops with a structured error.
var batchAbortCodes = map[string]bool{
	gscerr.CodeAuthRequired:          true,
	gscerr.CodeAuthRevoked:           true,
	gscerr.CodeAuthScopeInsufficient: true,
	gscerr.CodeQuotaExceeded:         true,
	gscerr.CodeRateLimited:           true,
	gscerr.CodeNetworkError:          true,
}

func inspectBatch(cmd *cobra.Command, a *app, site, urlsFile string) error {
	urls, skipped, err := readURLList(urlsFile, a.stdin)
	if err != nil {
		return err
	}
	if len(urls) == 0 {
		return gscerr.New(gscerr.CodeInvalidArgument, "No URLs found in "+urlsFile+".", "Provide one URL per line.")
	}
	if len(urls) > maxBatchURLs {
		return gscerr.New(gscerr.CodeInvalidArgument, fmt.Sprintf("%d URLs exceed the batch limit of %d (Google's per-site daily inspection quota).", len(urls), maxBatchURLs), "Split the list into smaller batches.")
	}
	client, err := a.client(cmd.Context())
	if err != nil {
		return err
	}
	results := make([]batchItem, 0, len(urls))
	succeeded := 0
	for i, u := range urls {
		if err := cmd.Context().Err(); err != nil {
			return gscerr.New(gscerr.CodeNetworkError, fmt.Sprintf("Batch cancelled after %d of %d URLs.", i, len(urls)), "")
		}
		req := searchconsole.InspectionRequest{Site: site, InspectionURL: u}
		var res *searchconsole.InspectionResult
		err := req.Validate()
		if err == nil {
			res, err = client.InspectURL(cmd.Context(), req)
		}
		if err != nil {
			ge := gscerr.From(err)
			if batchAbortCodes[ge.Code] {
				return &gscerr.Error{Code: ge.Code, Message: fmt.Sprintf("%s (batch stopped at URL %d of %d: %s)", ge.Message, i+1, len(urls), u), Action: ge.Action, Retryable: ge.Retryable, Cause: err}
			}
			results = append(results, batchItem{URL: u, OK: false, Error: ge})
			continue
		}
		succeeded++
		results = append(results, batchItem{URL: u, OK: true, Inspection: res})
	}
	meta := inspectMeta(site)
	meta["source"] = urlsFile
	meta["requested"] = len(urls) + skipped
	meta["duplicatesSkipped"] = skipped
	meta["inspected"] = len(urls)
	meta["succeeded"] = succeeded
	meta["failed"] = len(urls) - succeeded
	data := map[string]any{"results": results}
	return a.emit(data, meta, nil, func(w io.Writer) {
		for i, r := range results {
			if i > 0 {
				fmt.Fprintln(w)
			}
			if r.OK {
				writeInspection(w, r.Inspection)
				continue
			}
			fmt.Fprintf(w, "URL:       %s\n", r.URL)
			fmt.Fprintf(w, "Error:     %s %s\n", r.Error.Code, r.Error.Message)
		}
		fmt.Fprintf(a.stderr, "%d of %d URL(s) inspected successfully.\n", succeeded, len(urls))
	})
}

// readURLList reads one URL per line, skipping blanks and # comments and
// de-duplicating while preserving order. path "-" reads stdin.
func readURLList(path string, stdin io.Reader) (urls []string, duplicates int, err error) {
	var r io.Reader
	if path == "-" {
		if stdin == nil {
			stdin = os.Stdin
		}
		r = stdin
	} else {
		f, ferr := os.Open(path)
		if ferr != nil {
			return nil, 0, gscerr.Wrap(ferr, gscerr.CodeInvalidArgument, fmt.Sprintf("Could not read URL list %q.", path), "Pass a readable file with one URL per line, or \"-\" for stdin.")
		}
		defer f.Close()
		r = f
	}
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if seen[line] {
			duplicates++
			continue
		}
		seen[line] = true
		urls = append(urls, line)
	}
	if err := sc.Err(); err != nil {
		return nil, 0, gscerr.Wrap(err, gscerr.CodeInvalidArgument, "Could not read the URL list.", "")
	}
	return urls, duplicates, nil
}

func writeInspection(w io.Writer, res *searchconsole.InspectionResult) {
	fmt.Fprintf(w, "URL:       %s\n", res.InspectionURL)
	fmt.Fprintf(w, "Property:  %s\n", res.Site)
	is := res.IndexStatus
	if is == nil {
		fmt.Fprintln(w, "Google returned no index status for this URL.")
		return
	}
	line := func(label, v string) {
		if v != "" {
			fmt.Fprintf(w, "%-18s %s\n", label+":", v)
		}
	}
	line("Verdict", is.Verdict)
	line("Coverage", is.CoverageState)
	line("Indexing state", is.IndexingState)
	line("robots.txt", is.RobotsTxtState)
	line("Page fetch", is.PageFetchState)
	line("Last crawl", is.LastCrawlTime)
	line("Crawled as", is.CrawledAs)
	line("Google canonical", is.GoogleCanonical)
	line("User canonical", is.UserCanonical)
	if len(is.Sitemaps) > 0 {
		line("Sitemaps", strings.Join(is.Sitemaps, ", "))
	}
	if len(is.ReferringURLs) > 0 {
		line("Referring URLs", strings.Join(is.ReferringURLs, ", "))
	}
	if res.RichResults != nil {
		types := make([]string, 0, len(res.RichResults.DetectedItems))
		for _, d := range res.RichResults.DetectedItems {
			types = append(types, d.Type)
		}
		v := res.RichResults.Verdict
		if len(types) > 0 {
			v += " (" + strings.Join(types, ", ") + ")"
		}
		line("Rich results", v)
	}
	if res.InspectionResultLink != "" {
		line("Details", res.InspectionResultLink)
	}
}
