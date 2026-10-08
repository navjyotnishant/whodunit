// Package repoid derives a stable identifier for a git repository.
//
// The identifier starts from the repository's root commit SHA. That choice
// matters:
//
//   - A filesystem path breaks the moment the same repo is cloned to another
//     machine or directory, and records local paths in a shared store.
//   - A remote URL leaks the org and repo name, which NAV-25 forbids
//     recording at all.
//   - The root commit SHA is identical for everyone with the same history,
//     survives clones, worktrees, and renames, and reveals nothing on its
//     own.
//
// Its blind spot is that it treats same history as same repository. GitLab's
// "Create from template" imports the template's history, so every project
// made from "Sample GitLab Project" starts at root b362ea7a and they all got
// one id (WHO-263). So there are two schemes:
//
//   - root: id = root commit SHA. Every repository instrumented before this
//     change stays on it, so no working repository's id ever moves.
//   - origin: id = sha1 of the root and the normalised origin URL, the same
//     40-hex shape. Repositories instrumented from now on use it, so two
//     projects from one template differ while every clone of one project
//     agrees. Only the hash is ever stored or sent, never the URL.
//
// Which scheme applies is decided from the repository itself, so every clone
// reaches the same answer with nothing per-machine to set up. See Resolve.
package repoid

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/navjyotnishant/whodunit/internal/registry"
)

// Scheme is how a repository's id is derived.
type Scheme string

const (
	SchemeRoot   Scheme = "root"
	SchemeOrigin Scheme = "origin"
)

// ConfigKey pins a scheme in the repository's own .git/config: local,
// never committed, shared by every worktree. Set by `dun init --distinct`.
const ConfigKey = "whodunit.repoIdScheme"

// MarkerKey and MarkerValue are the trailer field carried by every commit
// made under the origin scheme. Teammates' dun reads it from history and
// follows, so a team switches when one person opts in.
const (
	MarkerKey   = "idv"
	MarkerValue = "2"
)

// Neither a marker nor any dun trailer can predate these dates, so history
// scans stop there instead of walking a whole repository on every call.
// dun's first commit is 2026-08-11; the marker shipped in 0.6.2.
const (
	trailersSince = "2026-08-01"
	markerSince   = "2026-10-01"
)

// gitBin is git resolved to an absolute path once, rather than a bare name
// looked up through PATH on every call.
var gitBin = func() string {
	if p, err := exec.LookPath("git"); err == nil {
		return p
	}
	return "git"
}()

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command(gitBin, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// ForCurrentRepo returns the identifier for the repository containing the
// current working directory.
func ForCurrentRepo() (string, error) {
	return ForRepo("")
}

// ForRepo returns the identifier for the repository at dir. An empty dir
// means the current working directory.
func ForRepo(dir string) (string, error) {
	r, err := Resolve(dir)
	return r.ID, err
}

// Resolution is a repository's id together with how it was reached.
type Resolution struct {
	ID     string
	Root   string
	Scheme Scheme
}

// Resolve returns the repository's id, its root commit, and the scheme.
//
// The scheme, first match wins:
//  1. pinned in .git/config (an explicit opt-in);
//  2. a commit carrying the origin-scheme marker → origin (a teammate is on
//     it, so everyone follows);
//  3. a dun trailer anywhere → root (instrumented before this change:
//     grandfathered, untouched);
//  4. registered on this machine under the root id → root (instrumented, no
//     trailered commit yet);
//  5. no origin remote → root (nothing to tell projects apart by);
//  6. otherwise → origin (never instrumented).
//
// 1 is local; 2-3 read history, so every clone agrees; 4 only ever keeps an
// existing id.
func Resolve(dir string) (Resolution, error) {
	root, err := RootFor(dir)
	if err != nil {
		return Resolution{}, err
	}
	scheme := schemeFor(dir, root)
	if scheme == SchemeOrigin {
		if remote := originURL(dir); remote != "" {
			return Resolution{ID: OriginID(root, remote), Root: root, Scheme: SchemeOrigin}, nil
		}
	}
	return Resolution{ID: root, Root: root, Scheme: SchemeRoot}, nil
}

func schemeFor(dir, root string) Scheme {
	if pinned, err := git(dir, "config", "--get", ConfigKey); err == nil {
		switch Scheme(pinned) {
		case SchemeRoot, SchemeOrigin:
			return Scheme(pinned)
		}
	}
	if hasCommit(dir, markerSince, `^AI-Attribution:.*`+MarkerKey+`=`+MarkerValue) {
		return SchemeOrigin
	}
	if hasCommit(dir, trailersSince, `^AI-Attribution:`) {
		return SchemeRoot
	}
	if registeredUnderRoot(dir, root) {
		return SchemeRoot
	}
	if originURL(dir) == "" {
		return SchemeRoot
	}
	return SchemeOrigin
}

// hasCommit reports whether HEAD's history has a commit after since whose
// message has a line matching pattern.
func hasCommit(dir, since, pattern string) bool {
	out, err := git(dir, "log", "-1", "--since="+since, "-E", "--grep="+pattern, "--format=%H", "HEAD")
	return err == nil && out != ""
}

func registeredUnderRoot(dir, root string) bool {
	return RegisteredHere(dir, root)
}

// RegisteredHere reports whether the registry holds id for this checkout's
// path. Colliding repositories share one entry, so the id alone cannot say
// whose it is.
func RegisteredHere(dir, id string) bool {
	top, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	entries, err := registry.List()
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.RepoID == id && samePath(e.Path, top) {
			return true
		}
	}
	return false
}

