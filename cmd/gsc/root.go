package main

// The CLI is wired here. Commands are thin: they parse flags, call the
// reusable auth and Search Console packages, and render results.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/morgancrozier/searchprobe/internal/agent"
	"github.com/morgancrozier/searchprobe/internal/auth"
	"github.com/morgancrozier/searchprobe/internal/gscerr"
	"github.com/morgancrozier/searchprobe/internal/output"
	"github.com/morgancrozier/searchprobe/internal/searchconsole"
)

// Version may be set at build time via -ldflags "-X main.Version=...".
// When unset, resolveVersion derives it from Go build info so that
// `go install` builds still report something meaningful.
var Version = "dev"

// deps are the injectable collaborators used by commands; tests override them.
type deps struct {
	interactive      func() bool
	agentEnvironment func() (agent.Environment, error)
	stdout           io.Writer
	stderr           io.Writer
	stdin            io.Reader
	now              func() time.Time
	store            func() (*auth.Store, error)
	client           func(ctx context.Context) (*searchconsole.Client, error)
	login            func(ctx context.Context, opts auth.LoginOptions) (*auth.Credentials, error)
	revoke           func(ctx context.Context, token string) error
	openBrowser      func(url string) error
}

func defaultDeps() *deps {
	d := &deps{
		interactive: terminalInput,
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		stdin:       os.Stdin,
		now:         time.Now,
		store:       auth.DefaultStore,
		login:       auth.Login,
		openBrowser: auth.OpenBrowser,
		revoke: func(ctx context.Context, token string) error {
			return auth.Revoke(ctx, nil, token)
		},
	}
	d.client = func(ctx context.Context) (*searchconsole.Client, error) {
		store, err := d.store()
		if err != nil {
			return nil, err
		}
		creds, err := store.Load()
		if err != nil {
			return nil, err
		}
		if err := auth.RequireBYO(creds.Client); err != nil {
			return nil, err
		}
		if !creds.HasReadOnlyScope() {
			return nil, gscerr.New(gscerr.CodeAuthScopeInsufficient,
				"Stored credentials do not include the read-only Search Console scope.",
				"Run `gsc auth login --client-file <path>` again.")
		}
		return searchconsole.New(auth.HTTPClient(ctx, creds, store)), nil
	}
	return d
}

// app carries per-invocation state shared by commands.
type app struct {
	*deps
	json bool
}

// emit renders a successful result in JSON or human form.
func (a *app) emit(data, meta any, warnings []output.Warning, human func(w io.Writer)) error {
	if a.json {
		return output.WriteJSON(a.stdout, output.Success(data, meta, warnings))
	}
	human(a.stdout)
	for _, w := range warnings {
		fmt.Fprintf(a.stderr, "Warning: %s\n", w.Message)
	}
	return nil
}

func newRoot(d *deps) (*cobra.Command, *app) {
	if d.stdin != nil {
		if _, ok := d.stdin.(*bufio.Reader); !ok {
			d.stdin = bufio.NewReader(d.stdin)
		}
	}
	a := &app{deps: d}
	root := &cobra.Command{
		Use:   "gsc",
		Short: "SearchProbe: Google Search Console for agents",
		Long: `SearchProbe (gsc) is a local-first, read-only Google Search Console CLI for developers,
scripts, and coding agents.

It requests only the read-only Search Console OAuth scope, talks directly to
Google's API from this machine, and emits stable JSON with --json.

First run:
  gsc setup

Setup signs in, verifies Google access, and prepares Claude Code/Codex skills.

Discover: gsc <command> --help. Use --site exactly as returned by gsc sites
(sc-domain:example.com or https://example.com/). Prefer --json for agent use;
read meta and warnings before interpreting data. No live test or indexing writes.

Exit codes: 0 success, 1 failure, 2 invalid arguments, 3 authentication required.`,
		Version:       resolveVersion(),
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}
	root.SetVersionTemplate("SearchProbe gsc version {{.Version}}\n")
	root.SetOut(d.stdout)
	root.SetErr(d.stderr)
	root.PersistentFlags().BoolVar(&a.json, "json", false, "emit machine-readable JSON on stdout")

	root.AddCommand(newSetupCmd(a), newAgentCmd(a), newAuthCmd(a), newSitesCmd(a), newPerformanceCmd(a), newCompareCmd(a), newInspectCmd(a), newSitemapsCmd(a), newSitemapCmd(a))
	return root, a
}

// Execute runs the CLI with the given arguments and returns the exit code.
func Execute(args []string) int {
	return run(args, defaultDeps())
}

func run(args []string, d *deps) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	root, a := newRoot(d)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return gscerr.ExitOK
	}

	// If flag parsing failed, the --json flag may not have been bound yet.
	if !a.json {
		for _, arg := range args {
			if arg == "--json" || arg == "--json=true" {
				a.json = true
			}
		}
	}

	ge := normalizeCLIError(err)
	if a.json {
		_ = output.WriteJSON(d.stdout, output.Failure(ge))
	} else {
		fmt.Fprintf(d.stderr, "Error: %s\n", ge.Message)
		if ge.Action != "" {
			fmt.Fprintf(d.stderr, "Next: %s\n", ge.Action)
		}
	}
	return gscerr.ExitCode(ge)
}

// normalizeCLIError turns cobra usage errors into INVALID_ARGUMENT.
func normalizeCLIError(err error) *gscerr.Error {
	var ge *gscerr.Error
	if errors.As(err, &ge) {
		return ge
	}
	msg := err.Error()
	if len(msg) > 0 {
		msg = strings.ToUpper(msg[:1]) + msg[1:]
	}
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return gscerr.Wrap(err, gscerr.CodeInvalidArgument, msg, "Run `gsc --help` for usage.")
}
