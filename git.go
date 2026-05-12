package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

type porcelainLine struct {
	mark string
	path string
}

func gitColor(mark string) lipgloss.Style {
	switch mark {
	case "?", "A":
		return greenStyle
	case "M", "R":
		return yellowStyle
	case "D", "U":
		return redStyle
	}

	return dimStyle
}

func gitRepoRoot(dir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}

func gitPorcelain(dir string) ([]porcelainLine, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain", "--untracked-files=all").Output()
	if err != nil {
		return nil, err
	}

	var lines []porcelainLine

	for line := range strings.SplitSeq(string(out), "\n") {
		if len(line) < 4 {
			continue
		}

		code, rel := line[:2], line[3:]
		mark := strings.TrimSpace(code)

		if mark == "" {
			continue
		}

		short := string(mark[len(mark)-1])

		if i := strings.Index(rel, " -> "); i >= 0 {
			rel = rel[i+len(" -> "):]
		}

		lines = append(lines, porcelainLine{mark: short, path: rel})
	}

	return lines, nil
}

func gitCheckIgnore(dir string, names []string) (map[string]bool, error) {
	ignored := map[string]bool{}
	if len(names) == 0 {
		return ignored, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", dir, "check-ignore", "--stdin", "-z")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return ignored, err
	}

	var out strings.Builder

	cmd.Stdout = &out

	if err := cmd.Start(); err != nil {
		return ignored, err
	}

	for _, n := range names {
		_, _ = stdin.Write([]byte(n))
		_, _ = stdin.Write([]byte{0})
	}

	_ = stdin.Close()

	if err := cmd.Wait(); err != nil && out.Len() == 0 {
		return ignored, err
	}

	for name := range strings.SplitSeq(out.String(), "\x00") {
		if name != "" {
			ignored[name] = true
		}
	}

	return ignored, nil
}

func gitIgnoredEntries(dir string, entries []os.DirEntry, enabled bool) map[string]bool {
	if !enabled {
		return map[string]bool{}
	}

	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}

	ignored, _ := gitCheckIgnore(dir, names)

	return ignored
}

func gitStatus(dir string) map[string]string {
	statuses := map[string]string{}

	repoRoot, err := gitRepoRoot(dir)
	if err != nil {
		return statuses
	}

	lines, err := gitPorcelain(repoRoot)
	if err != nil {
		return statuses
	}

	priority := map[string]int{"?": 0, "R": 1, "D": 2, "A": 3, "M": 4}
	bumpDir := func(p, mark string) {
		if cur, ok := statuses[p]; !ok || priority[mark] > priority[cur] {
			statuses[p] = mark
		}
	}

	for _, ln := range lines {
		path := filepath.Join(repoRoot, ln.path)
		statuses[path] = ln.mark

		for parent := filepath.Dir(path); parent != repoRoot && parent != "/" && parent != "."; parent = filepath.Dir(parent) {
			bumpDir(parent, ln.mark)
		}
	}

	return statuses
}
