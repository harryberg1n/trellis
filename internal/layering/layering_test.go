package layering

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestLayering_FixtureWithClockAndOsReportsBoth(t *testing.T) {
	got, err := Check([]string{"testdata/badkernel"}, []string{"os"}, []string{"time.Now"})
	if err != nil {
		t.Fatal(err)
	}

	want := []Finding{
		{File: "badkernel/k.go", Line: 5, What: "imports os"},
		{File: "badkernel/k.go", Line: 9, What: "calls time.Now"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %+v, want %+v", got, want)
	}
	if s := got[0].String(); s != "badkernel/k.go:5: imports os" {
		t.Errorf("String() = %q, want %q", s, "badkernel/k.go:5: imports os")
	}
}

func TestLayering_KernelAndMeasureAreClean(t *testing.T) {
	dirs := []string{"../kernel", "../measure"}
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatalf("%s must exist and be scanned: %v", d, err)
		}
		files := 0
		for _, e := range entries {
			if n := e.Name(); !e.IsDir() && strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
				files++
			}
		}
		if files == 0 {
			t.Fatalf("%s has no non-test .go file; the law scanned nothing", d)
		}
	}

	got, err := Check(dirs, ForbiddenImports, ForbiddenCalls)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("findings = %+v, want none", got)
	}
}

func TestLayering_DurationTypeIsAllowed(t *testing.T) {
	got, err := Check([]string{"testdata/durationonly"}, ForbiddenImports, ForbiddenCalls)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("findings = %+v, want none", got)
	}
}

// The law follows the import, not the spelling: an alias for time is still
// time, and a local type that happens to be called time is not.
func TestLayering_FollowsImportAliasesAndIgnoresShadowingTypes(t *testing.T) {
	got, err := Check([]string{"testdata/aliased"}, ForbiddenImports, ForbiddenCalls)
	if err != nil {
		t.Fatal(err)
	}
	want := []Finding{{File: "aliased/a.go", Line: 7, What: "calls time.Sleep"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %+v, want %+v", got, want)
	}
}

func TestLayering_TheForbiddenSetIsTheSpecs(t *testing.T) {
	if want := []string{"os", "os/exec"}; !reflect.DeepEqual(ForbiddenImports, want) {
		t.Errorf("ForbiddenImports = %v, want %v", ForbiddenImports, want)
	}
	want := []string{"time.Now", "time.Since", "time.Until", "time.Sleep", "time.After", "time.Tick", "time.NewTimer", "time.NewTicker"}
	if !reflect.DeepEqual(ForbiddenCalls, want) {
		t.Errorf("ForbiddenCalls = %v, want %v", ForbiddenCalls, want)
	}
}

func TestCheck_ADirThatDoesNotExistIsSkippedAndASyntaxErrorIsReported(t *testing.T) {
	if got, err := Check([]string{"testdata/nope"}, ForbiddenImports, ForbiddenCalls); err != nil || len(got) != 0 {
		t.Errorf("missing dir: %+v, %v; want nothing", got, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/bad.go", []byte("package x\nfunc {"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Check([]string{dir}, ForbiddenImports, ForbiddenCalls); err == nil {
		t.Error("a file that does not parse gave no error")
	}
}

// A test file may use the real clock and the real filesystem: the law is about
// the code that ships.
func TestCheck_TestFilesAreNotChecked(t *testing.T) {
	dir := t.TempDir()
	src := "package x\nimport \"os\"\nvar _ = os.Args\n"
	if err := os.WriteFile(dir+"/x_test.go", []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Check([]string{dir}, ForbiddenImports, ForbiddenCalls); err != nil || len(got) != 0 {
		t.Errorf("%+v, %v; want nothing", got, err)
	}
}

// A dot-import brings the clock in under bare names; the call is still a call
// to time.Now.
func TestLayering_DotImportedClockCallIsCaught(t *testing.T) {
	got, err := Check([]string{"testdata/dotimport"}, []string{"os"}, []string{"time.Now"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Finding{{File: "dotimport/d.go", Line: 5, What: "calls time.Now"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %+v, want %+v", got, want)
	}
}
