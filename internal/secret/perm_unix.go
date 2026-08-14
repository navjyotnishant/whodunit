// Author: Navjyot Nishant
// Created: 2026-08-14
// Last updated: 2026-08-14
// Description: Owner-only file permissions via the Unix mode bits.

//go:build !windows

package secret

import (
	"fmt"
	"os"
)

// secureFile narrows the file to owner-only.
//
// Applied after writing rather than relying on the mode passed to
// os.WriteFile, which is masked by the process umask and ignored entirely
// for a file that already exists — so a file that was once world-readable
// would silently stay that way.
func secureFile(path string) error {
	return os.Chmod(path, FileMode)
}

// checkFile reports the file as too permissive if any bit outside the owner
// triad is set. Empty means fine.
func checkFile(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if mode := info.Mode().Perm(); mode&^FileMode != 0 {
		return fmt.Sprintf("%04o", mode)
	}
	return ""
}
