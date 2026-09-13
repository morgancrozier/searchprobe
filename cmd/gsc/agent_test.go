package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morgancrozier/searchprobe/internal/agent"
)

func TestAgentCommands(t *testing.T) {
	h := newHarness(t, nil)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := agent.Environment{Home: home, LookPath: func(string) (string, error) { return "", os.ErrNotExist }}
	h.deps.agentEnvironment = func() (agent.Environment, error) { return env, nil }
	if code := h.run("agent", "install", "--json"); code != 1 || !strings.Contains(h.stdout.String(), "CONFIG_ERROR") {
		t.Fatal(code, h.stdout.String())
	}
	if code := h.run("agent", "install", "--agent", "all", "--json"); code != 0 || !strings.Contains(h.stdout.String(), `"ok": true`) {
		t.Fatal(code, h.stdout.String())
	}
	if code := h.run("agent", "status", "--json"); code != 0 || strings.Count(h.stdout.String(), `"state": "installed"`) != 2 {
		t.Fatal(code, h.stdout.String())
	}
	if code := h.run("agent", "install", "--agent", "unknown", "--json"); code != 2 {
		t.Fatal(code, h.stdout.String())
	}
	if code := h.run("agent", "uninstall", "--json"); code != 0 {
		t.Fatal(code, h.stdout.String())
	}
	// Auto mode installs only detected agents, and reports the missing one.
	env.LookPath = func(name string) (string, error) {
		if name == "claude" {
			return "/bin/claude", nil
		}
		return "", os.ErrNotExist
	}
	// Remove the empty config directory created during the explicit install.
	os.Remove(filepath.Join(home, ".claude/skills"))
	os.Remove(filepath.Join(home, ".claude"))
	if code := h.run("agent", "install", "--json"); code != 0 || !strings.Contains(h.stdout.String(), "skipped") {
		t.Fatal(code, h.stdout.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".agents/skills/searchprobe")); !os.IsNotExist(err) {
		t.Fatal("auto installed undetected Codex")
	}
}

func TestAgentPartialFailureIsVisible(t *testing.T) {
	h := newHarness(t, nil)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.deps.agentEnvironment = func() (agent.Environment, error) { return agent.Environment{Home: home}, nil }
	custom := filepath.Join(home, ".claude/skills/searchprobe")
	if err := os.MkdirAll(custom, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(custom, "SKILL.md"), []byte("user skill"), 0600); err != nil {
		t.Fatal(err)
	}
	code := h.run("agent", "install", "--agent", "all", "--json")
	if code != 1 || !strings.Contains(h.stdout.String(), "claude: conflict") || !strings.Contains(h.stdout.String(), "codex: installed") {
		t.Fatal(code, h.stdout.String())
	}
	data, err := os.ReadFile(filepath.Join(custom, "SKILL.md"))
	if err != nil || string(data) != "user skill" {
		t.Fatal("overwrote user skill")
	}
}
