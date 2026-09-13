package searchconsole

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/morgancrozier/searchprobe/internal/gscerr"
)

// SitemapContent is one content-type breakdown within a sitemap.
type SitemapContent struct {
	// Type is Google's value: web, image, video, news, mobile, androidApp, iosApp, pattern.
	Type string `json:"type"`
	// Submitted is the number of URLs of this type in the sitemap.
	Submitted int64 `json:"submitted"`
}

// Sitemap is a normalized, read-only view of a submitted sitemap.
// Google's deprecated contents[].indexed counter is intentionally omitted.
type Sitemap struct {
	// Path is the sitemap URL as Google records it.
	Path string `json:"path"`
	// Type is Google's value: sitemap, urlList, rssFeed, atomFeed, patternSitemap, notSitemap.
	Type            string `json:"type"`
	IsSitemapsIndex bool   `json:"isSitemapsIndex"`
	// IsPending is true when Google has not yet processed the sitemap.
	IsPending      bool   `json:"isPending"`
	LastSubmitted  string `json:"lastSubmitted,omitempty"`
	LastDownloaded string `json:"lastDownloaded,omitempty"`
	Warnings       int64  `json:"warnings"`
	Errors         int64  `json:"errors"`
	// SubmittedURLs sums Contents[].Submitted across content types.
	SubmittedURLs int64            `json:"submittedUrls"`
	Contents      []SitemapContent `json:"contents"`
}

// flexInt64 accepts Google's "long" values, which arrive as JSON strings
// ("123") but are also tolerated as plain numbers.
type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	*f = flexInt64(n)
	return nil
}

// wmxSitemap mirrors https://developers.google.com/webmaster-tools/v1/sitemaps
type wmxSitemap struct {
	Path            string    `json:"path"`
	LastSubmitted   string    `json:"lastSubmitted"`
	IsPending       bool      `json:"isPending"`
	IsSitemapsIndex bool      `json:"isSitemapsIndex"`
	Type            string    `json:"type"`
	LastDownloaded  string    `json:"lastDownloaded"`
	Warnings        flexInt64 `json:"warnings"`
	Errors          flexInt64 `json:"errors"`
	Contents        []struct {
		Type      string    `json:"type"`
		Submitted flexInt64 `json:"submitted"`
	} `json:"contents"`
}

type sitemapsListResponse struct {
	Sitemap []json.RawMessage `json:"sitemap"`
}

func normalizeSitemap(w wmxSitemap) Sitemap {
	s := Sitemap{
		Path:            w.Path,
		Type:            w.Type,
		IsSitemapsIndex: w.IsSitemapsIndex,
		IsPending:       w.IsPending,
		LastSubmitted:   w.LastSubmitted,
		LastDownloaded:  w.LastDownloaded,
		Warnings:        int64(w.Warnings),
		Errors:          int64(w.Errors),
		Contents:        []SitemapContent{},
	}
	for _, c := range w.Contents {
		s.Contents = append(s.Contents, SitemapContent{Type: c.Type, Submitted: int64(c.Submitted)})
		s.SubmittedURLs += int64(c.Submitted)
	}
	return s
}

// ListSitemaps returns the sitemaps submitted for a property, sorted by
// path. If sitemapIndex is non-empty, only sitemaps listed in that sitemap
// index are returned (Google's sitemapIndex query parameter).
func (c *Client) ListSitemaps(ctx context.Context, site, sitemapIndex string) ([]Sitemap, error) {
	if strings.TrimSpace(site) == "" {
		return nil, gscerr.New(gscerr.CodeInvalidArgument, "A Search Console property is required.", "Pass --site with a value from `gsc sites --json`.")
	}
	endpoint := c.BaseURL + "/sites/" + url.PathEscape(site) + "/sitemaps"
	if sitemapIndex != "" {
		endpoint += "?sitemapIndex=" + url.QueryEscape(sitemapIndex)
	}
	var resp sitemapsListResponse
	if err := c.do(ctx, "GET", endpoint, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Sitemap, 0, len(resp.Sitemap))
	for _, raw := range resp.Sitemap {
		var w wmxSitemap
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, gscerr.Wrap(err, gscerr.CodeGoogleAPIError, "Google returned a sitemap entry gsc could not parse.", "Retry the command; if it persists, report a bug with the command used.")
		}
		out = append(out, normalizeSitemap(w))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// GetSitemap returns details for one submitted sitemap URL.
func (c *Client) GetSitemap(ctx context.Context, site, sitemapURL string) (*Sitemap, error) {
	if strings.TrimSpace(site) == "" {
		return nil, gscerr.New(gscerr.CodeInvalidArgument, "A Search Console property is required.", "Pass --site with a value from `gsc sites --json`.")
	}
	if strings.TrimSpace(sitemapURL) == "" {
		return nil, gscerr.New(gscerr.CodeInvalidArgument, "A sitemap URL is required.", "Pass the sitemap URL exactly as listed by `gsc sitemaps --site <property>`.")
	}
	endpoint := c.BaseURL + "/sites/" + url.PathEscape(site) + "/sitemaps/" + url.PathEscape(sitemapURL)
	var w wmxSitemap
	if err := c.do(ctx, "GET", endpoint, nil, &w); err != nil {
		var ge *gscerr.Error
		if errors.As(err, &ge) && ge.Code == gscerr.CodePropertyNotFound {
			return nil, gscerr.Wrap(err, gscerr.CodeSitemapNotFound,
				"Sitemap "+sitemapURL+" is not submitted for property "+site+".",
				"Run `gsc sitemaps --site "+site+" --json` to list submitted sitemaps and pass one exactly as shown.")
		}
		return nil, err
	}
	s := normalizeSitemap(w)
	return &s, nil
}
