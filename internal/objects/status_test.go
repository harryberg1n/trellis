package objects

import (
	"reflect"
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
