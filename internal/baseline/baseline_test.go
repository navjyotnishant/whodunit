package baseline

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/purpose"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.local",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.local",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")

	commit := func(file, content, msg string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", file, err)
		}
		run("add", file)
		run("commit", "-q", "-m", msg)
	}

	commit("main.go", "package main\nfunc main() {}\n", "feat: add main")
	commit("main_test.go", "package main\n", "test: cover main")
	commit("README.md", "# repo\n", `Revert "feat: add main"`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	return dir
}

func TestCaptureComputesGitMetrics(t *testing.T) {
	initRepo(t)

	snap, err := Capture(90, nil, time.Now())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if snap.Git.Commits != 3 {
		t.Errorf("Commits = %d, want 3", snap.Git.Commits)
	}
	if snap.Git.Reverts != 1 {
		t.Errorf("Reverts = %d, want 1 (the Revert \"...\" commit)", snap.Git.Reverts)
	}
	if snap.Git.RevertRate < 0.32 || snap.Git.RevertRate > 0.34 {
		t.Errorf("RevertRate = %v, want ~0.333 (1 of 3)", snap.Git.RevertRate)
	}
	if snap.Git.MedianDiffLines <= 0 {
		t.Errorf("MedianDiffLines = %d, want > 0", snap.Git.MedianDiffLines)
	}
	if snap.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", snap.SchemaVersion, SchemaVersion)
	}
	if snap.HeadSHA == "" {
		t.Error("HeadSHA is empty")
	}
	if snap.Git.PurposeDistribution[purpose.Feature] != 1 {
		t.Errorf("purpose feature = %d, want 1", snap.Git.PurposeDistribution[purpose.Feature])
	}
	if snap.Git.PurposeDistribution[purpose.Test] != 1 {
		t.Errorf("purpose test = %d, want 1", snap.Git.PurposeDistribution[purpose.Test])
	}
}

func TestCaptureOmitsManualWhenNotSupplied(t *testing.T) {
	initRepo(t)

	snap, err := Capture(90, nil, time.Now())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if snap.Manual != nil {
		t.Errorf("Manual = %+v, want nil when nothing was supplied", snap.Manual)
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "\"manual\"") {
		t.Error("unsupplied manual metrics must be omitted entirely, not serialized as zero values")
	}
}

func TestCaptureRecordsManualMetrics(t *testing.T) {
	initRepo(t)

	prs := 42
	cycle := 6.5
	manual := &ManualMetrics{PRsMerged: &prs, MedianCycleTimeHrs: &cycle, Note: "from GitHub Insights"}

	snap, err := Capture(90, manual, time.Now())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if snap.Manual == nil || snap.Manual.PRsMerged == nil || *snap.Manual.PRsMerged != 42 {
		t.Errorf("Manual.PRsMerged not recorded: %+v", snap.Manual)
	}
	if snap.Manual.ChangeFailureRate != nil {
		t.Error("ChangeFailureRate should stay nil when not supplied, not default to 0")
	}
}

func TestWriteRefusesToOverwrite(t *testing.T) {
	initRepo(t)
	path := filepath.Join(t.TempDir(), "baseline.json")

	snap, err := Capture(90, nil, time.Now())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if err := Write(path, snap); err != nil {
		t.Fatalf("first Write: %v", err)
	}

	// A baseline measures a window that cannot be recaptured — silently
	// replacing it destroys the only copy.
	if err := Write(path, snap); err == nil {
		t.Error("second Write() = nil error, want refusal to overwrite an existing baseline")
	}
}

func TestWriteProducesReadableJSON(t *testing.T) {
	initRepo(t)
	path := filepath.Join(t.TempDir(), "baseline.json")

	snap, err := Capture(90, nil, time.Now())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if err := Write(path, snap); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var round Snapshot
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("snapshot is not valid JSON: %v", err)
	}
	if round.Git.Commits != snap.Git.Commits {
		t.Errorf("round-tripped commits = %d, want %d", round.Git.Commits, snap.Git.Commits)
	}
}

func TestLoadReturnsNilWhenAbsent(t *testing.T) {
	// A repo that never captured a baseline is a normal state to report,
	// not an error to fail on.
	snap, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("Load on missing file = %v, want nil error", err)
	}
	if snap != nil {
		t.Errorf("Load on missing file = %+v, want nil snapshot", snap)
	}
}

func TestLoadRejectsUnknownSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"999","git":{}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Comparing across schema versions would silently compare different
	// definitions of the same metric names.
	if _, err := Load(path); err == nil {
		t.Error("Load with an unknown schema version = nil error, want a refusal")
	}
}

func TestLoadRoundTripsWrite(t *testing.T) {
	initRepo(t)
	path := filepath.Join(t.TempDir(), "baseline.json")

	snap, err := Capture(90, nil, time.Now())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if err := Write(path, snap); err != nil {
		t.Fatalf("Write: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load returned nil for a file that exists")
	}
	if loaded.Git.Commits != snap.Git.Commits || loaded.HeadSHA != snap.HeadSHA {
		t.Errorf("round trip mismatch: got %+v, want %+v", loaded.Git, snap.Git)
	}
}

