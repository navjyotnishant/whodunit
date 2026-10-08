package repoid

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navjyotnishant/whodunit/internal/registry"
)

const sampleRoot = "b362ea7aa65515dc35ff3a93423478b2143e771d"

// templateServer builds a "server" repository whose first two commits are
// byte-identical to every other call: fixed author, committer, dates and
// content, as a GitLab template import produces. Two calls therefore give
// two unrelated projects with the same root commit.
func templateServer(t *testing.T, project string) string {
	t.Helper()
	dir := newRepo(t)
	seed := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Administrator", "GIT_AUTHOR_EMAIL=admin@example.com",
			"GIT_COMMITTER_NAME=Administrator", "GIT_COMMITTER_EMAIL=admin@example.com",
			"GIT_AUTHOR_DATE=2021-02-15T15:52:03+00:00", "GIT_COMMITTER_DATE=2021-02-15T15:52:03+00:00",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	seed("commit", "-q", "--allow-empty", "-m", "Initial commit")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Sample GitLab Project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seed("add", "README.md")
	seed("commit", "-q", "-m", "Update README.md")
	commitIn(t, dir, project+".txt") // the project's own work starts here
	return dir
}

func cloneOf(t *testing.T, server, name string) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), name)
	if out, err := exec.Command("git", "clone", "-q", server, clone).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	return clone
}

func resolveIn(t *testing.T, dir string) Resolution {
	t.Helper()
	r, err := Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve(%s): %v", dir, err)
	}
	return r
}

func commitWithMessage(t *testing.T, dir, name, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runIn(t, dir, "add", name)
	runIn(t, dir, "commit", "-q", "-m", msg)
}

func TestProjectsFromOneTemplateGetDifferentIDs(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())
	api := cloneOf(t, templateServer(t, "api"), "api")
	vue := cloneOf(t, templateServer(t, "vue"), "vue")

	a, v := resolveIn(t, api), resolveIn(t, vue)
	if a.Root != v.Root {
		t.Fatalf("fixture broken: roots differ (%s, %s), the template must make them equal", a.Root, v.Root)
	}
	if a.Scheme != SchemeOrigin || v.Scheme != SchemeOrigin {
		t.Fatalf("schemes = %s, %s; want origin for repositories never instrumented", a.Scheme, v.Scheme)
	}
	if a.ID == v.ID {
		t.Errorf("two projects from one template share id %s (WHO-263)", a.ID)
	}
}

func TestATeamOnTheOriginSchemeAgrees(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())
	server := templateServer(t, "api")
	alice, bob := resolveIn(t, cloneOf(t, server, "alice")), resolveIn(t, cloneOf(t, server, "bob"))
	if alice.ID != bob.ID {
		t.Errorf("two clones of one project: %s and %s, want one id", alice.ID, bob.ID)
	}
}

func TestAnInstrumentedRepositoryKeepsItsRootID(t *testing.T) {
	// Zero impact: a dun trailer in history means instrumented before this
	// change, so the id stays the root commit even with an origin remote.
	t.Setenv("WHODUNIT_HOME", t.TempDir())
	clone := cloneOf(t, templateServer(t, "api"), "api")
	commitWithMessage(t, clone, "b.txt", "work\n\nAI-Attribution: v=2; status=undetermined; method=undetermined")

	r := resolveIn(t, clone)
	if r.Scheme != SchemeRoot || r.ID != r.Root {
		t.Errorf("got scheme %s id %s, want root scheme with id = root %s", r.Scheme, r.ID, r.Root)
	}
}

func TestTheMarkerSwitchesEveryClone(t *testing.T) {
	// One developer opted in and pushed a marked commit; a teammate who has
	// pulled it follows, despite older unmarked trailers further back.
	t.Setenv("WHODUNIT_HOME", t.TempDir())
	clone := cloneOf(t, templateServer(t, "api"), "api")
	commitWithMessage(t, clone, "b.txt", "old\n\nAI-Attribution: v=2; status=undetermined; method=undetermined")
	commitWithMessage(t, clone, "c.txt", "new\n\nAI-Attribution: v=2; status=undetermined; method=undetermined; idv=2")

	if r := resolveIn(t, clone); r.Scheme != SchemeOrigin {
		t.Errorf("scheme = %s, want origin once a marked commit is in history", r.Scheme)
	}
}

