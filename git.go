package main

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// gitIgnored returns the subset of paths that git ignores, evaluated from dir.
// It is best-effort: if dir is not a git repo, git is missing, or anything
// fails, it returns an empty set so nothing is dimmed. One batched
// `git check-ignore` call covers the whole tree.
func gitIgnored(dir string, paths []string) map[string]bool {
	ignored := map[string]bool{}
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
