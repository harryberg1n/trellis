package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workflow is the text of a workflow file.
func workflow(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// step is the text of the step called name: from its "- name:" line to the
// next step.
func step(t *testing.T, text, name string) string {
	t.Helper()
	start := strings.Index(text, "- name: "+name+"\n")
	if start < 0 {
		start = strings.Index(text, "- name: "+name)
	}
	if start < 0 {
		t.Fatalf("no step %q", name)
	}
	rest := text[start+len("- name: "):]
	if next := strings.Index(rest, "\n      - "); next >= 0 {
		rest = rest[:next]
	}
	return rest
}

// A release re-run after a partial upload finishes the upload: the release is
// created without assets, the assets go up with --clobber, the sums last, and
// a release that already has its sums is left alone. A gh failure that is not
// "not found" stops the run: it must not be answered by a create.
func TestRelease_ARerunAfterAPartialUploadFinishesIt(t *testing.T) {
	text := workflow(t, "release.yml")
	tag := step(t, text, "the tag this commit earns")
	publish := step(t, text, "publish")

	for _, want := range []string{
		`said=$(gh release view "$tag" --json assets --jq '.assets[].name' 2>&1)`,
		`if grep -qx sha256sums.txt <<<"$said"; then`,
		`echo "[skip] release $tag already has its checksums"`,
		`*"not found"*) ;;`,
		`gh release view $tag failed, so no release is created: $said`,
		`echo "exists=true" >> "$GITHUB_OUTPUT"`,
	} {
		if !strings.Contains(tag, want) {
			t.Errorf("the tag step lacks %q", want)
		}
	}
	create := strings.Index(publish, `gh release create "$TAG" --target "$GITHUB_SHA" --title "$TAG" --generate-notes`)
	binaries := strings.Index(publish, `gh release upload "$TAG" dist/trellis-* --clobber`)
	sums := strings.Index(publish, `gh release upload "$TAG" dist/sha256sums.txt --clobber`)
	if create < 0 || binaries < create || sums < binaries {
		t.Errorf("publish must create, upload the binaries, then upload the sums (at %d, %d, %d):\n%s", create, binaries, sums, publish)
	}
	if !strings.Contains(publish, `if [ "$EXISTS" != true ]; then`) {
		t.Errorf("publish creates a release that already exists:\n%s", publish)
	}
	if strings.Contains(publish, "dist/* --target") {
		t.Errorf("publish still creates the release and uploads in one call:\n%s", publish)
	}
	if !strings.Contains(step(t, text, "build"), `if: steps.tag.outputs.skip != 'true'`) {
		t.Errorf("a release with its checksums is rebuilt")
	}
}

// gremlins sizes each mutant's timeout from the time its coverage gather took,
// so a gather answered from Go's test cache would time out every mutant.
func TestNightlyMutants_TheGatherIsNeverServedFromTheTestCache(t *testing.T) {
	text := workflow(t, "nightly-mutants.yml")

	run := step(t, text, "mutate the pure packages")

	if !strings.Contains(run, "env:\n          GOFLAGS: -count=1\n") {
		t.Errorf("the gremlins step does not set GOFLAGS=-count=1:\n%s", run)
	}
}

// The flake hunt keeps the machine-readable stream and a plain log, and an
// empty stream after a failed run is an infrastructure failure, not a clean
// night. Each runner uploads under its own name.
func TestNightlyFlakeHunt_KeepsTheJSONStreamAndNamesAnEmptyOne(t *testing.T) {
	text := workflow(t, "nightly-flake-hunt.yml")

	run := step(t, text, "the suite under -race -shuffle=on -count=5")
	upload := step(t, text, "keep the test streams")

	for _, want := range []string{
		`go test -race -shuffle=on -count=5 -timeout=3000s -json ./... > test.json`,
		`if ! jq -rj 'select(.Action=="output") | .Output' test.json > test.log; then`,
		`::error::the JSON stream could not be turned into a log (jq missing, or the stream is cut off)`,
		`tail -n 200 test.log`,
		`if [ "$code" != 0 ] && ! grep -q . test.json; then`,
		`::error::go test exited $code with an empty JSON stream: an infrastructure failure, not a flake`,
		`exit "$code"`,
	} {
		if !strings.Contains(run, want) {
			t.Errorf("the suite step lacks %q", want)
		}
	}
	for _, want := range []string{"if: always()", "name: flake-hunt-${{ matrix.runner }}", "test.json", "test.log"} {
		if !strings.Contains(upload, want) {
			t.Errorf("the upload step lacks %q", want)
		}
	}
	if strings.Contains(run, "|| true") || strings.Contains(run, "cat test.log") {
		t.Errorf("the suite step hides a jq failure or prints the whole log:\n%s", run)
	}
	if strings.Contains(text, "flakehunt") || strings.Contains(text, "gh issue") {
		t.Errorf("the flake hunt files issues; a red night is read by a person")
	}
}
