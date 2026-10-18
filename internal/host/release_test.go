package host

import (
	"reflect"
	"testing"

	"github.com/harryberg1n/trellis/internal/testsupport"
)

func TestGH_LatestReleaseReadsTheTagName(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: testsupport.Stdout(`{"tag_name":"v0.0.4","name":"v0.0.4","draft":false}`)}

	got, err := GH{Runner: r}.LatestRelease("harryberg1n/trellis")
	if err != nil || got != "v0.0.4" {
		t.Fatalf("LatestRelease = %q, %v; want v0.0.4", got, err)
	}
	if want := []string{"api", "repos/harryberg1n/trellis/releases/latest"}; !reflect.DeepEqual(r.Specs()[0].Args, want) {
		t.Errorf("argv = %v, want %v", r.Specs()[0].Args, want)
	}

	empty := &testsupport.FakeRunner{Answer: testsupport.Stdout(`{}`)}
	if _, err := (GH{Runner: empty}).LatestRelease("harryberg1n/trellis"); err == nil {
		t.Error("a release with no tag_name was accepted")
	}
}

func TestGH_DownloadReleaseArgv(t *testing.T) {
	r := &testsupport.FakeRunner{Answer: testsupport.Stdout("")}

	if err := (GH{Runner: r}).DownloadRelease("harryberg1n/trellis", "v0.0.4", "stage", "trellis-linux-amd64", "sha256sums.txt"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"release", "download", "v0.0.4", "--repo", "harryberg1n/trellis", "--dir", "stage", "--clobber",
		"--pattern", "trellis-linux-amd64", "--pattern", "sha256sums.txt",
	}
	if got := r.Specs()[0].Args; !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}
