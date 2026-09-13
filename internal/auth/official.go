package auth

import "github.com/morgancrozier/searchprobe/internal/gscerr"

// LegacyClientID is retained only to recognize and retire pre-BYO grants.
const LegacyClientID = "896690371526-u099kn10a97lo8jl1rd1vq3n27udfjqk.apps.googleusercontent.com"

func RequireBYO(c ClientConfig) error {
	if c.ClientID == "" || c.ClientID == LegacyClientID {
		return gscerr.New(gscerr.CodeAuthRequired, "SearchProbe requires your own Google Cloud Desktop OAuth client.", "Run gsc setup --client-file <downloaded-client.json> to connect your project. Existing shared-client credentials can still be removed with gsc auth logout.")
	}
	return nil
}
