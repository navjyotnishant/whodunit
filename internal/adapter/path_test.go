package adapter

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// isolate points config at a temp home so a test never reads or writes the
// developer's real ~/.whodunit.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("WHODUNIT_HOME", home)
	return home
}

func TestEnvVarFor(t *testing.T) {
	cases := map[string]string{
		"claude-code": "WHODUNIT_CLAUDE_CODE_PATH",
		"codex":       "WHODUNIT_CODEX_PATH",
		"agy":         "WHODUNIT_AGY_PATH",
	}
	for agent, want := range cases {
		if got := EnvVarFor(agent); got != want {
			t.Errorf("EnvVarFor(%q) = %q, want %q", agent, got, want)
		}
	}
}

func TestResolveRootPrecedence(t *testing.T) {
	isolate(t)

	// Nothing set: the built-in default is used.
	got, src := ResolveRoot("claude-code", "/builtin")
	if got != "/builtin" || src != SourceDefault {
		t.Fatalf("default: got (%q, %q)", got, src)
	}

	// Config beats the default.
	writeAgentConfig(t, "claude-code", "/from-config")
	got, src = ResolveRoot("claude-code", "/builtin")
	if got != "/from-config" || src != SourceConfig {
		t.Fatalf("config: got (%q, %q)", got, src)
	}

	// The environment beats config, so CI and one-off debugging need no
	// file edit.
	t.Setenv("WHODUNIT_CLAUDE_CODE_PATH", "/from-env")
	got, src = ResolveRoot("claude-code", "/builtin")
	if got != "/from-env" || src != SourceEnv {
		t.Fatalf("env: got (%q, %q)", got, src)
	}
}

// A config for one agent must not redirect another.
func TestResolveRootIsPerAgent(t *testing.T) {
	isolate(t)
	writeAgentConfig(t, "claude-code", "/claude-only")

	if got, _ := ResolveRoot("codex", "/codex-builtin"); got != "/codex-builtin" {
		t.Fatalf("codex resolved to %q; claude-code's config leaked", got)
	}
}

// The three states exist to be distinguishable. Collapsing them either nags
// users about agents they do not have, or hides a real misconfiguration.
func TestDetectStates(t *testing.T) {
	defer restore(t)()

	t.Run("found", func(t *testing.T) {
		isolate(t)
		registered = nil
		dir := t.TempDir()
		Register(fake{name: "a", dir: dir, files: []string{"s1.jsonl", "s2.jsonl"}})

		d := Detect("/repo")[0]
		if d.State != StateFound || d.Sessions != 2 {
			t.Fatalf("got state=%q sessions=%d", d.State, d.Sessions)
		}
	})

	t.Run("empty directory is not the same as absent", func(t *testing.T) {
		isolate(t)
		registered = nil
		Register(fake{name: "a", dir: t.TempDir()}) // exists, no sessions

		if d := Detect("/repo")[0]; d.State != StateEmpty {
			t.Fatalf("got %q, want %q", d.State, StateEmpty)
		}
	})

	t.Run("default and absent means not installed", func(t *testing.T) {
		isolate(t)
		registered = nil
		Register(fake{name: "a", dir: filepath.Join(t.TempDir(), "nope")})

		if d := Detect("/repo")[0]; d.State != StateNotInstalled {
			t.Fatalf("got %q, want %q", d.State, StateNotInstalled)
		}
	})

	t.Run("configured and absent is a mistake to fix", func(t *testing.T) {
		isolate(t)
		registered = nil
		missing := filepath.Join(t.TempDir(), "nope")
		Register(fake{name: "a", dir: missing})
		writeAgentConfig(t, "a", missing)

		d := Detect("/repo")[0]
		if d.State != StateMissing {
			t.Fatalf("got %q, want %q", d.State, StateMissing)
		}
		// The report has to show what the user set, not a derived path.
		if d.Root != missing {
			t.Fatalf("Root = %q, want the configured %q", d.Root, missing)
		}
	})

	t.Run("a failed probe is unknown, never absence", func(t *testing.T) {
		isolate(t)
		registered = nil
		Register(fake{name: "a", dir: t.TempDir(), err: errors.New("permission denied")})

		d := Detect("/repo")[0]
		if d.State != StateError {
			t.Fatalf("got %q, want %q — an unreadable directory is not evidence of no AI", d.State, StateError)
		}
		if d.Err == nil {
			t.Fatal("state is unknown but no error was carried")
		}
	})
}

// Detection must report every registered agent, so a machine with several
// installed sees all of them rather than the first.
func TestDetectCoversEveryAgent(t *testing.T) {
	defer restore(t)()
	isolate(t)
	registered = nil
	Register(fake{name: "one", dir: t.TempDir()})
	Register(fake{name: "two", dir: t.TempDir()})
	Register(fake{name: "three", dir: t.TempDir()})

	if got := Detect("/repo"); len(got) != 3 {
		t.Fatalf("Detect returned %d results for 3 agents", len(got))
	}
}

func writeAgentConfig(t *testing.T, agent, path string) {
	t.Helper()
	home := os.Getenv("WHODUNIT_HOME")
	if home == "" {
		t.Fatal("writeAgentConfig called without an isolated home")
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	// Marshalled rather than concatenated. A Windows path carries
	// backslashes, and pasting one into a JSON string literal produces
	// invalid escapes — "C:\Users" is a bad \U — so the config failed to
	// parse, no agent path was seen, and the test read the resulting
	// "not installed" as a behaviour difference rather than as its own bug.
	body, err := json.Marshal(map[string]any{
		"agents": map[string]any{
			agent: map[string]any{"path": path},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}
