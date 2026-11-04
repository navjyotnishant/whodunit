package main

import (
	"os"
	"path/filepath"
	"testing"

	pubtrailer "github.com/navjyotnishant/whodunit/pkg/trailer"
)

// The public package must stamp exactly what dun's own hook stamps, so the
// whodunit Pro collector and dun cannot disagree about a commit (spec 002,
// constitution Principle I). Checked on an intersected commit, where the
// most evidence is involved, and on one with no agent activity.
func TestCompatPublicTrailerStampsWhatDunStamps(t *testing.T) {
	for _, withAgent := range []bool{true, false} {
		t.Run(map[bool]string{true: "intersected", false: "no agent"}[withAgent], func(t *testing.T) {
			t.Setenv("WHODUNIT_HOME", t.TempDir())
			t.Setenv("WHODUNIT_CODEX_PATH", filepath.Join(t.TempDir(), "none"))
			t.Setenv("WHODUNIT_AGY_PATH", filepath.Join(t.TempDir(), "none"))

			repo := t.TempDir()
			git(t, repo, "init", "-q")
			git(t, repo, "commit", "-q", "--allow-empty", "-m", "base")
			realRepo, err := filepath.EvalSymlinks(repo)
			if err != nil {
				t.Fatal(err)
			}
			const body = "package main\n\nfunc addedByAgent() int {\n\treturn 42\n}\n"
			target := filepath.Join(realRepo, "agent.go")
			if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if withAgent {
				writeTranscriptFor(t, realRepo, target, body)
			}
			git(t, repo, "add", "-A")

			wd, _ := os.Getwd()
			defer os.Chdir(wd)
			if err := os.Chdir(repo); err != nil {
				t.Fatal(err)
			}

			stamp := func(run func(msgFile string) error) string {
				msgFile := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
				if err := os.WriteFile(msgFile, []byte("feat: add a function\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := run(msgFile); err != nil {
					t.Fatal(err)
				}
				out, err := os.ReadFile(msgFile)
				if err != nil {
					t.Fatal(err)
				}
				return string(out)
			}

			viaDun := stamp(func(f string) error { return runPrepareCommitMsg([]string{f, "message"}) })
			viaPkg := stamp(func(f string) error { return pubtrailer.PrepareCommitMsg([]string{f, "message"}, pubtrailer.Env{}) })
			if viaDun != viaPkg {
				t.Fatalf("pkg/trailer stamped a different message.\ndun:\n%s\npkg:\n%s", viaDun, viaPkg)
			}
			lines := pubtrailer.Lines(viaPkg)
			if len(lines) != 1 {
				t.Fatalf("want exactly one trailer, got:\n%s", viaPkg)
			}
			got, err := pubtrailer.Parse(lines[0])
			if err != nil {
				t.Fatal(err)
			}
			if withAgent && got.Method != "intersected" {
				t.Fatalf("method = %s, want intersected; the comparison proves too little", got.Method)
			}
			if err := pubtrailer.CommitMsg([]string{writeTemp(t, viaPkg)}); err != nil {
				t.Fatalf("commit-msg rejected the stamped message: %v", err)
			}
		})
	}
}

func writeTemp(t *testing.T, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
