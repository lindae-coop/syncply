package client

import "os"

// browseRoots: o cartafol persoal e as unidades (C:\, D:\…).
func browseRoots(home string) []string {
	var out []string
	if home != "" {
		out = append(out, home)
	}
	for c := 'A'; c <= 'Z'; c++ {
		d := string(c) + `:\`
		if _, err := os.Stat(d); err == nil {
			out = append(out, d)
		}
	}
	return out
}
