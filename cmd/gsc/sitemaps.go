package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/morgancrozier/searchprobe/internal/searchconsole"
)

func newSitemapsCmd(a *app) *cobra.Command {
	var site, index string
	cmd := &cobra.Command{
		Use:   "sitemaps",
		Short: "List sitemaps submitted for a property (read-only)",
		Long: `List the sitemaps Google has recorded for a property, with type, submitted
URL counts, last download time, pending state, and error/warning counts.

Pass --index to list only the sitemaps contained in a sitemap index file.

This command is read-only. Submitting or deleting sitemaps is intentionally
not supported; gsc holds only the read-only Search Console scope.`,
		Example: `  gsc sitemaps --site sc-domain:example.com
  gsc sitemaps --site https://www.example.com/ --index https://www.example.com/sitemap_index.xml --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			sitemaps, err := client.ListSitemaps(cmd.Context(), site, index)
			if err != nil {
				return err
			}
			data := map[string]any{"sitemaps": sitemaps}
			// readOnly and submittedCountsOnly replace a per-call warning: gsc
			// never submits or deletes sitemaps, and URL counts are Google's
			// submitted counts, not index coverage.
			meta := map[string]any{"site": site, "count": len(sitemaps), "readOnly": true, "submittedCountsOnly": true}
			if index != "" {
				meta["sitemapIndex"] = index
			}
			return a.emit(data, meta, nil, func(w io.Writer) {
				if len(sitemaps) == 0 {
					fmt.Fprintln(w, "No sitemaps are recorded for this property.")
					return
				}
				tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "SITEMAP\tTYPE\tURLS\tERRORS\tWARNINGS\tPENDING\tLAST DOWNLOADED")
				for _, s := range sitemaps {
					t := s.Type
					if s.IsSitemapsIndex {
						t += " (index)"
					}
					fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\t%s\n", s.Path, t, s.SubmittedURLs, s.Errors, s.Warnings, yesNo(s.IsPending), orDash(s.LastDownloaded))
				}
				tw.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "Search Console property exactly as listed by gsc sites (required)")
	cmd.Flags().StringVar(&index, "index", "", "only list sitemaps contained in this sitemap index URL")
	_ = cmd.MarkFlagRequired("site")
	return cmd
}

func newSitemapCmd(a *app) *cobra.Command {
	var site string
	cmd := &cobra.Command{
		Use:   "sitemap <sitemap-url>",
		Short: "Show details for one submitted sitemap (read-only)",
		Long: `Show Google's record for one submitted sitemap: type, submission and
download times, pending state, error/warning counts, and submitted URL
counts per content type.

Pass the sitemap URL exactly as printed by gsc sitemaps.`,
		Example: `  gsc sitemap https://www.example.com/sitemap.xml --site sc-domain:example.com --json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			s, err := client.GetSitemap(cmd.Context(), site, strings.TrimSpace(args[0]))
			if err != nil {
				return err
			}
			meta := map[string]any{"site": site, "sitemap": s.Path, "readOnly": true, "submittedCountsOnly": true}
			return a.emit(s, meta, nil, func(w io.Writer) { writeSitemap(w, s) })
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "Search Console property exactly as listed by gsc sites (required)")
	_ = cmd.MarkFlagRequired("site")
	return cmd
}

func writeSitemap(w io.Writer, s *searchconsole.Sitemap) {
	line := func(label, v string) { fmt.Fprintf(w, "%-17s %s\n", label+":", v) }
	line("Sitemap", s.Path)
	t := s.Type
	if s.IsSitemapsIndex {
		t += " (sitemap index)"
	}
	line("Type", t)
	line("Pending", yesNo(s.IsPending))
	line("Last submitted", orDash(s.LastSubmitted))
	line("Last downloaded", orDash(s.LastDownloaded))
	line("Errors", fmt.Sprint(s.Errors))
	line("Warnings", fmt.Sprint(s.Warnings))
	line("Submitted URLs", fmt.Sprint(s.SubmittedURLs))
	for _, c := range s.Contents {
		fmt.Fprintf(w, "  %-15s %d\n", c.Type, c.Submitted)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
