// Author: Navjyot Nishant
// Created: 2026-08-14
// Last updated: 2026-08-14
// Description: Whether the hooks will still find dun tomorrow, reported at
// init time.

package main

import (
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"
)

// reportReachability says whether `dun` resolves by name, and what it costs
// when it does not (NAV-102).
//
// The hook script resolves the binary at run time:
//
//	DUN="$(command -v dun || echo "<absolute path>")"
//
// so a binary that is not on PATH still works — through the absolute path
// recorded when the hook was written. That fallback is why this is a
// warning rather than an error, and it is also why the situation is worth
// naming: the fallback is a path to wherever the binary happened to be at
// init time, and it survives exactly as long as the binary stays there.
//
// The concrete way this breaks: someone downloads the Windows archive,
// unzips it to Downloads, runs `dun init` from there, and later tidies up
// Downloads. Every commit after that is stamped undetermined, and that
// reads downstream as "no AI was used" rather than "the binary is gone"
// (NAV-21). Nothing announces it — the hook exits 0 either way, because a
// hook that fails a commit over its own installation is worse than one
// that stays quiet.
//
// Deliberately after the hooks are installed and never fatal: this is a
// report, not a gate. Someone deliberately running a dev build from a temp
// directory does not need to be stopped.
func reportReachability(w io.Writer, self string) {
	onPath, err := lookDun()
	if err == nil && sameBinary(onPath, self) {
		return // resolves by name, and to this binary: nothing to say
	}

	fmt.Fprintln(w)

	if err == nil {
		// A different dun is first on PATH. Worth naming precisely,
		// because the hooks will run THAT one, not the one being used to
		// install them — so a fix applied here may appear to do nothing.
		fmt.Fprintf(w, "warning: the hooks will run a different dun than this one\n")
		fmt.Fprintf(w, "  on PATH:   %s\n", onPath)
		fmt.Fprintf(w, "  this one:  %s\n", self)
		fmt.Fprintf(w, "%s\n", reachabilityFix())
		return
	}

	fmt.Fprintf(w, "warning: dun is not on PATH\n")
	fmt.Fprintf(w, "  the hooks will fall back to %s\n", self)
	fmt.Fprintf(w, "  which works until that file moves or is deleted — after which\n")
	fmt.Fprintf(w, "  every commit is stamped undetermined, silently.\n")
	fmt.Fprintf(w, "%s\n", reachabilityFix())
}

// reachabilityFix names the platform's usual remedy. Windows is called out
// separately because it is where this actually bites: the archive is the
// common install route there, and unzipping does not put anything on PATH.
func reachabilityFix() string {
	if runtime.GOOS == "windows" {
		return "  fix: move dun.exe somewhere on PATH, or install it with\n" +
			"       `scoop install dun` so PATH is handled for you"
	}
	return "  fix: move dun somewhere on PATH, or install it with\n" +
		"       `brew install navjyotnishant/tap/dun`"
}

// sameBinary reports whether two paths denote the same executable.
//
// Symlinks are resolved on both sides, and that is the whole difficulty.
// Homebrew installs dun as /opt/homebrew/bin/dun -> ../Cellar/dun/<v>/bin/dun,
// so LookPath returns the link while os.Executable returns its target.
// Comparing the raw strings would warn "the hooks will run a different dun"
// at every Homebrew user, on every `dun init` — a false alarm on the most
// common install route, which is worse than not warning at all.
//
// A path that cannot be resolved is compared as given: EvalSymlinks fails
// on a path that no longer exists, and a missing binary is a real answer
// here rather than a reason to skip the check.
//
// On Windows the comparison is case-insensitive, because PATH lookup is.
func sameBinary(a, b string) bool {
	a, b = resolveBinary(a), resolveBinary(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func resolveBinary(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
