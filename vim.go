package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type vimSyncDoneMsg struct {
	path string
	err  error
}

func syncVimCmd(vim, server, path string) tea.Cmd {
	return func() tea.Msg {
		return vimSyncDoneMsg{path: path, err: openInVim(vim, server, path)}
	}
}

func vimServers(vim string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, vim, "--serverlist").Output()
	if err != nil {
		return nil, fmt.Errorf("could not run %s --serverlist: %w", vim, err)
	}

	var servers []string

	for line := range strings.SplitSeq(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			servers = append(servers, line)
		}
	}

	return servers, nil
}

func detectVimServer(vim string) (string, error) {
	servers, err := vimServers(vim)
	if err != nil {
		return "", err
	}

	switch len(servers) {
	case 0:
		return "", errors.New("no vim server running - start vim with --servername first")
	case 1:
		return servers[0], nil
	default:
		return "", fmt.Errorf("multiple vim servers running (%s); pick one with -server", strings.Join(servers, ", "))
	}
}

func openInVim(vim, server, path string) error {
	if server == "" {
		return errors.New("no vim --servername set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, vim, "--servername", server, "--remote-silent", path)
	out, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("vim server %q not responding", server)
	}

	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}

	return nil
}
