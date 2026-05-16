package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// underGitDir reports whether path has a .git component, i.e. it is the git
// directory or anything inside it.
func underGitDir(path string) bool {
	return slices.Contains(strings.Split(path, string(filepath.Separator)), ".git")
}

// gitIgnored returns the subset of paths git ignores, evaluated from dir. It is
// the single source of truth for "what git ignores": `git check-ignore` for
// .gitignore rules (it already reports descendants of an ignored dir), plus
// .git itself and its contents, which git excludes internally and
// check-ignore never reports. Best-effort: a non-repo, missing git, or any
// failure just means only the .git entries come back.
func gitIgnored(dir string, paths []string) map[string]bool {
	ignored := map[string]bool{}

	for _, p := range paths {
		if underGitDir(p) {
			ignored[p] = true
		}
	}

	if len(paths) == 0 {
		return ignored
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", dir, "check-ignore", "--stdin", "-z")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return ignored
	}

	var out strings.Builder

	cmd.Stdout = &out

	if err := cmd.Start(); err != nil {
		return ignored
	}

	for _, p := range paths {
		_, _ = stdin.Write([]byte(p))
		_, _ = stdin.Write([]byte{0})
	}

	_ = stdin.Close()

	if err := cmd.Wait(); err != nil && out.Len() == 0 {
		return ignored
	}

	for name := range strings.SplitSeq(out.String(), "\x00") {
		if name != "" {
			ignored[name] = true
		}
	}

	return ignored
}
