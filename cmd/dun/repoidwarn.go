package main

import (
	"fmt"
	"io"
	"os"

	"github.com/navjyotnishant/whodunit/internal/registry"
	"github.com/navjyotnishant/whodunit/internal/repoid"
)

// warnSharedRepoID prints, on every dun command, that this repository's id
// is shared with other projects, until it is not (WHO-263).
//
// Shown only while the repository is on the root scheme and either its root
// is a known history-importing template, or another registered checkout on
// this machine has the same root but a different origin. It stops by itself
// once anyone on the team runs `dun init --distinct` and their commit is
// pulled. Written to stderr, so it never fails a hook or pollutes output a
// script reads.
func warnSharedRepoID(w io.Writer) {
	r, err := repoid.Resolve("")
	if err != nil || r.Scheme != repoid.SchemeRoot {
		return
	}
	if !repoid.KnownTemplateRoot(r.Root) && !collidesOnThisMachine(r.Root) {
		return
	}
	fmt.Fprintf(w,
		"dun: this repository shares its id (%s) with other projects that start from the same\n"+
			"     history (e.g. a GitLab template), so their data is merged. Run `dun init --distinct`\n"+
			"     once here; teammates switch automatically after their next pull.\n",
		short(r.Root))
}

// collidesOnThisMachine reports whether a registered checkout shares this
// root but points at a different project. This checkout's own entry, or a
// second clone of the same project, has the same origin and does not count.
func collidesOnThisMachine(root string) bool {
	entries, err := registry.List()
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.RepoID != root {
			continue
		}
		if _, err := os.Stat(e.Path); err == nil && !repoid.SameProject("", e.Path) {
			return true
		}
	}
	return false
}