func TestCaptureOnEmptyRepoErrors(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	// Unlike report/status, a baseline over zero commits is meaningless
	// rather than a valid empty result — surface it rather than writing a
	// snapshot that measures nothing.
	if _, err := Capture(90, nil, time.Now()); err == nil {
		t.Error("Capture() on an empty repo = nil error, want an error")
	}
}

// A snapshot is owner-only, like everything else under ~/.whodunit.
//
// It named a repository and reported its commit cadence and revert rate at
// mode 0644 for a long time without anyone noticing, because the enclosing
// directory is 0700 and hid it. That makes this exactly the kind of thing
// worth asserting rather than reading: the loose mode is invisible until
// the file is copied somewhere the directory no longer protects it.
//
// Unix-only. Windows has no mode bits — os.Chmod there toggles the
// read-only attribute and os.Stat synthesises a mode from it, so this
// would assert an invented number. internal/secret handles the Windows
// equivalent with a real ACL; baselines have not needed that yet.
func TestSnapshotIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix mode bits on Windows")
	}
	path := filepath.Join(t.TempDir(), "repo.json")
	if err := Write(path, Snapshot{SchemaVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("snapshot mode is %04o, want 0600 — it records a repository's "+
			"commit cadence and revert rate, which is nobody else's business "+
			"on a shared machine", perm)
	}
}

// initDatedRepo builds a repo with one commit per supplied date, so a
// window can be asserted to include and exclude specific history.
func initDatedRepo(t *testing.T, dates ...string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.local",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.local",
		), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(nil, "init", "-q")

	for i, d := range dates {
		name := "f" + strconv.Itoa(i) + ".go"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		stamp := d + "T12:00:00+00:00"
		run(nil, "add", name)
		run([]string{"GIT_AUTHOR_DATE=" + stamp, "GIT_COMMITTER_DATE=" + stamp},
			"commit", "-q", "-m", "feat: commit on "+d)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	return dir
}

func mustDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return d
}

// The point of the whole feature: a user names the period they worked
// without an agent, and only that period is measured.
func TestCaptureWindowMeasuresOnlyTheNamedRange(t *testing.T) {
	initDatedRepo(t,
		"2026-01-10", "2026-02-15", "2026-03-20", // inside
		"2026-08-01", "2026-08-05", // after, must be excluded
	)

	w := Window{Since: mustDay(t, "2026-01-01"), Until: mustDay(t, "2026-06-30")}
	snap, err := CaptureWindow(w, nil, time.Now())
	if err != nil {
		t.Fatalf("CaptureWindow: %v", err)
	}

	if snap.Git.Commits != 3 {
		t.Errorf("commits inside window = %d, want 3 (the August commits must be excluded)", snap.Git.Commits)
	}
	if !snap.WindowSince.Equal(w.Since.UTC()) || !snap.WindowUntil.Equal(w.Until.UTC()) {
		t.Errorf("snapshot bounds = %s..%s, want %s..%s",
			snap.WindowSince, snap.WindowUntil, w.Since.UTC(), w.Until.UTC())
	}
	if snap.WindowDays != 180 {
		t.Errorf("WindowDays = %d, want 180 (kept for downstream readers)", snap.WindowDays)
	}
}

func TestCaptureWindowRejectsInvertedRange(t *testing.T) {
	initDatedRepo(t, "2026-01-10")

	w := Window{Since: mustDay(t, "2026-06-30"), Until: mustDay(t, "2026-01-01")}
	if _, err := CaptureWindow(w, nil, time.Now()); err == nil {
		t.Fatal("expected an error when the window ends before it starts")
	}
}

// Capture stays equivalent to the window it describes, so the existing
// --days path cannot drift from CaptureWindow.
func TestCaptureDelegatesToWindow(t *testing.T) {
	initRepo(t)
	now := time.Now()

	viaDays, err := Capture(90, nil, now)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	viaWindow, err := CaptureWindow(WindowFromDays(90, now), nil, now)
	if err != nil {
		t.Fatalf("CaptureWindow: %v", err)
	}

	if viaDays.Git.Commits != viaWindow.Git.Commits || viaDays.WindowDays != viaWindow.WindowDays {
		t.Errorf("Capture(90) = %d commits/%dd, CaptureWindow = %d commits/%dd",
			viaDays.Git.Commits, viaDays.WindowDays, viaWindow.Git.Commits, viaWindow.WindowDays)
	}
}

func TestWriteForceReplacesExisting(t *testing.T) {
	initRepo(t)
	path := filepath.Join(t.TempDir(), "baseline.json")

	first, err := Capture(90, nil, time.Now())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if err := Write(path, first); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Without --force the refusal stands.
	if err := Write(path, first); err == nil {
		t.Fatal("Write overwrote an existing baseline without force")
	}

	second, err := CaptureWindow(Window{
		Since: mustDay(t, "2026-01-01"), Until: mustDay(t, "2026-03-31"),
	}, nil, time.Now())
	if err != nil {
		t.Fatalf("CaptureWindow: %v", err)
	}
	if err := WriteForce(path, second); err != nil {
		t.Fatalf("WriteForce: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.WindowDays != second.WindowDays {
		t.Errorf("after force, WindowDays = %d, want %d", got.WindowDays, second.WindowDays)
	}
}
