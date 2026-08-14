// Author: Navjyot Nishant
// Created: 2026-08-14
// Last updated: 2026-08-14
// Description: The path invariant every attribution match depends on.

package linehash

import (
	"os"
	"path/filepath"
	"testing"
)

// Canonical must be idempotent and total.
//
// Every caller feeds its result into a hash, so anything that changes on a
// second application, or that returns "" for a path that has one, breaks a
// match silently — the commit is stamped observed instead of intersected
// and nothing says why.
func TestCanonicalIsIdempotentAndTotal(t *testing.T) {
	real := t.TempDir()

	cases := []string{
		"/repo/main.go",
		"/repo//main.go",
		"/repo/./main.go",
		"/repo/sub/../main.go",
		`C:\repo\main.go`,
		"C:/repo/main.go",
		"relative/path.go",
		"/deleted/but/recorded.go",
		real,
		filepath.Join(real, "exists.go"),
		"/repo/ünïcode/文件.go",
		"/repo/with spaces/file.go",
	}

	if err := os.WriteFile(filepath.Join(real, "exists.go"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, p := range cases {
		once := Canonical(p)
		if once == "" {
			t.Errorf("Canonical(%q) = \"\"; a path with a spelling must keep one", p)
			continue
		}
		if twice := Canonical(once); twice != once {
			t.Errorf("Canonical is not idempotent for %q: %q then %q", p, once, twice)
		}
	}

	// The empty path is the one case that stays empty: there is nothing to
	// canonicalise, and inventing a spelling would hash an absent file.
	if got := Canonical(""); got != "" {
		t.Errorf("Canonical(\"\") = %q, want \"\"", got)
	}
}

// Spellings that denote one file must hash alike.
//
// This is the invariant the whole intersected method rests on. The staged
// half builds a path with filepath.Join from git's output; the recorded half
// takes whatever an agent wrote into its transcript. If those two spell one
// file differently, no line ever matches and every commit degrades — quietly,
// because a failed match is not an error.
func TestEquivalentSpellingsHashAlike(t *testing.T) {
	groups := [][]string{
		{"/repo/main.go", "/repo//main.go", "/repo/./main.go", "/repo/sub/../main.go"},
		{`C:\repo\main.go`, "C:/repo/main.go", `C:\repo\.\main.go`},
		{"/repo/pkg/file.go", "/repo/pkg//file.go"},
	}

	for _, group := range groups {
		want := Of(group[0], "doWork()")
		for _, spelling := range group[1:] {
			if got := Of(spelling, "doWork()"); got != want {
				t.Errorf("Of(%q) = %d and Of(%q) = %d; these name one file "+
					"and must hash alike, or no staged line ever matches a "+
					"recorded one", group[0], want, spelling, got)
			}
		}
	}
}

// Different files must not collide.
//
// The other half of the invariant: canonicalising aggressively enough to
// merge genuinely distinct paths would manufacture attribution that never
// happened, which is worse than losing some.
func TestDistinctPathsDoNotCollide(t *testing.T) {
	distinct := []string{
		"/repo/main.go",
		"/repo/other.go",
		"/other-repo/main.go",
		"/repo/sub/main.go",
		`C:\repo\main.go`,
		`D:\repo\main.go`,
	}

	seen := map[uint64]string{}
	for _, p := range distinct {
		h := Of(p, "doWork()")
		if prev, ok := seen[h]; ok && prev != p {
			// C:\ and D:\ are the case worth naming: folding the volume
			// would merge two drives' copies of one project.
			t.Errorf("%q and %q hash identically; distinct files must not "+
				"share a hash or attribution is manufactured", prev, p)
		}
		seen[h] = p
	}
}
