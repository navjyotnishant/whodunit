package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The binary inside a release archive must be named `dun` (or `dun.exe`),
// not the archive's own versioned name (NAV-101).
//
// These used to be the same string, so unzipping on Windows produced
// dun_v0.2.0_windows_amd64.exe, which does nothing until the user renames
// it: the git hook resolves `dun` from PATH by name, so a differently-named
// binary means every commit is silently stamped undetermined — and it reads
// downstream as "no AI was used" rather than "the tool was not found"
// (NAV-21).
//
// Homebrew hid this on macOS and Linux by renaming during install, so the
// archive is the only path where the name has to be right on its own. It is
// also the path anyone whose policy forbids package managers takes.
//
// Building all six targets takes minutes, so this builds one — the naming
// is a property of the script's loop, not of any particular platform.
func TestReleaseArchiveContainsPlainlyNamedBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a release archive")
	}
	requireTool(t, "go")

	root := repoRootForTest(t)
	dist := filepath.Join(root, "dist")

	// The script wipes dist/ on entry, so refuse to run when one exists
	// rather than deleting a real release someone is mid-way through
	// publishing.
	if _, err := os.Stat(dist); err == nil {
		t.Skip("dist/ exists; not clobbering a release build in progress")
	}
	t.Cleanup(func() { os.RemoveAll(dist) })

	cmd := exec.Command("sh", "scripts/release.sh", "v0.0.0-test")
	cmd.Dir = root
	// TARGETS is not parameterised in the script, so the whole matrix is
	// built. Accepted: correctness of the release layout is worth a slow
	// test, and it is skipped under -short.
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("release.sh failed: %v\n%s", err, out)
	}

	t.Run("zip carries dun.exe", func(t *testing.T) {
		names := zipEntries(t, filepath.Join(dist, "dun_v0.0.0-test_windows_amd64.zip"))
		assertBinaryNamed(t, names, "dun.exe")
	})

	t.Run("tarball carries dun", func(t *testing.T) {
		names := tarEntries(t, filepath.Join(dist, "dun_v0.0.0-test_linux_amd64.tar.gz"))
		assertBinaryNamed(t, names, "dun")
	})

	t.Run("archive name keeps the version and platform", func(t *testing.T) {
		// The archive name is how release assets are told apart, so it must
		// NOT be plain — this is the other half of the contract, and
		// collapsing both to `dun` would be a different bug.
		for _, want := range []string{
			"dun_v0.0.0-test_windows_amd64.zip",
			"dun_v0.0.0-test_linux_amd64.tar.gz",
			"dun_v0.0.0-test_darwin_arm64.tar.gz",
		} {
			if _, err := os.Stat(filepath.Join(dist, want)); err != nil {
				t.Errorf("missing release asset %s", want)
			}
		}
	})

	t.Run("checksums cover every asset", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(dist, "checksums.txt"))
		if err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(dist)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Name() == "checksums.txt" {
				continue
			}
			if !strings.Contains(string(data), e.Name()) {
				t.Errorf("%s has no checksum", e.Name())
			}
		}
	})
}

func assertBinaryNamed(t *testing.T, names []string, want string) {
	t.Helper()

	// AppleDouble sidecars are reported rather than tolerated. They are
	// noise in a release asset, and the script disables them (COPYFILE_DISABLE
	// for tar, -X for zip) — one appearing means that stopped working.
	var payload []string
	for _, n := range names {
		if strings.HasPrefix(filepath.Base(n), "._") || strings.HasPrefix(n, "__MACOSX/") {
			t.Errorf("archive carries macOS metadata entry %q — it should have "+
				"been suppressed by COPYFILE_DISABLE / zip -X", n)
			continue
		}
		payload = append(payload, n)
	}

	if len(payload) != 1 {
		t.Fatalf("archive holds %d payload entries, want exactly 1: %v", len(payload), payload)
	}
	got := payload[0]
	if got != want {
		t.Errorf("archive contains %q, want %q — a binary not named `dun` is "+
			"invisible to the git hook, which resolves it from PATH by name, "+
			"so every commit would be stamped undetermined (NAV-101)", got, want)
	}
}

func zipEntries(t *testing.T, path string) []string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	return names
}

func tarEntries(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()

	var names []string
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	return names
}

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not on PATH", name)
	}
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("release.sh is a POSIX shell script")
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}
