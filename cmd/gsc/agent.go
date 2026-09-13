package main

import (
	"fmt"
	"io"

	"github.com/morgancrozier/searchprobe/internal/agent"
	"github.com/morgancrozier/searchprobe/internal/gscerr"
	"github.com/spf13/cobra"
)

func newAgentCmd(a *app) *cobra.Command {
	parent := &cobra.Command{Use: "agent", Short: "Install the SearchProbe skill for local Claude Code and Codex"}
	for _, action := range []string{"install", "status", "uninstall"} {
		action := action
		var selection string
		cmd := &cobra.Command{
			Use: action, Short: action + " the personal SearchProbe agent skill",
			Long: "Manage the bundled SearchProbe skill across projects. Auto installation detects\nagent executables on PATH or existing configuration directories. Use --agent\nclaude, codex, or all to install explicitly, including before the agent exists.\nStatus checks files, not agent activation, PATH correctness, or Google access.\nModified/unmanaged skills and symlinks are preserved as conflicts. No agent\nsettings, credentials, or project instructions are changed. After upgrading gsc,\nrerun install; restart the agent if discovery or PATH changes do not appear.",
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				envFn := a.agentEnvironment
				if envFn == nil {
					envFn = agent.DefaultEnvironment
				}
				env, err := envFn()
				if err != nil {
					return gscerr.Wrap(err, gscerr.CodeConfigError, "Cannot locate agent directories.", "Check HOME and agent configuration paths.")
				}
				targets, err := env.Targets()
				if err != nil {
					return gscerr.Wrap(err, gscerr.CodeConfigError, err.Error(), "Use absolute configuration paths.")
				}
				targets, err = agent.Select(targets, selection)
				if err != nil {
					return gscerr.New(gscerr.CodeInvalidArgument, err.Error(), "")
				}
				results := make([]agent.Target, 0, len(targets))
				failed, eligible := false, 0
				for _, target := range targets {
					if action == "install" && selection == "auto" && !target.Detected {
						target = agent.Status(target)
						target.Detail = "Agent not detected; skipped. Use --agent " + target.Agent + " to install explicitly."
						results = append(results, target)
						continue
					}
					eligible++
					result, applyErr := agent.Apply(target, action)
					if applyErr != nil {
						failed = true
						result.Detail = applyErr.Error()
					}
					results = append(results, result)
				}
				if failed || (action == "install" && eligible == 0) {
					// Failure envelopes keep their existing shape; report each outcome in the
					// error so partial installations are visible to JSON and human consumers.
					message := "Agent skill operation could not complete."
					for _, r := range results {
						message += fmt.Sprintf(" %s: %s (%s). %s", r.Agent, r.State, r.Path, r.Detail)
					}
					return gscerr.New(gscerr.CodeConfigError, message, "Inspect gsc agent status; resolve conflicts or select --agent claude|codex|all explicitly.")
				}
				return a.emit(map[string]any{"agents": results}, map[string]any{"action": action}, nil, func(w io.Writer) {
					for _, r := range results {
						fmt.Fprintf(w, "%s: detected=%t skill=%s\n  %s\n", r.Agent, r.Detected, r.State, r.Path)
						if r.Detail != "" {
							fmt.Fprintln(w, "  "+r.Detail)
						}
					}
					if action == "install" {
						fmt.Fprintln(w, "Ensure gsc --version identifies SearchProbe in the agent's shell. Restart the agent if the skill does not appear; authentication is separate (gsc auth login).")
					}
				})
			},
		}
		cmd.Flags().StringVar(&selection, "agent", "auto", "agent selection: auto, all, claude, codex")
		parent.AddCommand(cmd)
	}
	return parent
}
