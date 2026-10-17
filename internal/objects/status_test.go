package objects

import (
	"io/fs"
	"reflect"
	"syscall"
	"testing"
)

func TestStatusPaths(t *testing.T) {
	out := "?? new file.txt\x00 M dir/mod.go\x00R  to.go\x00from.go\x00 D gone\x00A  added\x00"
	want := []string{"new file.txt", "dir/mod.go", "to.go", "from.go", "gone", "added"}
	if got := statusPaths([]byte(out)); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := statusPaths(nil); len(got) != 0 {
		t.Fatalf("empty status gave %q", got)
	}
}

func TestPathGone(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"not exist", &fs.PathError{Op: "lstat", Path: "x", Err: fs.ErrNotExist}, true},
		{"not a directory", &fs.PathError{Op: "lstat", Path: "d/x.txt", Err: syscall.ENOTDIR}, true},
		{"permission", &fs.PathError{Op: "lstat", Path: "x", Err: fs.ErrPermission}, false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := pathGone(c.err); got != c.want {
			t.Errorf("%s: pathGone = %v, want %v", c.name, got, c.want)
		}
	}
}