// SameProject reports whether two checkouts point at the same project: same
// normalised origin. Two checkouts with one root and different origins are
// different projects that share an id under the root scheme.
func SameProject(dirA, dirB string) bool {
	a, b := originURL(dirA), originURL(dirB)
	return a != "" && b != "" && NormalizeRemote(a) == NormalizeRemote(b)
}

// HasOrigin reports whether the repository has an `origin` remote.
func HasOrigin(dir string) bool {
	return originURL(dir) != ""
}

func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

func originURL(dir string) string {
	remote, err := git(dir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return remote
}

// Pin records a scheme in the repository's .git/config.
func Pin(dir string, s Scheme) error {
	if _, err := git(dir, "config", "--local", ConfigKey, string(s)); err != nil {
		return fmt.Errorf("pin repo id scheme: %w", err)
	}
	return nil
}

// RootFor returns the repository's root commit SHA.
func RootFor(dir string) (string, error) {
	out, err := git(dir, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve repo root commit (unborn or not a git repo?): %w", err)
	}

	var roots []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			roots = append(roots, line)
		}
	}
	if len(roots) == 0 {
		return "", fmt.Errorf("repository has no commits yet, so it has no stable identifier")
	}

	// A repository can have several root commits — histories merged with
	// --allow-unrelated-histories, or a subtree merge. git lists them in
	// traversal order, which is not stable across clones, so sort and take
	// the lowest to get the same answer everywhere.
	sort.Strings(roots)
	return roots[0], nil
}

// OriginID is the origin-scheme id: 40 hex, the same shape as a commit SHA,
// so every store and column that holds a root id holds this unchanged.
func OriginID(root, remote string) string {
	sum := sha1.Sum([]byte("whodunit-repo-v2\x00" + root + "\x00" + NormalizeRemote(remote)))
	return hex.EncodeToString(sum[:])
}

// NormalizeRemote reduces a clone URL to host/path so every way of cloning
// one project gives one value: SSH or HTTPS, credentials (a CI token), port,
// letter case, trailing slash or .git. Frozen: changing it would split every
// team on the origin scheme, which is why the id prefix carries "v2".
func NormalizeRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	var host, path string
	if !strings.Contains(remote, "://") {
		// scp form: [user@]host:group/project.git
		if at := strings.Index(remote, "@"); at >= 0 && at < strings.Index(remote, ":") {
			remote = remote[at+1:]
		}
		if colon := strings.Index(remote, ":"); colon >= 0 {
			host, path = remote[:colon], remote[colon+1:]
		} else {
			path = remote // a local path
		}
	} else if u, err := url.Parse(remote); err == nil {
		host, path = u.Hostname(), u.Path
	} else {
		path = remote
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	return strings.ToLower(host + "/" + path)
}

// KnownTemplateRoot reports whether root is the first commit of a platform
// template that imports its history into every project made from it. Only
// roots verified to collide are listed.
func KnownTemplateRoot(root string) bool {
	return knownTemplateRoots[root]
}

var knownTemplateRoots = map[string]bool{
	// GitLab "Sample GitLab Project": Administrator, 2021-02-15. Verified
	// shared by unrelated projects created 2021-2026 (WHO-263).
	"b362ea7aa65515dc35ff3a93423478b2143e771d": true,
}
