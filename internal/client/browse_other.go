//go:build !windows

package client

import (
	"os"
	"os/user"
	"path/filepath"
)

// browseRoots: o cartafol persoal, a raíz e onde se montan os discos externos.
func browseRoots(home string) []string {
	var out []string
	if home != "" {
		out = append(out, home)
	}
	out = append(out, "/")
	cands := []string{"/media", "/mnt", "/Volumes"}
	if u, err := user.Current(); err == nil {
		cands = append([]string{filepath.Join("/run/media", u.Username), filepath.Join("/media", u.Username)}, cands...)
	}
	seen := map[string]bool{}
	for _, d := range cands {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() && !seen[d] {
			if entries, err := os.ReadDir(d); err == nil && len(entries) > 0 {
				out = append(out, d)
				seen[d] = true
			}
		}
	}
	return out
}
