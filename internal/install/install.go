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
	Common  string // the repository's common git dir; "" skips the exclude list
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
	exclude, excludeAction, err := addExclude(o.Common)
	if err != nil {
		return nil, err
	}
	if excludeAction != "" {
		actions = append(actions, excludeAction)
	}
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
	if exclude != nil {
		if err := writeExclude(o.Common, exclude); err != nil {
			return nil, err
		}
	}
	note, err := recordInstall(o.Root, o.Repo)
	if err != nil {
		return nil, err
	}
	if note != "" {
		actions = append(actions, note)
	}
	return actions, nil
}

// excludeEntry is the line install adds to the repository's local exclude
// list (<common dir>/info/exclude): the settings file it writes is untracked
// in every installed checkout, and nothing that counts untracked files, the
// discard wall or a tree landing, should count a file trellis itself made.
const excludeEntry = ".claude/settings.local.json"

func excludePath(common string) string {
	return filepath.Join(common, "info", "exclude")
}

// addExclude returns the exclude list with the entry added, or nil with a
// "keep" action when it is there already, or nil and "" when there is no
// common dir to write to. Foreign lines are kept byte for byte.
func addExclude(common string) (next []byte, action string, err error) {
	if common == "" {
		return nil, "", nil
	}
	cur, err := os.ReadFile(excludePath(common))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	if hasLine(cur, excludeEntry) {
		return nil, "keep exclude " + excludeEntry, nil
	}
	next = append([]byte{}, cur...)
	if len(next) > 0 && next[len(next)-1] != '\n' {
		next = append(next, '\n')
	}
	next = append(next, excludeEntry+"\n"...)
	return next, "exclude " + excludeEntry + " from git status", nil
}

// removeExclude returns the exclude list without the entry, or nil when it
// holds none. Every other byte stays. A line the user wrote before install,
// identical to the entry, cannot be told from ours and goes with it.
func removeExclude(common string) (next []byte, action string, err error) {
	if common == "" {
		return nil, "", nil
	}
	cur, err := os.ReadFile(excludePath(common))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", err
	}
	if !hasLine(cur, excludeEntry) {
		return nil, "", nil
	}
	lines := strings.SplitAfter(string(cur), "\n")
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimRight(l, "\r\n") != excludeEntry {
			kept = append(kept, l)
		}
	}
	return []byte(strings.Join(kept, "")), "remove exclude " + excludeEntry, nil
}

func hasLine(text []byte, line string) bool {
	for _, l := range strings.Split(string(text), "\n") {
		if strings.TrimRight(l, "\r") == line {
			return true
		}
	}
	return false
}

func writeExclude(common string, content []byte) error {
	dir := filepath.Dir(excludePath(common))
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}
	return store.WriteAtomic(dir, filepath.Base(excludePath(common)), content, store.WithMode(0o644), store.WithTempPattern(".exclude-*"))
}

// pin copies the binary at src to bin through a temp file, so a half copy
// never sits at the path a hook runs. The caller has seen bin absent.
func pin(bin, src string) error {
	content, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	dir := filepath.Dir(bin)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}
	return store.WriteAtomic(dir, filepath.Base(bin), content, store.WithMode(exeMode), store.WithTempPattern(".pin-*"))
}

func writeSettings(repo string, doc []byte) error { return writeSettingsWith(repo, doc) }

// writeSettingsWith writes the settings file through a temp file in its own
// directory and a rename, so a crash or an overlapping install leaves the old
// file or the whole new one, never half. A symlinked file (or .claude dir) is
// resolved first: the link target is replaced and the link stays.
func writeSettingsWith(repo string, doc []byte, opts ...store.AtomicOption) error {
	path := SettingsPath(repo)
	claude := filepath.Dir(path)
	if err := os.MkdirAll(claude, dirMode); err != nil {
		return err
	}
	dir, err := filepath.EvalSymlinks(claude)
	if err != nil {
		return err
	}
	target, err := resolveLink(filepath.Join(dir, filepath.Base(path)))
	if err != nil {
		return err
	}
	mode := fileMode
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm()
	}
	dir = filepath.Dir(target)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}
	opts = append([]store.AtomicOption{store.WithMode(mode), store.WithTempPattern(".settings-*")}, opts...)
	return store.WriteAtomic(dir, filepath.Base(target), doc, opts...)
}

// resolveLink follows path while it is a symlink, a dangling one included.
func resolveLink(path string) (string, error) {
	for range 16 {
		fi, err := os.Lstat(path)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		dest, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(dest) {
			dest = filepath.Join(filepath.Dir(path), dest)
		}
		path = dest
	}
	return "", fmt.Errorf("%s: too many levels of symbolic links", path)
}

// Eject removes the entries Install added, from this repo's settings, and
// returns one line per removal. Everything else in the file is left byte for
// byte. The pinned binaries stay: other repos may point at them. The repo is
// then dropped from the record `trellis update` reads.
func Eject(o Options) ([]string, error) {
	actions, err := eject(o)
	if err != nil {
		return actions, err
	}
	return actions, ForgetRepo(o.Root, o.Repo)
}

func eject(o Options) ([]string, error) {
	doc, err := readSettings(o.Repo)
	if err != nil {
		return nil, err
	}
	var next []byte
	var actions []string
	if doc != nil {
		if next, actions, err = removeEntries(doc, ownedBy(o.Root)); err != nil {
			return nil, fmt.Errorf("%s: %w", SettingsPath(o.Repo), err)
		}
	}
	exclude, excludeAction, err := removeExclude(o.Common)
	if err != nil {
		return nil, err
	}
	if len(actions) == 0 && exclude == nil {
		return nil, nil
	}
	if len(actions) > 0 {
		if err := writeSettings(o.Repo, next); err != nil {
			return nil, err
		}
	}
	if exclude != nil {
		if err := writeExclude(o.Common, exclude); err != nil {
			return nil, err
		}
		actions = append(actions, excludeAction)
	}
	return actions, nil
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
