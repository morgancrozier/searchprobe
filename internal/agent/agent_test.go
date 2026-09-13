package agent

import (
	"os"
	"path/filepath"
	"testing"

	skill "github.com/morgancrozier/searchprobe/skills/searchprobe"
)

func environment(t *testing.T) Environment {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return Environment{Home: home, LookPath: func(string) (string, error) { return "", os.ErrNotExist }}
}

func TestLifecycle(t *testing.T) {
	for _, selection := range []string{"claude", "codex", "all"} {
		t.Run(selection, func(t *testing.T) {
			env := environment(t)
			targets, err := env.Targets()
			if err != nil {
				t.Fatal(err)
			}
			targets, err = Select(targets, selection)
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range targets {
				if target.Detected {
					t.Fatal("detected nonexistent agent")
				}
				if got := Status(target); got.State != "missing" {
					t.Fatal(got)
				}
				for i := 0; i < 2; i++ {
					got, err := Apply(target, "install")
					if err != nil || got.State != "installed" {
						t.Fatal(got, err)
					}
				}
				body, err := os.ReadFile(filepath.Join(target.Path, "SKILL.md"))
				if err != nil || string(body) != skill.Content {
					t.Fatal("not canonical", err)
				}
				for i := 0; i < 2; i++ {
					_, err := Apply(target, "uninstall")
					if err != nil {
						t.Fatal(err)
					}
				}
				if Status(target).State != "missing" {
					t.Fatal("not removed")
				}
			}
			other := "codex"
			if selection == "codex" {
				other = "claude"
			}
			if selection != "all" {
				all, _ := env.Targets()
				for _, target := range all {
					if target.Agent == other && Status(target).State != "missing" {
						t.Fatal("wrote unselected agent")
					}
				}
			}
		})
	}
}

func TestConflictsPreserved(t *testing.T) {
	for _, kind := range []string{"modified", "unmanaged", "extra", "symlink-file", "symlink-directory", "symlink-parent", "receipt"} {
		t.Run(kind, func(t *testing.T) {
			env := environment(t)
			targets, _ := env.Targets()
			target := targets[0]
			if _, err := Apply(target, "install"); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(env.Home, "outside")
			if err := os.Mkdir(outside, 0700); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(outside, "sentinel")
			os.WriteFile(sentinel, []byte("preserve"), 0600)
			path := filepath.Join(target.Path, "SKILL.md")
			switch kind {
			case "modified":
				os.WriteFile(path, []byte("custom"), 0600)
			case "unmanaged":
				os.Remove(filepath.Join(target.Path, receipt))
			case "extra":
				os.WriteFile(filepath.Join(target.Path, "notes"), []byte("custom"), 0600)
			case "receipt":
				os.WriteFile(filepath.Join(target.Path, receipt), []byte("bad"), 0600)
			case "symlink-file":
				os.Remove(path)
				os.Symlink(sentinel, path)
			case "symlink-directory":
				os.RemoveAll(target.Path)
				os.Symlink(outside, target.Path)
			case "symlink-parent":
				os.RemoveAll(filepath.Dir(target.Path))
				os.Symlink(outside, filepath.Dir(target.Path))
			}
			if Status(target).State != "conflict" {
				t.Fatal("conflict not detected")
			}
			for _, action := range []string{"install", "uninstall"} {
				if _, err := Apply(target, action); err == nil {
					t.Fatal("accepted conflict")
				}
			}
			data, err := os.ReadFile(sentinel)
			if err != nil || string(data) != "preserve" {
				t.Fatal("escaped write")
			}
		})
	}
}

func TestManagedUpgradeAndDetection(t *testing.T) {
	env := environment(t)
	env.ClaudeConfig = filepath.Join(env.Home, "claude-custom")
	env.CodexHome = filepath.Join(env.Home, "codex-custom")
	os.MkdirAll(env.ClaudeConfig, 0700)
	env.LookPath = func(name string) (string, error) {
		if name == "codex" {
			return "/bin/codex", nil
		}
		return "", os.ErrNotExist
	}
	targets, err := env.Targets()
	if err != nil {
		t.Fatal(err)
	}
	if !targets[0].Detected || !targets[1].Detected {
		t.Fatal("detection failed")
	}
	if targets[1].Path != filepath.Join(env.Home, ".agents/skills/searchprobe") {
		t.Fatal("CODEX_HOME incorrectly changes neutral skill path")
	}
	target := targets[0]
	Apply(target, "install")
	os.WriteFile(filepath.Join(target.Path, "SKILL.md"), []byte("old skill"), 0644)
	os.WriteFile(filepath.Join(target.Path, receipt), []byte(digest("old skill")), 0644)
	if Status(target).State != "outdated" {
		t.Fatal("expected outdated")
	}
	if _, err := Apply(target, "install"); err != nil {
		t.Fatal(err)
	}
	if Status(target).State != "installed" {
		t.Fatal("upgrade failed")
	}
	if _, err := Select(targets, "../escape"); err == nil {
		t.Fatal("invalid selection accepted")
	}
	env.ClaudeConfig = "relative"
	if _, err := env.Targets(); err == nil {
		t.Fatal("relative override accepted")
	}
}

func TestStatusDoesNotWrite(t *testing.T) {
	env := environment(t)
	targets, _ := env.Targets()
	for _, target := range targets {
		Status(target)
	}
	entries, err := os.ReadDir(env.Home)
	if err != nil || len(entries) != 0 {
		t.Fatal("status wrote files", err)
	}
}

func TestLockPreservesInstallation(t *testing.T) {
	env := environment(t)
	targets, _ := env.Targets()
	target := targets[0]
	if _, err := Apply(target, "install"); err != nil {
		t.Fatal(err)
	}
	lock := target.Path + ".lock"
	if err := os.WriteFile(lock, []byte("busy"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(target, "uninstall"); err == nil {
		t.Fatal("ignored lock")
	}
	if Status(target).State != "installed" {
		t.Fatal("removed locked skill")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("removed another process lock")
	}
}
