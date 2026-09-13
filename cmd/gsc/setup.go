package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/morgancrozier/searchprobe/internal/agent"
	"github.com/morgancrozier/searchprobe/internal/auth"
	"github.com/morgancrozier/searchprobe/internal/gscerr"
	"github.com/morgancrozier/searchprobe/internal/output"
	"github.com/morgancrozier/searchprobe/internal/searchconsole"
	"github.com/spf13/cobra"
)

func newSetupCmd(a *app) *cobra.Command {
	var selection string
	var clientFile, credentialStore string
	var noBrowser bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use: "setup", Short: "Sign in, verify Google access, and prepare Claude Code or Codex",
		Long: `Set up SearchProbe on this machine. Reuses working credentials, verifies
property access with Google, and installs or updates the bundled personal skill.
Existing modified skills are preserved. Rerun after an interruption or upgrade.

In a terminal, choose agents after reviewing their detected state and destinations.
--agent explicitly authorizes skill installation without a selection prompt.
Use --agent none for terminal-only setup. Package installation never runs setup.

JSON/non-interactive use requires --agent and existing working authentication;
it never starts browser sign-in. Run gsc auth login separately in that case.
Import your Desktop OAuth client JSON once with --client-file <path>.
OS storage is preferred; --credential-store file selects protected plaintext storage.
--no-browser still requires consent and a loopback callback on this machine.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			interactive := !a.json && a.interactive != nil && a.interactive()
			if !interactive && !cmd.Flags().Changed("agent") {
				return gscerr.New(gscerr.CodeInvalidArgument, "Non-interactive setup requires an explicit agent selection.", "Run gsc setup in a terminal, or use gsc setup --agent claude|codex|all|auto|none after gsc auth login.")
			}
			// Validate choices before authentication or any filesystem mutation.
			switch selection {
			case "auto", "all", "claude", "codex", "none":
			default:
				return gscerr.New(gscerr.CodeInvalidArgument, "Unknown agent selection.", "Choose auto, claude, codex, all, or none.")
			}
			targets := []agent.Target{}
			if selection != "none" {
				envFn := a.agentEnvironment
				if envFn == nil {
					envFn = agent.DefaultEnvironment
				}
				env, err := envFn()
				if err != nil {
					return gscerr.Wrap(err, gscerr.CodeConfigError, "Cannot locate agent directories.", "Check HOME and agent configuration paths.")
				}
				targets, err = env.Targets()
				if err != nil {
					return gscerr.Wrap(err, gscerr.CodeConfigError, err.Error(), "Use absolute agent configuration paths.")
				}
				for i := range targets {
					targets[i] = agent.Status(targets[i])
				}
			}
			fmt.Fprintln(a.stderr, "SearchProbe setup · read-only Google access, local credentials, no MCP")
			if interactive && !cmd.Flags().Changed("agent") {
				defaults := []string{}
				for _, t := range targets {
					detection := "not detected"
					if t.Detected {
						detection = "found"
					}
					fmt.Fprintf(a.stderr, "  %s: %s; skill %s\n    %s\n", setupAgentName(t.Agent), detection, t.State, t.Path)
					if setupAutoTarget(t) {
						defaults = append(defaults, t.Agent)
					}
				}
				def := "all"
				if len(defaults) == 1 {
					def = defaults[0]
				}
				if len(defaults) == 0 {
					fmt.Fprintln(a.stderr, "No agents detected. You can prepare skills before installing the agent.")
				}
				fmt.Fprintf(a.stderr, "Set up [claude/codex/all/none] (none = terminal only) (%s): ", def)
				answer, err := setupAnswer(cmd.Context(), a.stdin)
				if err != nil {
					return gscerr.New(gscerr.CodeInvalidArgument, "Setup stopped before making changes: no selection received.", "Rerun gsc setup; choose none for terminal-only use.")
				}
				selection = strings.ToLower(strings.TrimSpace(answer))
				if selection == "" {
					selection = def
				}
			}
			chosen := []agent.Target{}
			if selection != "none" {
				selected, err := agent.Select(targets, selection)
				if err != nil {
					return gscerr.New(gscerr.CodeInvalidArgument, err.Error(), "Rerun gsc setup.")
				}
				for _, t := range selected {
					if selection != "auto" || setupAutoTarget(t) {
						chosen = append(chosen, t)
					}
				}
				if len(chosen) == 0 {
					return gscerr.New(gscerr.CodeConfigError, "No supported agents detected; no changes made.", "Run gsc setup --agent claude|codex|all, or --agent none for terminal-only use.")
				}
			}
			// Preflight conflicts before asking the user to authenticate.
			for _, t := range chosen {
				fmt.Fprintf(a.stderr, "Agent: %s (%s) → %s\n", t.Agent, t.State, t.Path)
				if t.State == "conflict" {
					return gscerr.New(gscerr.CodeConfigError, fmt.Sprintf("%s: %s (%s)", t.Agent, t.Detail, t.Path), "Inspect gsc agent status; preserve and move the conflicting directory aside, then rerun gsc setup.")
				}
			}
			store, err := a.store()
			if err != nil {
				return err
			}
			creds, err := store.Load()
			if err != nil && gscerr.From(err).Code != gscerr.CodeAuthRequired {
				return err
			}
			needsLogin := err != nil || creds == nil || !creds.HasReadOnlyScope() || (creds != nil && auth.RequireBYO(creds.Client) != nil) || cmd.Flags().Changed("client-file")
			if needsLogin && !interactive {
				return gscerr.New(gscerr.CodeAuthRequired, "Setup needs Google sign-in; no browser was opened.", "Run gsc auth login --client-file <path>, then rerun setup with --agent.")
			}
			clientConfig, err := selectOAuthClient(cmd, a, creds, clientFile, cmd.Flags().Changed("client-file"))
			if err != nil {
				return err
			}
			destination, err := selectCredentialStore(cmd, a, store, credentialStore)
			if err != nil {
				return err
			}
			storageChanged := (interactive && store.LegacyFile()) || cmd.Flags().Changed("credential-store")
			store = destination
			signIn := func() error {
				if !interactive {
					return gscerr.New(gscerr.CodeAuthRequired, "Setup needs Google sign-in; no browser was opened.", "Run gsc auth login (repeat --client-file for BYO), then rerun setup with --agent.")
				}
				fmt.Fprintln(a.stderr, "Sign in to Google in your browser. Credentials stay on this machine.")
				opts := auth.LoginOptions{Client: clientConfig, Stderr: a.stderr, Timeout: timeout}
				if !noBrowser {
					opts.OpenBrowser = a.openBrowser
				}
				c, err := a.login(cmd.Context(), opts)
				if err != nil {
					return err
				}
				if err := store.Save(c); err != nil {
					return err
				}
				savedCredentialsMessage(a, store)
				return nil
			}
			if needsLogin {
				if err := signIn(); err != nil {
					return err
				}
			}
			if !needsLogin && storageChanged {
				if err := store.Save(creds); err != nil {
					return err
				}
			}
			// Make the selected store available to the normal API client resolver.
			originalStore := a.deps.store
			a.deps.store = func() (*auth.Store, error) { return store, nil }
			defer func() { a.deps.store = originalStore }()
			fmt.Fprintln(a.stderr, "Checking Google Search Console access…")
			sites, err := setupSites(cmd.Context(), a)
			if err != nil && !needsLogin && setupAuthError(err) {
				if err := signIn(); err != nil {
					return err
				}
				sites, err = setupSites(cmd.Context(), a)
			}
			if err != nil {
				return err
			}
			results := make([]agent.Target, 0, len(chosen))
			for _, t := range chosen {
				result, err := agent.Apply(t, "install")
				if err != nil {
					return gscerr.Wrap(err, gscerr.CodeConfigError, fmt.Sprintf("Google access verified; %s skill could not be installed at %s: %s. Earlier completed steps are retained.", t.Agent, t.Path, err), "Inspect gsc agent status and rerun gsc setup; completed steps are safe to repeat.")
				}
				results = append(results, result)
			}
			usable := []searchconsole.Site{}
			for _, s := range sites {
				if s.PermissionLevel == "siteOwner" || s.PermissionLevel == "siteFullUser" || s.PermissionLevel == "siteRestrictedUser" {
					usable = append(usable, s)
				}
			}
			prompt := "Use SearchProbe to list my Search Console properties, ask which matches this repository, then compare page performance over the last 28 days with the previous 28 days. Show the largest click declines and distinguish Google evidence from repository hypotheses."
			if len(usable) == 1 {
				prompt = fmt.Sprintf("Use SearchProbe to compare page performance for %s over the last 28 days with the previous 28 days. Show the largest click declines and distinguish Google evidence from repository hypotheses.", usable[0].SiteURL)
			}
			next := "Start a fresh Claude Code or Codex session in your website repository and paste the prompt below. Ensure gsc --version identifies SearchProbe in the agent's shell; restart the agent after PATH changes. Skill files are verified; agent activation is not checked."
			if selection == "none" {
				next = "Terminal setup verified. Run gsc sites --json, then use a property identifier with gsc performance --site <property> --days 28 --dimensions page --json."
				prompt = ""
			}
			var warnings []output.Warning
			if len(usable) == 0 {
				next = "No accessible verified properties found. Add/verify your site in Google Search Console or ask its owner for access. If this is the wrong account, run gsc auth login. Then rerun gsc setup."
				prompt = ""
				warnings = append(warnings, output.Warning{Code: "NO_ACCESSIBLE_PROPERTIES", Message: next})
			}
			return a.emit(map[string]any{"ready": len(usable) > 0, "authenticated": true, "agents": results, "sites": sites, "firstPrompt": prompt, "nextStep": next}, map[string]any{"agentSelection": selection, "googleAccessVerified": true, "agentActivationVerified": false}, warnings, func(w io.Writer) {
				fmt.Fprintln(w, "✓ Google access verified")
				for _, t := range results {
					fmt.Fprintf(w, "✓ %s skill ready: %s\n", t.Agent, t.Path)
				}
				fmt.Fprintf(w, "%d accessible verified properties\n\n%s\n", len(usable), next)
				if prompt != "" {
					fmt.Fprintf(w, "\nTry this first:\n%s\n", prompt)
				}
			})
		},
	}
	cmd.Flags().StringVar(&clientFile, "client-file", "", "import your Google Cloud Desktop OAuth client JSON once")
	cmd.Flags().StringVar(&credentialStore, "credential-store", "auto", "credential storage: auto, keychain, or file (plaintext)")
	cmd.Flags().StringVar(&selection, "agent", "auto", "install skills for auto, claude, codex, all, or none (explicit selection skips prompt)")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the sign-in URL instead of opening a browser")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "how long to wait for browser sign-in")
	return cmd
}

func setupSites(ctx context.Context, a *app) ([]searchconsole.Site, error) {
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	return client.ListSites(ctx)
}

func setupAuthError(err error) bool {
	switch gscerr.From(err).Code {
	case gscerr.CodeAuthRequired, gscerr.CodeAuthRevoked, gscerr.CodeAuthScopeInsufficient:
		return true
	}
	return false
}

func terminalInput() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0 && info.Name() != "null"
}

// Keep explicitly prepared skills in subsequent automatic setup runs even when
// the agent executable/configuration has not appeared yet (e.g. desktop apps).
func setupAutoTarget(t agent.Target) bool {
	return t.Detected || t.State != "missing"
}

func setupAgentName(name string) string {
	if name == "claude" {
		return "Claude Code"
	}
	return "Codex"
}

func setupAnswer(ctx context.Context, input io.Reader) (string, error) {
	type answer struct {
		text string
		err  error
	}
	result := make(chan answer, 1)
	go func() {
		reader, ok := input.(*bufio.Reader)
		if !ok {
			reader = bufio.NewReader(input)
		}
		text, err := reader.ReadString('\n')
		result <- answer{text, err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case a := <-result:
		return a.text, a.err
	}
}
