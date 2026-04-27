// Package repo finds the project's repo root from any cwd. Used by the
// extractor cmds so `--out lib/tokens/<brand>` always resolves to the same
// location regardless of where the user invoked `go run`.
package repo

import (
	"os"
	"path/filepath"
)

// Root walks up from cwd until it finds a directory containing both
// "package.json" and a ".git" directory (the repo's hallmarks). Falls back
// to cwd if neither is found within 8 levels.
func Root() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	dir := cwd
	for i := 0; i < 8; i++ {
		if exists(filepath.Join(dir, "package.json")) && exists(filepath.Join(dir, ".git")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return cwd
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
