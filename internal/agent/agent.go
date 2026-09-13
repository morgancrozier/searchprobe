// Package agent installs the bundled skill without changing agent configuration.
package agent

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	skill "github.com/morgancrozier/searchprobe/skills/searchprobe"
)

const receipt = ".searchprobe-sha256"

type Environment struct {
	Home         string
	ClaudeConfig string
	CodexHome    string
	LookPath     func(string) (string, error)
}

type Target struct {
	Agent    string `json:"agent"`
	Detected bool   `json:"detected"`
	Path     string `json:"path"`
	State    string `json:"state"`
	Detail   string `json:"detail,omitempty"`
}

func DefaultEnvironment() (Environment, error) {
	home, err := os.UserHomeDir()
	return Environment{home, os.Getenv("CLAUDE_CONFIG_DIR"), os.Getenv("CODEX_HOME"), exec.LookPath}, err
}

func (e Environment) Targets() ([]Target, error) {
	if !filepath.IsAbs(e.Home) {
		return nil, fmt.Errorf("home must be an absolute path")
	}
	resolved, err := filepath.EvalSymlinks(e.Home)
	if err != nil {
		return nil, err
	}
	e.Home = resolved
	claude := e.ClaudeConfig
	if claude == "" {
		claude = filepath.Join(e.Home, ".claude")
	}
	codex := e.CodexHome
	if codex == "" {
		codex = filepath.Join(e.Home, ".codex")
	}
	if !filepath.IsAbs(claude) || !filepath.IsAbs(codex) {
		return nil, fmt.Errorf("agent configuration directories must be absolute paths")
	}
	detected := func(command, config string) bool {
		if e.LookPath != nil {
			if _, err := e.LookPath(command); err == nil {
				return true
			}
		}
		st, err := os.Stat(config)
		return err == nil && st.IsDir()
	}
	return []Target{
		{Agent: "claude", Detected: detected("claude", claude), Path: filepath.Join(claude, "skills", "searchprobe")},
		{Agent: "codex", Detected: detected("codex", codex), Path: filepath.Join(e.Home, ".agents", "skills", "searchprobe")},
	}, nil
}

func digest(content string) string { return fmt.Sprintf("%x\n", sha256.Sum256([]byte(content))) }

// Refuse symlinks below the selected home/config root, including dangling links.
// System ancestors (e.g. macOS /var) are resolved separately by the caller's OS.
func checkPath(path string) error {
	for p := filepath.Clean(path); p != filepath.Dir(p); p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink at %s", p)
		}
	}
	return nil
}

func Status(t Target) Target {
	t.State = "missing"
	if err := checkPath(t.Path); err != nil {
		t.State, t.Detail = "conflict", err.Error()
		return t
	}
	entries, err := os.ReadDir(t.Path)
	if os.IsNotExist(err) {
		return t
	}
	if err != nil {
		t.State, t.Detail = "conflict", err.Error()
		return t
	}
	t.State = "conflict"
	t.Detail = "Existing skill is unmanaged or modified; back it up and move it aside before installing."
	if len(entries) != 2 {
		return t
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || (entry.Name() != "SKILL.md" && entry.Name() != receipt) {
			return t
		}
	}
	content, err := os.ReadFile(filepath.Join(t.Path, "SKILL.md"))
	if err != nil {
		return t
	}
	hash, err := os.ReadFile(filepath.Join(t.Path, receipt))
	if err != nil || string(hash) != digest(string(content)) {
		return t
	}
	t.State, t.Detail = "outdated", "Run gsc agent install to update the managed skill."
	if string(content) == skill.Content {
		t.State, t.Detail = "installed", ""
	}
	return t
}

func Apply(t Target, action string) (Target, error) {
	t = Status(t)
	if action == "status" {
		return t, nil
	}
	if action != "install" && action != "uninstall" {
		return t, fmt.Errorf("unsupported action %q", action)
	}
	if t.State == "conflict" {
		return t, fmt.Errorf("%s: %s", t.Path, t.Detail)
	}
	if action == "install" && t.State == "installed" {
		return t, nil
	}
	if action == "uninstall" && t.State == "missing" {
		return t, nil
	}
	if err := os.MkdirAll(filepath.Dir(t.Path), 0755); err != nil {
		return t, err
	}
	// Serialize our own installers; stale locks are reported, never stolen.
	lock := t.Path + ".lock"
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return t, fmt.Errorf("cannot lock skill installation at %s: %w", lock, err)
	}
	f.Close()
	defer os.Remove(lock)
	t = Status(t)
	if t.State == "conflict" {
		return t, fmt.Errorf("%s: %s", t.Path, t.Detail)
	}
	if action == "uninstall" {
		if t.State == "missing" {
			return t, nil
		}
		// Only the two verified owned files; never recursively delete a user directory.
		for _, name := range []string{"SKILL.md", receipt} {
			if err := os.Remove(filepath.Join(t.Path, name)); err != nil {
				return t, err
			}
		}
		if err := os.Remove(t.Path); err != nil {
			return t, err
		}
		t.State, t.Detail = "removed", ""
		return t, nil
	}
	if t.State == "installed" {
		return t, nil
	}
	staging, err := os.MkdirTemp(filepath.Dir(t.Path), ".searchprobe-")
	if err != nil {
		return t, err
	}
	defer os.RemoveAll(staging)
	for name, content := range map[string]string{"SKILL.md": skill.Content, receipt: digest(skill.Content)} {
		if err := os.WriteFile(filepath.Join(staging, name), []byte(content), 0644); err != nil {
			return t, err
		}
	}
	if t.State == "missing" {
		err = os.Rename(staging, t.Path)
	} else {
		// Each replacement is atomic. Interruption between them leaves a conflict,
		// conservatively preserving files on the next run instead of guessing ownership.
		for _, name := range []string{"SKILL.md", receipt} {
			if err = os.Rename(filepath.Join(staging, name), filepath.Join(t.Path, name)); err != nil {
				break
			}
		}
	}
	if err != nil {
		return t, err
	}
	t.State, t.Detail = "installed", ""
	return t, nil
}

func Select(targets []Target, selection string) ([]Target, error) {
	switch selection {
	case "auto", "all":
		return targets, nil
	case "claude", "codex":
		for _, t := range targets {
			if t.Agent == selection {
				return []Target{t}, nil
			}
		}
	}
	return nil, fmt.Errorf("unsupported agent %q; choose auto, all, claude, or codex", strings.TrimSpace(selection))
}
