package searchconsole

import (
	"context"
	"net/url"
	"strings"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

// InspectionRequest asks for the indexed state of one URL within a property.
type InspectionRequest struct {
	Site          string
	InspectionURL string
	LanguageCode  string // optional BCP-47, defaults to en-US on Google's side
}

// Validate checks the request locally before calling Google.
//
// Principle: Google is authoritative for whether a URL belongs to a
// property. Local containment validation is a convenience that catches
// obvious mistakes early; it is not a security boundary and must stay
// conservative, rejecting only URLs that certainly lie outside the property
// and deferring every ambiguous case to the API.
func (r InspectionRequest) Validate() error {
	if strings.TrimSpace(r.Site) == "" {
		return gscerr.New(gscerr.CodeInvalidArgument, "A Search Console property is required.", "Pass --site with a value from `gsc sites --json`.")
	}
	raw := strings.TrimSpace(r.InspectionURL)
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Hostname() == "" {
		return gscerr.New(gscerr.CodeInvalidArgument, "The URL to inspect must be a fully-qualified URL such as https://example.com/page.", "")
	}
	if !URLWithinProperty(u, r.Site) {
		return gscerr.New(gscerr.CodeURLOutsideProperty,
			"The URL "+raw+" is not within property "+r.Site+".",
			"Pass a --site property that contains the URL (for example sc-domain:example.com for any URL on example.com).")
	}
	return nil
}

// URLWithinProperty reports whether u could belong to site. It returns false
// only when the URL is certainly outside the property and true otherwise,
// leaving Google's own check authoritative. It does not attempt to reproduce
// Google's URL canonicalization.
//
// Per https://support.google.com/webmasters/answer/34592:
//   - Domain properties (sc-domain:) cover the domain and every subdomain on
//     any protocol and path, so only the hostname is compared.
//   - URL-prefix properties cover only URLs starting with the exact prefix,
//     including the protocol, so a different scheme or hostname is certainly
//     outside. Paths are not compared locally.
func URLWithinProperty(u *url.URL, site string) bool {
	host := u.Hostname()
	if strings.HasPrefix(site, "sc-domain:") {
		domain := strings.TrimPrefix(site, "sc-domain:")
		return strings.EqualFold(host, domain) || strings.HasSuffix(strings.ToLower(host), "."+strings.ToLower(domain))
	}
	p, err := url.Parse(site)
	if err != nil || p.Hostname() == "" {
		return true
	}
	return strings.EqualFold(u.Scheme, p.Scheme) && strings.EqualFold(host, p.Hostname())
}

// Wire types for https://developers.google.com/webmaster-tools/v1/urlInspection.index/inspect
type inspectRequest struct {
	InspectionURL string `json:"inspectionUrl"`
	SiteURL       string `json:"siteUrl"`
	LanguageCode  string `json:"languageCode,omitempty"`
}

type inspectResponse struct {
	InspectionResult struct {
		InspectionResultLink string `json:"inspectionResultLink"`
		IndexStatusResult    *struct {
			Verdict         string   `json:"verdict"`
			CoverageState   string   `json:"coverageState"`
			RobotsTxtState  string   `json:"robotsTxtState"`
			IndexingState   string   `json:"indexingState"`
			LastCrawlTime   string   `json:"lastCrawlTime"`
			PageFetchState  string   `json:"pageFetchState"`
			GoogleCanonical string   `json:"googleCanonical"`
			UserCanonical   string   `json:"userCanonical"`
			CrawledAs       string   `json:"crawledAs"`
			Sitemap         []string `json:"sitemap"`
			ReferringUrls   []string `json:"referringUrls"`
		} `json:"indexStatusResult"`
		MobileUsabilityResult *struct {
			Verdict string  `json:"verdict"`
			Issues  []issue `json:"issues"`
		} `json:"mobileUsabilityResult"`
		RichResultsResult *struct {
			Verdict       string `json:"verdict"`
			DetectedItems []struct {
				RichResultType string `json:"richResultType"`
				Items          []struct {
					Name   string  `json:"name"`
					Issues []issue `json:"issues"`
				} `json:"items"`
			} `json:"detectedItems"`
		} `json:"richResultsResult"`
		AmpResult *struct {
			Verdict               string  `json:"verdict"`
			AmpURL                string  `json:"ampUrl"`
			IndexingState         string  `json:"indexingState"`
			RobotsTxtState        string  `json:"robotsTxtState"`
			AmpIndexStatusVerdict string  `json:"ampIndexStatusVerdict"`
			LastCrawlTime         string  `json:"lastCrawlTime"`
			PageFetchState        string  `json:"pageFetchState"`
			Issues                []issue `json:"issues"`
		} `json:"ampResult"`
	} `json:"inspectionResult"`
}

type issue struct {
	IssueMessage string `json:"issueMessage"`
	Severity     string `json:"severity"`
}

// Issue is a normalized problem reported by an inspection sub-result.
type Issue struct {
	Message  string `json:"message"`
	Severity string `json:"severity,omitempty"`
}

// IndexStatus is the indexed-version state of the URL.
type IndexStatus struct {
	Verdict         string `json:"verdict"`
	CoverageState   string `json:"coverageState,omitempty"`
	RobotsTxtState  string `json:"robotsTxtState,omitempty"`
	IndexingState   string `json:"indexingState,omitempty"`
	LastCrawlTime   string `json:"lastCrawlTime,omitempty"`
	PageFetchState  string `json:"pageFetchState,omitempty"`
	GoogleCanonical string `json:"googleCanonical,omitempty"`
	UserCanonical   string `json:"userCanonical,omitempty"`
	CrawledAs       string `json:"crawledAs,omitempty"`
	// Sitemaps and ReferringURLs are the entries Google chose to report; they
	// are not guaranteed to be exhaustive.
	Sitemaps      []string `json:"sitemaps"`
	ReferringURLs []string `json:"referringUrls"`
}

// RichResultItem is one detected structured-data item.
type RichResultItem struct {
	Name   string  `json:"name,omitempty"`
	Issues []Issue `json:"issues"`
}

// RichResultType groups detected items of one rich-result type.
type RichResultType struct {
	Type  string           `json:"type"`
	Items []RichResultItem `json:"items"`
}

// RichResults summarizes structured data Google found in the indexed version.
type RichResults struct {
	Verdict       string           `json:"verdict"`
	DetectedItems []RichResultType `json:"detectedItems"`
}

// MobileUsability is included only when Google still returns it.
type MobileUsability struct {
	Verdict string  `json:"verdict"`
	Issues  []Issue `json:"issues"`
}

// AMPResult summarizes the AMP version, when the page has one.
type AMPResult struct {
	Verdict               string  `json:"verdict"`
	AMPURL                string  `json:"ampUrl,omitempty"`
	IndexingState         string  `json:"indexingState,omitempty"`
	RobotsTxtState        string  `json:"robotsTxtState,omitempty"`
	AMPIndexStatusVerdict string  `json:"ampIndexStatusVerdict,omitempty"`
	LastCrawlTime         string  `json:"lastCrawlTime,omitempty"`
	PageFetchState        string  `json:"pageFetchState,omitempty"`
	Issues                []Issue `json:"issues"`
}

// InspectionResult is the normalized URL Inspection response. It describes
// Google's indexed version only; no live test is performed.
type InspectionResult struct {
	Site                 string           `json:"site"`
	InspectionURL        string           `json:"inspectionUrl"`
	InspectionResultLink string           `json:"inspectionResultLink,omitempty"`
	IndexStatus          *IndexStatus     `json:"indexStatus"`
	RichResults          *RichResults     `json:"richResults,omitempty"`
	MobileUsability      *MobileUsability `json:"mobileUsability,omitempty"`
	AMP                  *AMPResult       `json:"amp,omitempty"`
}

// InspectURL returns the indexed state of one URL.
func (c *Client) InspectURL(ctx context.Context, req InspectionRequest) (*InspectionResult, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	wire := inspectRequest{InspectionURL: req.InspectionURL, SiteURL: req.Site, LanguageCode: req.LanguageCode}
	var resp inspectResponse
	if err := c.do(ctx, "POST", c.InspectionBaseURL+"/urlInspection/index:inspect", wire, &resp); err != nil {
		return nil, err
	}
	return normalizeInspection(req, resp), nil
}

func normalizeInspection(req InspectionRequest, resp inspectResponse) *InspectionResult {
	ir := resp.InspectionResult
	out := &InspectionResult{
		Site:                 req.Site,
		InspectionURL:        req.InspectionURL,
		InspectionResultLink: ir.InspectionResultLink,
	}
	if s := ir.IndexStatusResult; s != nil {
		out.IndexStatus = &IndexStatus{
			Verdict:         s.Verdict,
			CoverageState:   s.CoverageState,
			RobotsTxtState:  s.RobotsTxtState,
			IndexingState:   s.IndexingState,
			LastCrawlTime:   s.LastCrawlTime,
			PageFetchState:  s.PageFetchState,
			GoogleCanonical: s.GoogleCanonical,
			UserCanonical:   s.UserCanonical,
			CrawledAs:       s.CrawledAs,
			Sitemaps:        nonNil(s.Sitemap),
			ReferringURLs:   nonNil(s.ReferringUrls),
		}
	}
	if r := ir.RichResultsResult; r != nil {
		rr := &RichResults{Verdict: r.Verdict, DetectedItems: []RichResultType{}}
		for _, d := range r.DetectedItems {
			rt := RichResultType{Type: d.RichResultType, Items: []RichResultItem{}}
			for _, it := range d.Items {
				rt.Items = append(rt.Items, RichResultItem{Name: it.Name, Issues: normalizeIssues(it.Issues)})
			}
			rr.DetectedItems = append(rr.DetectedItems, rt)
		}
		out.RichResults = rr
	}
	if m := ir.MobileUsabilityResult; m != nil {
		out.MobileUsability = &MobileUsability{Verdict: m.Verdict, Issues: normalizeIssues(m.Issues)}
	}
	if a := ir.AmpResult; a != nil {
		out.AMP = &AMPResult{
			Verdict:               a.Verdict,
			AMPURL:                a.AmpURL,
			IndexingState:         a.IndexingState,
			RobotsTxtState:        a.RobotsTxtState,
			AMPIndexStatusVerdict: a.AmpIndexStatusVerdict,
			LastCrawlTime:         a.LastCrawlTime,
			PageFetchState:        a.PageFetchState,
			Issues:                normalizeIssues(a.Issues),
		}
	}
	return out
}

func normalizeIssues(in []issue) []Issue {
	out := make([]Issue, 0, len(in))
	for _, i := range in {
		out = append(out, Issue{Message: i.IssueMessage, Severity: i.Severity})
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
