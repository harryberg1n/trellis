package tree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/harryberg1n/trellis/internal/config"
	"github.com/harryberg1n/trellis/internal/run"
)

// setUp readies a new tree at path as its trellis.toml says ([tree.new]):
// the links into the primary checkout first, then the commands, each shown
// as it runs. Issue #52 item 6: every threadspyp lane began with
// `ln -s ../threadspyp/node_modules node_modules`, and a check run before it
// made a real node_modules that the later link landed inside. Issue #50 item
// 3: the first check of every fanpyp lane paid about 1m45s for setup.
func setUp(env Env, primary, path string, n config.New) error {
	for _, p := range n.Link {
		if err := linkInto(env, primary, path, p); err != nil {
			return fmt.Errorf("tree new: link %s: %w; the tree is at %s", p, err, path)
		}
	}
	for _, cmd := range n.Run {
		env.say("run: %s", cmd)
		prog, args := shellCommand(cmd)
		tail := &lastLine{out: env.Out}
		res, err := env.runner().Light(run.Spec{Name: prog, Args: args, Dir: path, Timeout: checkTimeout, Stdout: tail, Stderr: tail})
		if err != nil {
			return fmt.Errorf("tree new: run %q failed: %s; the tree is at %s", cmd, lastLineOf(tail, res, err), path)
		}
	}
	return nil
}

// linkInto links the primary's p into the tree. A target that is missing, or
// a path the tree has already, is a warning: the tree is usable without it.
// A link git would list as untracked is added to the repository's exclude
// file, so the tree stays clean for merge.
func linkInto(env Env, primary, tree, p string) error {
	target := filepath.Join(primary, filepath.FromSlash(p))
	link := filepath.Join(tree, filepath.FromSlash(p))
	if _, err := os.Stat(target); err != nil {
		env.warn("%s is not in the primary checkout: not linked", p)
		return nil
	}
	if _, err := os.Lstat(link); err == nil {
		env.warn("%s is in the tree already: not linked", p)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	if err := makeLink(env, target, link); err != nil {
		return err
	}
	env.say("linked %s", p)
	rel := filepath.ToSlash(p)
	if _, err := env.runGit(tree, "check-ignore", "-q", "--", rel); err == nil {
		return nil // ignored already
	}
	exclude, err := env.runGit(tree, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(tree, exclude)
	}
	raw, _ := os.ReadFile(exclude)
	line := "/" + strings.TrimPrefix(rel, "/")
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == line {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	prefix := ""
	if len(raw) > 0 && !strings.HasSuffix(string(raw), "\n") {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + line + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
