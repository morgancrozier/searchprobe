package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/morgancrozier/searchprobe/internal/searchconsole"
)

func newSitesCmd(a *app) *cobra.Command {
	var match string
	cmd := &cobra.Command{
		Use:   "sites",
		Short: "List Search Console properties the signed-in account can access",
		Long: `List Search Console properties visible to the authenticated Google account.

Property identifiers are Google's canonical values and must be passed verbatim
to --site in other commands, e.g. "sc-domain:example.com" or
"https://www.example.com/".

--match keeps only properties whose identifier contains the given text
(case-insensitive substring, no regex).`,
		Example: `  gsc sites
  gsc sites --match example --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			all, err := client.ListSites(cmd.Context())
			if err != nil {
				return err
			}
			sites := filterSites(all, match)
			data := map[string]any{"sites": sites}
			meta := map[string]any{"count": len(sites), "total": len(all)}
			if match != "" {
				meta["match"] = match
			}
			return a.emit(data, meta, nil, func(w io.Writer) {
				if len(sites) == 0 {
					if match != "" {
						fmt.Fprintf(w, "No properties match %q (%d accessible).\n", match, len(all))
						return
					}
					fmt.Fprintln(w, "No Search Console properties are accessible to this account.")
					return
				}
				tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "SITE\tTYPE\tPERMISSION")
				for _, s := range sites {
					fmt.Fprintf(tw, "%s\t%s\t%s\n", s.SiteURL, s.Type, s.PermissionLevel)
				}
				tw.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&match, "match", "", "only list properties whose identifier contains this text (case-insensitive)")
	return cmd
}

// filterSites returns the sites whose canonical identifier contains match,
// case-insensitively. An empty match returns all sites.
func filterSites(sites []searchconsole.Site, match string) []searchconsole.Site {
	out := make([]searchconsole.Site, 0, len(sites))
	needle := strings.ToLower(strings.TrimSpace(match))
	for _, s := range sites {
		if needle == "" || strings.Contains(strings.ToLower(s.SiteURL), needle) {
			out = append(out, s)
		}
	}
	return out
}
