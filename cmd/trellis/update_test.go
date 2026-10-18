package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestUpdate_HelpUsesTheOneVoice(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"update", "--help"}, &out, &errb)
	const want = "usage: trellis update [--to VERSION] [--dry] [--allow-dev]\n"
	if got := out.String() + errb.String(); code != 0 || !strings.HasPrefix(got, want) {
		t.Errorf("trellis update --help: exit %d, output %q; want exit 0 starting with %q", code, got, want)
	}
}
