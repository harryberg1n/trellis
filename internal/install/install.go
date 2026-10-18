package install

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/kernel"
	"github.com/harryberg1n/trellis/internal/objects"
	"github.com/harryberg1n/trellis/internal/store"
)

// timeoutSeconds is every trellis hook's timeout. A hook that cannot finish
// in this time is the harness's to cut off, never the agent's to wait on.
const timeoutSeconds = 10

// toolMatcher is the PreToolUse and PostToolUse matcher: the tools that edit
// files or run commands.
const toolMatcher = "Edit|Write|Bash|PowerShell"

// markerText is the whole of a trellis.toml install writes: no keys yet.
const markerText = "# trellis is on in this repository.\n# Commit this file; a repository without it is off.\n"

// Options says where to install and what.
type Options struct {
	Repo      string // a checkout of the repository (a linked worktree stands for its primary); "" touches no repository
	ConfigDir string // the Claude config directory whose settings.json holds the hooks
	Root      string // the state root
	Exe       string // the running binary, the source of the pinned copy
	Version   string // buildinfo.Version of that binary
	Dry       bool   // report the actions without performing them

	AllowDev bool // pin a build whose version is "dev"

	here string // the checkout the verb runs in, set by atPrimary; "" when Repo is none

	// Clock names the backup of an existing settings file; nil is the wall clock.
	Clock clock.Clock
}

// atPrimary is o with Repo naming the primary checkout of its repository: a
// linked worktree stands for its primary, whose root holds the committed
// trellis.toml and is the one path the record of repositories keeps. The
// checkout Repo named stays in here: the marker is written and checked there.
// A Repo that is not a repository, or "", is left as it is.
func (o Options) atPrimary() Options {
	o.here = o.Repo
	if r, err := objects.Open(o.Repo); o.Repo != "" && err == nil {
		o.here, o.Repo = r.Root, r.PrimaryRoot()
	}
	return o
}

// recordOptions is the options of a record update: the clock, when one is set.
func (o Options) recordOptions() []RecordOption {
	if o.Clock == nil {
		return nil
	}
	return []RecordOption{WithClock(o.Clock)}
}

// ConfigDir is the Claude config directory: dir when given, else
// $CLAUDE_CONFIG_DIR, else ~/.claude.
//
// Follows aphrollo-tools internal/cli/cli_gate_init.go defaultClaudeDir at
// 12b3724e, which resolves the directory the way the harness does. What it
// encodes: a harness started with its own CLAUDE_CONFIG_DIR reads its hooks
// from there, so wiring ~/.claude would leave it unhooked.
func ConfigDir(dir string) (string, error) {
	if dir == "" {
		dir = os.Getenv("CLAUDE_CONFIG_DIR")
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".claude")
	}
	return filepath.Abs(dir)
}

// SettingsFile is the user-level settings file in a config directory: the one
// file the hooks are wired in, once per user.
func SettingsFile(configDir string) string { return filepath.Join(configDir, "settings.json") }

