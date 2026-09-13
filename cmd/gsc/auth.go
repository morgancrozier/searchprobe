package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/morgancrozier/searchprobe/internal/auth"
	"github.com/morgancrozier/searchprobe/internal/gscerr"
	"github.com/morgancrozier/searchprobe/internal/output"
)

func newAuthCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage Google authentication",
		Long: `Authenticate gsc with your Google account.

gsc requests only the read-only Search Console scope
(https://www.googleapis.com/auth/webmasters.readonly). Credentials use the OS
credential store or an explicitly selected protected plaintext file and are
refreshed automatically.

Start: gsc setup --client-file <downloaded-client.json>.
Check: gsc auth status (local check); gsc sites --json (verify Google access).
Remove: gsc auth logout before uninstalling to revoke and remove credentials.`,
	}
	cmd.AddCommand(newAuthLoginCmd(a), newAuthStatusCmd(a), newAuthLogoutCmd(a))
	return cmd
}

func newAuthLoginCmd(a *app) *cobra.Command {
	var clientFile string
	var credentialStore string
	var noBrowser bool
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in with Google using a Desktop OAuth client",
		Long: `Sign in with your Google Cloud Desktop OAuth client. Import its JSON once with
--client-file; later sign-ins reuse the saved client. Access is read-only;
credentials stay on this machine and requests go directly to Google.

The flow uses PKCE (S256), offline access, and a 127.0.0.1 loopback callback.
Complete consent in a browser on this machine.
--no-browser prints the URL; it does not remove the local callback requirement.
After signing in, run gsc setup to verify access and prepare Claude Code/Codex.

OS credential storage is preferred. Select --credential-store file explicitly
for plaintext storage protected by user-only filesystem permissions.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := a.store()
			if err != nil {
				return err
			}
			creds, err := store.Load()
			if err != nil && gscerr.From(err).Code != gscerr.CodeAuthRequired {
				return err
			}
			client, err := selectOAuthClient(cmd, a, creds, clientFile, cmd.Flags().Changed("client-file"))
			if err != nil {
				return err
			}
			store, err = selectCredentialStore(cmd, a, store, credentialStore)
			if err != nil {
				return err
			}
			opts := auth.LoginOptions{Client: client, Stderr: a.stderr, Timeout: timeout}
			if !noBrowser {
				opts.OpenBrowser = a.openBrowser
			}
			creds, err = a.login(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if err := store.Save(creds); err != nil {
				return err
			}
			savedCredentialsMessage(a, store)
			data := map[string]any{
				"authenticated":   true,
				"credentialsPath": store.CredentialsPath(), "credentialStore": store.Backend(),
				"clientId": creds.Client.ClientID,
				"scopes":   creds.Scopes,
			}
			return a.emit(data, nil, nil, func(w io.Writer) {
				fmt.Fprintln(w, "✓ Signed in to SearchProbe")
				fmt.Fprintln(w, "  Access: Google Search Console (read-only)")
				fmt.Fprintf(w, "  Credentials: %s\n", credentialLocation(store))
				fmt.Fprintln(w, "  Next: gsc setup — verify access and prepare Claude Code/Codex.")
			})
		},
	}
	cmd.Flags().StringVar(&clientFile, "client-file", "", "import your Google Cloud Desktop OAuth client JSON once")
	cmd.Flags().StringVar(&credentialStore, "credential-store", "auto", "credential storage: auto, keychain, or file (plaintext)")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the sign-in URL instead of opening a browser")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "how long to wait for the browser sign-in")
	return cmd
}

func newAuthStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether SearchProbe is signed in (no network call)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := a.store()
			if err != nil {
				return err
			}
			creds, err := store.Load()
			if err != nil {
				if gscerr.From(err).Code == gscerr.CodeAuthRequired {
					return gscerr.New(gscerr.CodeAuthRequired, "SearchProbe is not signed in.", "Run `gsc auth login`.")
				}
				return err
			}
			if err := auth.RequireBYO(creds.Client); err != nil {
				return err
			}
			now := a.now()
			valid := creds.Token.AccessToken != "" && creds.Token.Expiry.After(now.Add(10*time.Second))
			data := map[string]any{
				"authenticated":   true,
				"credentialsPath": store.CredentialsPath(), "credentialStore": store.Backend(),
				"clientId":             creds.Client.ClientID,
				"projectId":            creds.Client.ProjectID,
				"scopes":               creds.Scopes,
				"readOnly":             creds.HasReadOnlyScope(),
				"hasRefreshToken":      creds.Token.RefreshToken != "",
				"accessTokenValid":     valid,
				"accessTokenExpiresAt": creds.Token.Expiry.UTC().Format(time.RFC3339),
				"createdAt":            creds.CreatedAt.UTC().Format(time.RFC3339),
				"updatedAt":            creds.UpdatedAt.UTC().Format(time.RFC3339),
			}
			var warnings []output.Warning
			if !creds.HasReadOnlyScope() {
				warnings = append(warnings, output.Warning{Code: gscerr.CodeAuthScopeInsufficient, Message: "Stored credentials do not include the read-only Search Console scope; run `gsc auth login` again."})
			}
			return a.emit(data, nil, warnings, func(w io.Writer) {
				fmt.Fprintln(w, "Signed in")
				fmt.Fprintln(w)
				fmt.Fprintf(w, "OAuth:       %s\n", oauthLabel(creds.Client))
				fmt.Fprintln(w, "Access:      Google Search Console (read-only)")
				fmt.Fprintf(w, "Refresh:     %s\n", availability(creds.Token.RefreshToken != ""))
				fmt.Fprintf(w, "Credentials: %s\n", credentialLocation(store))
			})
		},
	}
}

func newAuthLogoutCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Revoke the Google grant (best effort) and delete local credentials",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := a.store()
			if err != nil {
				return err
			}
			var warnings []output.Warning
			revoked := false
			creds, loadErr := store.Load()
			if loadErr == nil {
				if err := a.revoke(cmd.Context(), creds.Token.RefreshToken); err != nil {
					warnings = append(warnings, output.Warning{
						Code:    "REVOKE_FAILED",
						Message: "Could not revoke the Google grant. Local credentials were still removed; you can revoke access at https://myaccount.google.com/permissions.",
					})
				} else {
					revoked = true
				}
			}
			removed, err := store.Delete()
			if err != nil {
				return err
			}
			data := map[string]any{
				"authenticated":      false,
				"revoked":            revoked,
				"removedCredentials": removed,
				"credentialsPath":    store.CredentialsPath(), "credentialStore": store.Backend(),
			}
			return a.emit(data, nil, warnings, func(w io.Writer) {
				switch {
				case removed && revoked:
					fmt.Fprintln(w, "Signed out of SearchProbe.")
					fmt.Fprintln(w, "Google access was revoked and local credentials were removed.")
				case removed:
					fmt.Fprintln(w, "Signed out of SearchProbe.")
					fmt.Fprintln(w, "Local credentials were removed. Google access could not be confirmed as revoked.")
				default:
					fmt.Fprintln(w, "SearchProbe is not signed in; nothing to remove.")
				}
			})
		},
	}
}

func oauthLabel(client auth.ClientConfig) string {
	if client.ClientID == auth.LegacyClientID {
		return "Retired SearchProbe identity: BYO setup required"
	}
	return "Custom Google client"
}

func availability(available bool) string {
	if available {
		return "available"
	}
	return "unavailable"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func displayPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	cleanPath := filepath.Clean(path)
	cleanHome := filepath.Clean(home)
	if cleanPath == cleanHome {
		return "~"
	}
	prefix := cleanHome + string(filepath.Separator)
	if strings.HasPrefix(cleanPath, prefix) {
		return "~" + string(filepath.Separator) + strings.TrimPrefix(cleanPath, prefix)
	}
	return path
}

func credentialLocation(s *auth.Store) string {
	if s.Backend() == "keychain" {
		return "OS credential store (keychain)"
	}
	return displayPath(s.Path)
}
