package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/store"
)

// timeoutSeconds is every trellis hook's timeout. A hook that cannot finish
// in this time is the harness's to cut off, never the agent's to wait on.
const timeoutSeconds = 10

// toolMatcher is the PreToolUse and PostToolUse matcher: the tools that edit
// files or run commands.
const toolMatcher = "Edit|Write|Bash|PowerShell"

// Options says where to install and what.
type Options struct {
	Repo    string // the repository root
	Root    string // the state root
	Exe     string // the running binary, the source of the pinned copy
	Version string // buildinfo.Version of that binary
	Dry     bool   // report the actions without performing them

	AllowDev bool // pin a build whose version is "dev"
}

// SettingsPath is the file trellis installs into: the project-local settings,
// which the harness keeps out of version control, so a consumer repository's
// tracked files and CI never change.
func SettingsPath(repo string) string {
	return filepath.Join(repo, ".claude", "settings.local.json")
}

// wanted is the entry for each event, in the order they are installed.
func wanted(bin string) []eventEntry {
	type spec struct{ event, matcher string }
	specs := make([]spec, 0, len(kernel.InstalledHookEvents()))
	for _, event := range kernel.InstalledHookEvents() {
		matcher := ""
		if event == kernel.HookPreToolUse || event == kernel.HookPostToolUse {
			matcher = toolMatcher
		}
		specs = append(specs, spec{event, matcher})
	}
	out := make([]eventEntry, 0, len(specs))
	for _, s := range specs {
		out = append(out, eventEntry{Event: s.event, Entry: Entry{
			Matcher: s.matcher,
			Hooks:   []Hook{{Type: "command", Command: Command(bin, s.event), Timeout: timeoutSeconds}},
		}})
	}
	return out
}

// ownedBy is the ownership test for a state root: a hook whose executable is a
// pinned trellis binary under it. It is asked of single hooks, so a foreign
// hook beside a trellis one in the same entry is never touched.
func ownedBy(root string) func(Hook) bool {
	return func(h Hook) bool {
		_, ok := Owned(h.Command, root)
		return ok
	}
}

// readSettings returns the settings file's bytes, none when it is absent.
func readSettings(repo string) ([]byte, error) {
	b, err := os.ReadFile(SettingsPath(repo))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// Install pins the running binary under the state root and points the repo's
// hooks at the pinned copy. It validates everything before it writes anything,
// and returns one line per action; with Dry the lines start "would " and
// nothing is written.
func Install(o Options) ([]string, error) {
	if o.Version == "" || (o.Version == "dev" && !o.AllowDev) {
		return nil, errors.New("refusing to pin an unversioned build; pass --allow-dev")
	}
	if o.Version == ".." || strings.ContainsAny(o.Version, `/\`) {
		return nil, fmt.Errorf("version %q cannot name a directory", o.Version)
	}
	bin := BinPath(o.Root, o.Version, filepath.Ext(o.Exe))
	doc, err := readSettings(o.Repo)
	if err != nil {
		return nil, err
	}
	next, hookActions, err := addEntries(doc, wanted(bin), ownedBy(o.Root))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", SettingsPath(o.Repo), err)
	}
	// An existing pinned file is kept whatever it holds: same version, same
	// bytes expected, and on Windows a running hook may hold it open.
	verb := "copy"
	if _, err := os.Stat(bin); err == nil {
		verb = "keep"
	}
	actions := append([]string{verb + " binary " + bin}, hookActions...)
	if o.Dry {
		for i, a := range actions {
			actions[i] = "would " + a
		}
		return actions, nil
	}
	if verb == "copy" {
		if err := pin(bin, o.Exe); err != nil {
			return nil, err
		}
	}
	if err := writeSettings(o.Repo, next); err != nil {
		return nil, err
	}
	return actions, nil
}

// pin copies the binary at src to bin through a temp file, so a half copy
// never sits at the path a hook runs. The caller has seen bin absent.
func pin(bin, src string) error {
	content, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	dir := filepath.Dir(bin)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return store.WriteAtomic(dir, filepath.Base(bin), content, store.WithMode(0o755), store.WithTempPattern(".pin-*"))
}

func writeSettings(repo string, doc []byte) error {
	path := SettingsPath(repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, doc, 0o644)
}

// Eject removes the entries Install added, from this repo's settings, and
// returns one line per removal. Everything else in the file is left byte for
// byte. The pinned binaries stay: other repos may point at them.
func Eject(o Options) ([]string, error) {
	doc, err := readSettings(o.Repo)
	if err != nil || doc == nil {
		return nil, err
	}
	next, actions, err := removeEntries(doc, ownedBy(o.Root))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", SettingsPath(o.Repo), err)
	}
	if len(actions) == 0 {
		return nil, nil
	}
	return actions, writeSettings(o.Repo, next)
}

// Doctor returns one line per problem: no install, a hook pinned to another
// version than the running binary, a pinned binary gone, a state root that
// cannot be written. No lines means all is well.
func Doctor(o Options) []string {
	var problems []string
	doc, err := readSettings(o.Repo)
	if err == nil {
		var found []located
		if found, err = walkEntries(doc); err == nil {
			problems = append(problems, checkHooks(found, o)...)
		}
	}
	if err != nil {
		problems = append(problems, fmt.Sprintf("cannot read %s: %v", SettingsPath(o.Repo), err))
	}
	if !writable(o.Root) {
		problems = append(problems, "state root not writable: "+o.Root)
	}
	return problems
}

func checkHooks(found []located, o Options) []string {
	var problems []string
	seen := map[string]bool{}
	ours := 0
	for _, l := range found {
		for _, h := range l.Entry.Hooks {
			version, ok := Owned(h.Command, o.Root)
			if !ok {
				continue
			}
			ours++
			bin, _ := executable(h.Command)
			if version != o.Version && !seen[version] {
				seen[version] = true
				problems = append(problems, fmt.Sprintf("plugin %s, binary %s", version, o.Version))
			}
			if _, err := os.Stat(bin); err != nil && !seen[bin] {
				seen[bin] = true
				problems = append(problems, "pinned binary missing: "+bin)
			}
		}
	}
	if ours == 0 {
		problems = append(problems, "trellis is not installed: "+SettingsPath(o.Repo))
	}
	return problems
}

// writable reports whether a file can be made and removed in the root. An
// absent root is probed through its nearest existing parent: doctor creates
// no directory of its own.
func writable(root string) bool {
	dir := root
	for {
		info, err := os.Stat(dir)
		if err == nil {
			if !info.IsDir() {
				return false
			}
			break
		}
		up := filepath.Dir(dir)
		if !errors.Is(err, os.ErrNotExist) || up == dir {
			return false
		}
		dir = up
	}
	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return false
	}
	name := f.Name()
	cerr := f.Close()
	return errors.Join(cerr, os.Remove(name)) == nil
}
