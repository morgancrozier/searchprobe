package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/morgancrozier/searchprobe/internal/auth"
	"github.com/morgancrozier/searchprobe/internal/gscerr"
	"github.com/spf13/cobra"
)

const googleSetupGuide = `Create a Google Cloud project and enable the Google Search Console API.
Configure Google Auth Platform with webmasters.readonly access.
Personal Google account: External audience, In production (Testing tokens expire after 7 days).
Workspace/Cloud Identity: Internal is available only for eligible organization projects.
Create a Desktop app OAuth client and download its JSON. Sign in with your GSC account.
Guide: https://searchprobe.com/docs#google-setup
`

func interactiveAuth(a *app) bool { return !a.json && a.interactive != nil && a.interactive() }
func selectCredentialStore(cmd *cobra.Command, a *app, s *auth.Store, kind string) (*auth.Store, error) {
	if kind == "auto" && s.LegacyFile() && interactiveAuth(a) {
		fmt.Fprint(a.stderr, "Existing credentials use a plaintext file. Move to OS storage? [keychain/file]: ")
		answer, err := setupAnswer(cmd.Context(), a.stdin)
		if err != nil {
			return nil, gscerr.New(gscerr.CodeConfigError, "No storage choice received.", "Rerun setup with --credential-store keychain or file.")
		}
		kind = strings.TrimSpace(answer)
		if kind != "keychain" && kind != "file" {
			return nil, gscerr.New(gscerr.CodeInvalidArgument, "Choose keychain or file.", "Rerun setup.")
		}
	}
	next, err := s.Select(kind)
	if err != nil && kind != "file" && gscerr.From(err).Code == gscerr.CodeConfigError && interactiveAuth(a) {
		fmt.Fprintln(a.stderr, "OS credential storage is unavailable. File storage is plaintext protected by user-only filesystem permissions.")
		fmt.Fprint(a.stderr, "Use file storage? Type file to accept, or press Enter to stop: ")
		answer, e := setupAnswer(cmd.Context(), a.stdin)
		if e == nil && strings.TrimSpace(answer) == "file" {
			return s.Select("file")
		}
	}
	return next, err
}
func selectOAuthClient(cmd *cobra.Command, a *app, creds *auth.Credentials, path string, explicit bool) (auth.ClientConfig, error) {
	if !explicit && creds != nil && auth.RequireBYO(creds.Client) == nil {
		return creds.Client, nil
	}
	if !explicit && interactiveAuth(a) {
		fmt.Fprint(a.stderr, googleSetupGuide)
		fmt.Fprint(a.stderr, "Downloaded Desktop OAuth JSON path: ")
		answer, err := setupAnswer(cmd.Context(), a.stdin)
		if err != nil {
			return auth.ClientConfig{}, gscerr.New(gscerr.CodeAuthRequired, "No client file supplied.", "Run gsc setup --client-file <path>.")
		}
		path = strings.Trim(strings.TrimSpace(answer), "\"'")
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return auth.ClientConfig{}, err
		}
		path = filepath.Join(home, path[2:])
	}
	if path == "" {
		return auth.ClientConfig{}, auth.RequireBYO(auth.ClientConfig{})
	}
	c, err := auth.LoadClientFile(path)
	if err != nil {
		return c, err
	}
	return c, auth.RequireBYO(c)
}
func savedCredentialsMessage(a *app, s *auth.Store) {
	fmt.Fprintf(a.stderr, "Credentials saved and verified (%s). You can delete the downloaded OAuth JSON; SearchProbe reuses its saved client for future sign-ins.\n", s.Backend())
	if s.Backend() == "file" {
		fmt.Fprintln(a.stderr, "Storage is plaintext, protected by user-only filesystem permissions.")
	}
}
