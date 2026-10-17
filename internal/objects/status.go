package objects

import "bytes"

// statusPaths is the paths named by `git status -z --untracked-files=all`
// output: records of "XY path", where a rename or copy (X or Y is R or C) is
// followed by one more record, the path it came from. Both are returned: the
// new path to hash, the old one to find gone.
func statusPaths(out []byte) []string {
	var paths []string
	recs := bytes.Split(out, []byte{0})
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 4 || rec[2] != ' ' {
			continue
		}
		paths = append(paths, string(rec[3:]))
		if x, y := rec[0], rec[1]; x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			i++
			if i < len(recs) && len(recs[i]) > 0 {
				paths = append(paths, string(recs[i]))
			}
		}
	}
	return paths
}