// retiredSettings is the project-local settings file older installs wired the
// hooks in, per checkout. Install and update remove trellis's own entries from
// it: an entry left there fires every event a second time.
func retiredSettings(checkout string) string {
	return filepath.Join(checkout, ".claude", "settings.local.json")
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

// readSettings returns the bytes of the settings file at path, none when it is
// absent.
func readSettings(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// wirePlan is a validated wiring of the user-level settings, not yet written.
type wirePlan struct {
	file    string
	bin     string
	prev    []byte // the file as it is, nil when absent
	next    []byte
	copyBin bool
	actions []string
}

// planWire validates the build and the settings file and works out the edit.
func planWire(o Options) (wirePlan, error) {
	if o.Version == "" || (o.Version == "dev" && !o.AllowDev) {
		return wirePlan{}, errors.New("refusing to pin an unversioned build; pass --allow-dev")
	}
	if o.Version == ".." || strings.ContainsAny(o.Version, `/\`) {
		return wirePlan{}, fmt.Errorf("version %q cannot name a directory", o.Version)
	}
	p := wirePlan{file: SettingsFile(o.ConfigDir), bin: BinPath(o.Root, o.Version, filepath.Ext(o.Exe))}
	var err error
	if p.prev, err = readSettings(p.file); err != nil {
		return wirePlan{}, err
	}
	var hookActions []string
	if p.next, hookActions, err = addEntries(p.prev, wanted(p.bin), ownedBy(o.Root)); err != nil {
		return wirePlan{}, fmt.Errorf("%s: %w", p.file, err)
	}
	// An existing pinned file is kept whatever it holds: same version, same
	// bytes expected, and on Windows a running hook may hold it open.
	verb := "copy"
	if _, err := os.Stat(p.bin); err == nil {
		verb = "keep"
	}
	p.copyBin = verb == "copy"
	p.actions = append([]string{verb + " binary " + p.bin}, hookActions...)
	return p, nil
}

// changed says whether applying the plan edits the settings file.
func (p wirePlan) changed() bool { return p.prev == nil || !bytes.Equal(p.prev, p.next) }

// apply pins the binary and writes the settings file, an existing one backed
// up first. It returns the backup's path, "" when none was made.
func (p wirePlan) apply(o Options) (backup string, err error) {
	if p.copyBin {
		if err := pin(p.bin, o.Exe); err != nil {
			return "", err
		}
	}
	if !p.changed() {
		return "", nil
	}
	if p.prev != nil {
		if backup, err = backUp(p.file, p.prev, o.Clock); err != nil {
			return "", err
		}
	}
	return backup, writeSettings(p.file, p.next)
}

// backUp copies the settings file beside itself as <file>.pre-trellis-<unix
// seconds> before its first change; one already there is the first and is
// kept. It returns the new backup's path, "" when none was made.
//
// Follows aphrollo-tools internal/tdd/install/init.go InitSettings at
// 12b3724e. What it encodes: the user's own settings file is edited, so a copy
// of it sits beside it first. The reference copies before every change; this
// keeps the copy of the file as it was before the first one.
func backUp(file string, content []byte, clk clock.Clock) (string, error) {
	if hasBackup(file) {
		return "", nil
	}
	if clk == nil {
		clk = clock.Real()
	}
	path := file + ".pre-trellis-" + strconv.FormatInt(clk.Now().Unix(), 10)
	err := store.WriteAtomic(filepath.Dir(path), filepath.Base(path), content, store.WithMode(fileMode), store.WithTempPattern(".backup-*"))
	if err != nil {
		return "", fmt.Errorf("writing backup %s: %w", path, err)
	}
	return path, nil
}

// hasBackup reports whether a backup of the settings file sits beside it. It
// lists the directory and matches the name's prefix: the path is never a glob
// pattern, so a config directory with a bracket in its name is read as it is.
func hasBackup(file string) bool {
	entries, _ := os.ReadDir(filepath.Dir(file))
	prefix := filepath.Base(file) + ".pre-trellis-"
	return slices.ContainsFunc(entries, func(e os.DirEntry) bool { return strings.HasPrefix(e.Name(), prefix) })
}

// wouldAll marks every action as not done.
func wouldAll(actions []string) []string {
	out := make([]string, len(actions))
	for i, a := range actions {
		out[i] = "would " + a
	}
	return out
}

// Wire pins the running binary under the state root and points the six hook
// entries of the user-level settings at the pinned copy. It validates before
// it writes, and returns one line per action; with Dry the lines start "would "
// and nothing is written. It touches no repository.
func Wire(o Options) ([]string, error) {
	p, err := planWire(o)
	if err != nil {
		return nil, err
	}
	if o.Dry {
		return wouldAll(p.actions), nil
	}
	backup, err := p.apply(o)
	if err != nil {
		return nil, err
	}
	return withBackup(p.actions, backup, p.file), nil
}

// withBackup adds the backup action, if one was made, after the binary's.
func withBackup(actions []string, backup, file string) []string {
	if backup == "" {
		return actions
	}
	line := "back up " + file + " to " + backup
	return append(actions[:1:1], append([]string{line}, actions[1:]...)...)
}

// Install wires the user-level settings (see Wire), writes trellis.toml at the
// root of the checkout it runs in (a linked worktree's own, committed from there)
// when that has none, and removes trellis's entries from the
// repository's project-local settings and from each linked worktree's. It
// records the repository for `trellis update`. It validates everything before
// it writes anything, and returns one line per action; with Dry the lines
// start "would " and nothing is written.
func Install(o Options) ([]string, error) {
	o = o.atPrimary()
	p, err := planWire(o)
	if err != nil {
		return nil, err
	}
	actions := p.actions
	var edits []prune
	var notes []string
	writeMarker := false
	if o.Repo != "" {
		if edits, notes, err = planPrune(o); err != nil {
			return nil, err
		}
		if writeMarker = !objects.HasMarker(o.here); writeMarker {
			actions = append(actions, "write "+filepath.Join(o.here, objects.MarkerFile))
		} else {
			actions = append(actions, "keep "+objects.MarkerFile)
		}
	}
	for _, e := range edits {
		actions = append(actions, e.actions...)
	}
	actions = append(actions, notes...)
	if o.Dry {
		return wouldAll(actions), nil
	}
	backup, err := p.apply(o)
	if err != nil {
		return nil, err
	}
	actions = withBackup(actions, backup, p.file)
	if writeMarker {
		err := store.WriteAtomic(o.here, objects.MarkerFile, []byte(markerText), store.WithMode(0o644), store.WithTempPattern(".trellis-*"))
		if err != nil {
			return nil, err
		}
	}
	if err := applyPrune(edits); err != nil {
		return nil, err
	}
	if o.Repo != "" {
		if err := RecordRepo(o.Root, o.Repo, o.recordOptions()...); err != nil {
			return nil, err
		}
	}
	return actions, nil
}

// prune is the removal of trellis's entries from one project-local settings
// file.
type prune struct {
	file    string
	next    []byte
	actions []string
}

// checkouts is the repository's primary checkout and each of its linked
// worktrees.
// A worktree that is not found is skipped, with a note line: the user-level
// settings never wait on a git worktree prune.
func checkouts(repo string) (dirs, notes []string, err error) {
	trees, missing, err := LinkedWorktrees(repo)
	if err != nil {
		return nil, nil, err
	}
	for _, m := range missing {
		notes = append(notes, "skip "+m+": not found, git worktree prune forgets it")
	}
	return append([]string{repo}, slices.DeleteFunc(trees, func(t string) bool { return t == repo })...), notes, nil
}

// planPrune finds the project-local settings of the repository and its linked
// worktrees that hold trellis entries and works out the edit of each. A file
// that is absent, or holds none, is left out. A file left with nothing of its
// own stays, as removeEntries leaves it.
// Notes are the skipped worktrees.
func planPrune(o Options) (out []prune, notes []string, err error) {
	dirs, notes, err := checkouts(o.Repo)
	if err != nil {
		return nil, nil, err
	}
	for _, dir := range dirs {
		file := retiredSettings(dir)
		doc, err := readSettings(file)
		if err != nil {
			return nil, nil, err
		}
		if doc == nil {
			continue
		}
		next, removed, err := removeEntries(doc, ownedBy(o.Root))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", file, err)
		}
		if len(removed) == 0 {
			continue
		}
		for i, r := range removed {
			removed[i] = r + " from " + file
		}
		out = append(out, prune{file: file, next: next, actions: removed})
	}
	return out, notes, nil
}

func applyPrune(edits []prune) error {
	for _, e := range edits {
		if err := writeSettings(e.file, e.next); err != nil {
			return err
		}
	}
	return nil
}

// Prune removes trellis's entries from the project-local settings of the
// repository and its linked worktrees, and returns one line per removal; with
// Dry the lines start "would " and nothing is written.
func Prune(o Options) ([]string, error) {
	o = o.atPrimary()
	edits, notes, err := planPrune(o)
	if err != nil {
		return nil, err
	}
	var actions []string
	for _, e := range edits {
		actions = append(actions, e.actions...)
	}
	actions = append(actions, notes...)
	if o.Dry {
		return wouldAll(actions), nil
	}
	return actions, applyPrune(edits)
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

func writeSettings(path string, doc []byte) error { return writeSettingsWith(path, doc) }

// writeSettingsWith writes the settings file through a temp file in its own
// directory and a rename, so a crash or an overlapping install leaves the old
// file or the whole new one, never half. A symlinked file (or directory) is
// resolved first: the link target is replaced and the link stays.
func writeSettingsWith(path string, doc []byte, opts ...store.AtomicOption) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, userDirMode); err != nil {
		return err
	}
	dir, err := filepath.EvalSymlinks(parent)
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
	// A symlink that points into a directory that is not there is not made good
	// by creating it: the link is the user's, and so is its target.
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("%s points into %s, which cannot be used: %w", path, dir, errors.Unwrap(err))
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

// Eject removes the entries Install added from the user-level settings, and
// the repository's own from its project-local settings and its linked
// worktrees', and returns one line per removal. Everything else in each file is
// left byte for byte. The pinned binaries stay: other repositories may be
// served by them. trellis.toml stays too, the repository's own committed file,
// and the last line says so. The repository is then dropped from the record
// `trellis update` reads.
func Eject(o Options) ([]string, error) {
	o = o.atPrimary()
	file := SettingsFile(o.ConfigDir)
	doc, err := readSettings(file)
	if err != nil {
		return nil, err
	}
	var next []byte
	var actions []string
	if doc != nil {
		if next, actions, err = removeEntries(doc, ownedBy(o.Root)); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
	}
	var edits []prune
	var notes []string
	if o.Repo != "" {
		if edits, notes, err = planPrune(o); err != nil {
			return nil, err
		}
	}
	if len(actions) > 0 {
		if err := writeSettings(file, next); err != nil {
			return nil, err
		}
	}
	if err := applyPrune(edits); err != nil {
		return nil, err
	}
	for _, e := range edits {
		actions = append(actions, e.actions...)
	}
	actions = append(actions, notes...)
	if o.Repo == "" {
		return actions, nil
	}
	if objects.HasMarker(o.here) {
		actions = append(actions, "keep "+objects.MarkerFile+": it is this repository's own committed file")
	}
	return actions, ForgetRepo(o.Root, o.Repo)
}

// Doctor returns one line per problem: no wiring, a hook pinned to another
// version than the running binary, a pinned binary gone, a repository without
// trellis.toml, a project-local file that still holds trellis hooks, a state
// root that cannot be written. No lines means all is well.
func Doctor(o Options) []string {
	o = o.atPrimary()
	var problems []string
	file := SettingsFile(o.ConfigDir)
	doc, err := readSettings(file)
	if err == nil {
		var found []located
		if found, err = walkEntries(doc); err == nil {
			problems = append(problems, checkHooks(found, o)...)
		}
	}
	if err != nil {
		problems = append(problems, fmt.Sprintf("cannot read %s: %v", file, err))
	}
	if o.Repo != "" {
		problems = append(problems, checkRepo(o)...)
	}
	if !writable(o.Root) {
		problems = append(problems, "state root not writable: "+o.Root)
	}
	return problems
}

// checkRepo is the doctor's view of one repository: on or off, and any
// project-local file that would fire the hooks a second time.
func checkRepo(o Options) []string {
	var problems []string
	if !objects.HasMarker(o.here) {
		problems = append(problems, objects.MarkerFile+" missing: this repository is off")
	}
	dirs, _, err := checkouts(o.Repo)
	if err != nil {
		return append(problems, "cannot list the linked worktrees: "+err.Error())
	}
	for _, dir := range dirs {
		file := retiredSettings(dir)
		doc, err := readSettings(file)
		if err == nil {
			var found []located
			if found, err = walkEntries(doc); err == nil {
				for _, l := range found {
					if slices.ContainsFunc(l.Entry.Hooks, ownedBy(o.Root)) {
						problems = append(problems, file+" still holds trellis hooks (every event fires twice): run trellis install")
						break
					}
				}
			}
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("cannot read %s: %v", file, err))
		}
	}
	return problems
}

func checkHooks(found []located, o Options) []string {
	var problems []string
	seen := map[string]bool{}
	ours := 0
	wired := map[string]bool{}
	for _, l := range found {
		for _, h := range l.Entry.Hooks {
			version, ok := Owned(h.Command, o.Root)
			if !ok {
				continue
			}
			ours++
			wired[l.Event] = true
			bin, _ := executable(h.Command)
			if version != o.Version && !seen[version] {
				seen[version] = true
				problems = append(problems, fmt.Sprintf("hook %s, binary %s", version, o.Version))
			}
			if _, err := os.Stat(bin); err != nil && !seen[bin] {
				seen[bin] = true
				problems = append(problems, "pinned binary missing: "+bin)
			}
		}
	}
	if ours == 0 {
		problems = append(problems, "trellis is not installed: "+SettingsFile(o.ConfigDir))
	} else if missing := slices.DeleteFunc(kernel.InstalledHookEvents(), func(e string) bool { return wired[e] }); len(missing) > 0 {
		problems = append(problems, "hook missing for "+strings.Join(missing, ", ")+": run trellis install")
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