func TestPinOverridesHistory(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())
	clone := cloneOf(t, templateServer(t, "api"), "api")
	commitWithMessage(t, clone, "b.txt", "work\n\nAI-Attribution: v=2; status=undetermined; method=undetermined")
	if r := resolveIn(t, clone); r.Scheme != SchemeRoot {
		t.Fatalf("precondition: scheme = %s, want root", r.Scheme)
	}
	if err := Pin(clone, SchemeOrigin); err != nil {
		t.Fatal(err)
	}
	if r := resolveIn(t, clone); r.Scheme != SchemeOrigin || r.ID == r.Root {
		t.Errorf("after Pin: scheme %s id %s, want origin and an id other than the root", r.Scheme, r.ID)
	}
}

func TestARegisteredRepositoryWithoutTrailersKeepsItsRootID(t *testing.T) {
	// Instrumented, but no trailered commit yet: its journal rows are under
	// the root id, so it must stay there.
	t.Setenv("WHODUNIT_HOME", t.TempDir())
	clone := cloneOf(t, templateServer(t, "api"), "api")
	if err := registry.Add(sampleRootOf(t, clone), clone, time.Now()); err != nil {
		t.Fatal(err)
	}
	if r := resolveIn(t, clone); r.Scheme != SchemeRoot {
		t.Errorf("scheme = %s, want root for a repository registered under its root id", r.Scheme)
	}
}

func sampleRootOf(t *testing.T, dir string) string {
	t.Helper()
	root, err := RootFor(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestNoOriginKeepsTheRootID(t *testing.T) {
	t.Setenv("WHODUNIT_HOME", t.TempDir())
	dir := newRepo(t)
	commitIn(t, dir, "a.txt")
	if r := resolveIn(t, dir); r.Scheme != SchemeRoot || r.ID != r.Root {
		t.Errorf("no remote: scheme %s id %s, want root", r.Scheme, r.ID)
	}
}

func TestEveryWayOfCloningOneProjectNormalisesTheSame(t *testing.T) {
	forms := []string{
		"https://gitlab.com/njlabs-group/testp.git",
		"git@gitlab.com:njlabs-group/testp.git",
		"https://gitlab-ci-token:glcbt-abc123@gitlab.com/njlabs-group/testp.git",
		"https://GitLab.com/njlabs-group/testp/",
		"ssh://git@gitlab.com:22/njlabs-group/testp.git",
	}
	want := "gitlab.com/njlabs-group/testp"
	for _, f := range forms {
		if got := NormalizeRemote(f); got != want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", f, got, want)
		}
	}
}

func TestOriginIDIsStable(t *testing.T) {
	// Pinned values: once released, an id that changes moves every
	// repository on the origin scheme to a new id and orphans its data.
	cases := map[string]string{
		"https://gitlab.com/njlabs-group/testp.git":                "d7a60f9a5efa",
		"https://gitlab.com/kochi-chk/sample-gitlab-project.git":   "d7d7ee672c96",
		"https://gitlab.com/my-group960/sample-gitlab-project.git": "0806e88b3d23",
	}
	for remote, prefix := range cases {
		if got := OriginID(sampleRoot, remote); !strings.HasPrefix(got, prefix) {
			t.Errorf("OriginID(%s) = %s, want prefix %s", remote, got, prefix)
		}
	}
	if got := OriginID(sampleRoot, "https://gitlab.com/njlabs-group/testp.git"); len(got) != 40 {
		t.Errorf("id length %d, want 40 so every store holding a root SHA holds it", len(got))
	}
}

func TestTheSampleTemplateRootIsKnown(t *testing.T) {
	if !KnownTemplateRoot(sampleRoot) {
		t.Error("the GitLab Sample template root is not recognised")
	}
}
