package searchconsole

import (
	"context"
	"sort"
)

// Site is a Search Console property the authenticated account can access.
type Site struct {
	// SiteURL is Google's canonical property identifier, e.g.
	// "sc-domain:example.com" or "https://www.example.com/".
	SiteURL string `json:"siteUrl"`
	// PermissionLevel is Google's value, e.g. siteOwner, siteFullUser,
	// siteRestrictedUser, siteUnverifiedUser.
	PermissionLevel string `json:"permissionLevel"`
	// Type is derived: "domain" for sc-domain: properties, else "urlPrefix".
	Type string `json:"type"`
}

type sitesListResponse struct {
	SiteEntry []struct {
		SiteURL         string `json:"siteUrl"`
		PermissionLevel string `json:"permissionLevel"`
	} `json:"siteEntry"`
}

// ListSites returns all properties visible to the account, sorted by URL.
func (c *Client) ListSites(ctx context.Context) ([]Site, error) {
	var resp sitesListResponse
	if err := c.do(ctx, "GET", c.BaseURL+"/sites", nil, &resp); err != nil {
		return nil, err
	}
	sites := make([]Site, 0, len(resp.SiteEntry))
	for _, e := range resp.SiteEntry {
		sites = append(sites, Site{
			SiteURL:         e.SiteURL,
			PermissionLevel: e.PermissionLevel,
			Type:            PropertyType(e.SiteURL),
		})
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].SiteURL < sites[j].SiteURL })
	return sites, nil
}

// PropertyType classifies a canonical property identifier.
func PropertyType(siteURL string) string {
	if len(siteURL) > len("sc-domain:") && siteURL[:len("sc-domain:")] == "sc-domain:" {
		return "domain"
	}
	return "urlPrefix"
}
