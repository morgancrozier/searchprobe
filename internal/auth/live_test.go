package auth

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Explicit isolated test configuration only; never uses the developer's default grant.
func TestLiveBYORefresh(t *testing.T) {
	dir := os.Getenv("GSC_TEST_LIVE_CONFIG")
	if dir == "" {
		t.Skip("set GSC_TEST_LIVE_CONFIG to an isolated, authorized BYO test configuration")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("test config must be absolute")
	}
	t.Setenv("GSC_CONFIG_DIR", dir)
	s, err := DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireBYO(c.Client); err != nil {
		t.Fatal(err)
	}
	// Expire only the in-memory access token to exercise Google's refresh endpoint.
	c.Token.Expiry = time.Now().Add(-time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := HTTPClient(ctx, c, s).Get("https://www.googleapis.com/webmasters/v3/sites")
	if err != nil {
		t.Fatal("live request failed; no credentials logged")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("live request status %d", resp.StatusCode)
	}
	saved, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Client != c.Client || !saved.Token.Expiry.After(time.Now()) {
		t.Fatal("refreshed credential persistence failed")
	}
}
