// Package main is the vitree TUI binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	ansiReset    = "\x1b[0m"
	ansiSelected = "\x1b[7m"
	ansiDim      = "\x1b[2m"
	ansiError    = "\x1b[31m"
	ansiRed      = "\x1b[31m"
	ansiGreen    = "\x1b[32m"
	ansiYellow   = "\x1b[33m"
	ansiBlue     = "\x1b[34m"
)

func gitColor(mark string) string {
	switch mark {
	case "?", "A":
		return ansiGreen
	case "M", "R":
		return ansiYellow
	case "D", "U":
		return ansiRed
	}

	return ansiDim
}

type node struct {
	path     string
	name     string
	isDir    bool
	expanded bool
	loaded   bool
	depth    int
	children []*node
	parent   *node
}

type model struct {
	root        *node
	flat        []*node
	cursor      int
	scroll      int
	vim         string
	server      string
	msg         string
	help        bool
	hideIgnored bool
	autoRefresh bool
	syncing     bool
	activePath  string
	pendingPath string
	gitStatus   map[string]string
	w, h        int
}

type vimSyncDoneMsg struct {
	path string
	err  error
}

type autoRefreshTickMsg struct{}

const autoRefreshInterval = 2 * time.Second

func autoRefreshTick() tea.Cmd {
	return tea.Tick(autoRefreshInterval, func(time.Time) tea.Msg {
		return autoRefreshTickMsg{}
	})
}

func newNode(path string, depth int, parent *node) (*node, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}

	return &node{
		path:   path,
		name:   filepath.Base(path),
		isDir:  info.IsDir(),
		depth:  depth,
		parent: parent,
	}, nil
}

func (n *node) walk(fn func(*node)) {
	fn(n)

	for _, c := range n.children {
		c.walk(fn)
	}
}

func (n *node) load(hideIgnored bool) error {
	if !n.isDir || n.loaded {
		return nil
	}

	entries, err := os.ReadDir(n.path)
	if err != nil {
		return err
	}

	ignored := gitIgnoredEntries(n.path, entries, hideIgnored)

	sort.Slice(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di
		}

		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	for _, e := range entries {
		if ignored[e.Name()] {
			continue
		}

		c, err := newNode(filepath.Join(n.path, e.Name()), n.depth+1, n)
		if err != nil {
			continue
		}

		n.children = append(n.children, c)
	}

	n.loaded = true

	return nil
}

func gitIgnoredEntries(dir string, entries []os.DirEntry, enabled bool) map[string]bool {
	ignored := map[string]bool{}
	if !enabled || len(entries) == 0 {
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

	for _, e := range entries {
		_, _ = stdin.Write([]byte(e.Name()))
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

func clamp(v, lo, hi int) int {
	return max(lo, min(hi, v))
}

func syncVimCmd(vim, server, path string) tea.Cmd {
	return func() tea.Msg {
		return vimSyncDoneMsg{path: path, err: openInVim(vim, server, path)}
	}
}

func (m model) Init() tea.Cmd {
	if m.autoRefresh {
		return autoRefreshTick()
	}

	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case vimSyncDoneMsg:
		return m, m.finishSync(msg)
	case autoRefreshTickMsg:
		if !m.autoRefresh {
			return m, nil
		}

		savedMsg, savedPending := m.msg, m.pendingPath
		m.refreshWithMessage("")
		m.msg, m.pendingPath = savedMsg, savedPending

		return m, autoRefreshTick()
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		if m.help {
			m.help = false
			if msg.String() == "ctrl+c" {
				return m, tea.Quit
			}

			return m, nil
		}

		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "?":
			m.help = true
		case "i":
			m.hideIgnored = !m.hideIgnored
			m.refreshWithMessage("")
		case "a":
			m.autoRefresh = !m.autoRefresh
			if m.autoRefresh {
				return m, autoRefreshTick()
			}
		case "r":
			m.refresh()
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.ensureVisible()

				return m, m.syncCurrent()
			}
		case "down", "j":
			if m.cursor < len(m.flat)-1 {
				m.cursor++
				m.ensureVisible()

				return m, m.syncCurrent()
			}
		case "g":
			m.cursor = 0
			m.ensureVisible()

			return m, m.syncCurrent()
		case "G":
			m.cursor = max(0, len(m.flat)-1)
			m.ensureVisible()

			return m, m.syncCurrent()
		case "left", "h":
			cur := m.current()
			if cur == nil {
				break
			}

			if cur.isDir && cur.expanded {
				cur.expanded = false

				m.rebuildFlat()
				m.pendingPath = ""
			} else if cur.parent != nil && cur.parent != m.root {
				for i, n := range m.flat {
					if n == cur.parent {
						m.cursor = i
						break
					}
				}

				m.ensureVisible()
				m.pendingPath = ""
			}
		case "right", "l":
			cur := m.current()
			if cur == nil || !cur.isDir {
				break
			}

			if err := cur.load(m.hideIgnored); err != nil {
				m.msg = "error: " + err.Error()
			}

			cur.expanded = true

			m.rebuildFlat()
		case "enter":
			cur := m.current()
			if cur == nil {
				break
			}

			if !cur.isDir {
				return m, m.syncCurrent()
			}

			if cur.expanded {
				cur.expanded = false
			} else {
				if err := cur.load(m.hideIgnored); err != nil {
					m.msg = "error: " + err.Error()
				}

				cur.expanded = true
			}

			m.rebuildFlat()
		}
	}

	return m, nil
}

func (m model) View() string {
	if m.help {
		return m.helpView()
	}

	var b strings.Builder

	start := m.scroll
	end := min(start+m.maxRows(), len(m.flat))

	rendered := 0

	for i := start; i < end; i++ {
		n := m.flat[i]
		indent := strings.Repeat("  ", n.depth-1)

		var marker, suffix string

		if n.isDir {
			if n.expanded {
				marker = "- "
			} else {
				marker = "+ "
			}

			suffix = "/"
		} else {
			marker = "  "
		}

		raw := indent + marker + n.name + suffix

		gitMark := m.gitStatus[n.path]
		if gitMark != "" {
			raw += " " + gitMark
		}

		var line string

		if i == m.cursor {
			pad := max(0, m.w-utf8.RuneCountInString(raw))
			line = ansiSelected + raw + strings.Repeat(" ", pad) + ansiReset
		} else {
			styled := indent
			if n.isDir {
				styled += ansiDim + marker + ansiReset + ansiBlue + n.name + suffix + ansiReset
			} else {
				styled += marker + n.name + suffix
			}

			if gitMark != "" {
				styled += " " + gitColor(gitMark) + gitMark + ansiReset
			}

			line = styled
		}

		b.WriteString(line + "\n")

		rendered++
	}

	gap := max(0, m.h-rendered-1)
	b.WriteString(strings.Repeat("\n", gap))

	leftRaw := m.root.path + "/"
	color := ansiDim

	if m.msg != "" {
		leftRaw = m.msg
		if strings.HasPrefix(m.msg, "error:") {
			color = ansiError
		}
	}

	right := "? help"
	if !m.hideIgnored {
		right = "gitignore off · " + right
	}

	if !m.autoRefresh {
		right = "auto off · " + right
	}

	rightStyled := ansiDim + right + ansiReset
	rightWidth := utf8.RuneCountInString(right)

	leftRaw = truncateLeft(leftRaw, max(0, m.w-rightWidth-1))
	leftStyled := color + leftRaw + ansiReset
	pad := max(1, m.w-utf8.RuneCountInString(leftRaw)-rightWidth)
	b.WriteString(leftStyled + strings.Repeat(" ", pad) + rightStyled)

	return b.String()
}

func truncateLeft(s string, maxWidth int) string {
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}

	if maxWidth < 1 {
		return ""
	}

	return "…" + string(runes[len(runes)-maxWidth+1:])
}

func (m *model) rebuildFlat() {
	m.flat = m.flat[:0]

	var walk func(n *node)

	walk = func(n *node) {
		m.flat = append(m.flat, n)
		if n.isDir && n.expanded {
			for _, c := range n.children {
				walk(c)
			}
		}
	}
	for _, c := range m.root.children {
		walk(c)
	}

	m.cursor = clamp(m.cursor, 0, len(m.flat)-1)
	m.ensureVisible()
}

func (m *model) maxRows() int {
	r := m.h - 1
	if r <= 0 {
		return len(m.flat)
	}

	return r
}

func (m *model) ensureVisible() {
	mr := m.maxRows()
	if mr <= 0 {
		m.scroll = 0
		return
	}

	if m.cursor < m.scroll {
		m.scroll = m.cursor
	} else if m.cursor >= m.scroll+mr {
		m.scroll = m.cursor - mr + 1
	}

	m.scroll = clamp(m.scroll, 0, max(0, len(m.flat)-mr))
}

func (m *model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.help {
		m.help = false
		return *m, nil
	}

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if m.cursor > 0 {
			m.cursor--
			m.ensureVisible()

			return *m, m.syncCurrent()
		}
	case tea.MouseButtonWheelDown:
		if m.cursor < len(m.flat)-1 {
			m.cursor++
			m.ensureVisible()

			return *m, m.syncCurrent()
		}
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return *m, nil
		}

		if msg.Y < 0 || msg.Y >= m.maxRows() {
			return *m, nil
		}

		idx := m.scroll + msg.Y
		if idx < 0 || idx >= len(m.flat) {
			return *m, nil
		}

		m.cursor = idx

		cur := m.flat[idx]
		if cur.isDir {
			if cur.expanded {
				cur.expanded = false
			} else {
				if err := cur.load(m.hideIgnored); err != nil {
					m.msg = "error: " + err.Error()
				}

				cur.expanded = true
			}

			m.rebuildFlat()

			return *m, nil
		}

		return *m, m.syncCurrent()
	}

	return *m, nil
}

func (m *model) current() *node {
	if m.cursor < 0 || m.cursor >= len(m.flat) {
		return nil
	}

	return m.flat[m.cursor]
}

func (m *model) syncCurrent() tea.Cmd {
	cur := m.current()
	if cur == nil || cur.isDir {
		m.pendingPath = ""
		return nil
	}

	return m.requestSync(cur.path)
}

func (m *model) requestSync(path string) tea.Cmd {
	if m.syncing {
		if path == m.activePath {
			m.pendingPath = ""
		} else {
			m.pendingPath = path
		}

		return nil
	}

	m.syncing = true
	m.activePath = path

	return syncVimCmd(m.vim, m.server, path)
}

func (m *model) finishSync(msg vimSyncDoneMsg) tea.Cmd {
	m.syncing = false
	m.activePath = ""

	if cur := m.current(); cur != nil && cur.path == msg.path {
		if msg.err != nil {
			m.msg = "error: " + msg.err.Error()
		} else {
			m.msg = ""
		}
	}

	pending := m.pendingPath
	m.pendingPath = ""

	if pending == "" || pending == msg.path {
		return nil
	}

	return m.requestSync(pending)
}

func (m *model) refresh() {
	m.refreshWithMessage("refreshed")
}

func (m *model) refreshWithMessage(success string) {
	expanded := map[string]bool{}

	m.root.walk(func(n *node) {
		if n.isDir && n.expanded {
			expanded[n.path] = true
		}
	})

	var cursorPath string
	if cur := m.current(); cur != nil {
		cursorPath = cur.path
	}

	m.root.children = nil
	m.root.loaded = false
	m.pendingPath = ""

	if err := m.root.load(m.hideIgnored); err != nil {
		m.msg = "error: " + err.Error()
		return
	}

	m.root.walk(func(n *node) {
		if n.isDir && expanded[n.path] {
			_ = n.load(m.hideIgnored)
			n.expanded = true
		}
	})
	m.rebuildFlat()
	m.restoreCursor(cursorPath)
	m.loadGitStatus()
	m.msg = success
}

func (m *model) restoreCursor(path string) {
	if path == "" {
		return
	}

	for i, n := range m.flat {
		if n.path == path {
			m.cursor = i
			m.ensureVisible()

			return
		}
	}

	m.cursor = min(m.cursor, max(0, len(m.flat)-1))
	m.ensureVisible()
}

func (m *model) loadGitStatus() {
	m.gitStatus = gitStatus(m.root.path)
}

func gitStatus(dir string) map[string]string {
	statuses := map[string]string{}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	root, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return statuses
	}

	repoRoot := strings.TrimSpace(string(root))

	out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "status", "--porcelain", "--untracked-files=all").Output()
	if err != nil {
		return statuses
	}

	priority := map[string]int{"?": 0, "R": 1, "D": 2, "A": 3, "M": 4}
	bumpDir := func(p, mark string) {
		if cur, ok := statuses[p]; !ok || priority[mark] > priority[cur] {
			statuses[p] = mark
		}
	}

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

		path := filepath.Join(repoRoot, rel)
		statuses[path] = short

		for parent := filepath.Dir(path); parent != repoRoot && parent != "/" && parent != "."; parent = filepath.Dir(parent) {
			bumpDir(parent, short)
		}
	}

	return statuses
}

func (m model) helpView() string {
	body := `keys
  up/down    j/k     move
  left/right h/l     collapse / expand
  g / G              jump to top / bottom
  enter              toggle dir / open file
  i                  toggle gitignore hiding
  a                  toggle auto-refresh (2s, on by default)
  r                  refresh tree from disk
  ?                  toggle this help
  q                  quit

vim server
  ` + m.server
	hint := "press any key to close"
	gap := max(0, m.h-strings.Count(body, "\n")-2)
	pad := max(0, m.w-utf8.RuneCountInString(hint))

	return body + "\n" + strings.Repeat("\n", gap) + strings.Repeat(" ", pad) + ansiDim + hint + ansiReset
}

func detectVimServer(vim string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, vim, "--serverlist").Output()
	if err != nil {
		return "", fmt.Errorf("could not run %s --serverlist: %w", vim, err)
	}

	var servers []string

	for line := range strings.SplitSeq(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			servers = append(servers, line)
		}
	}

	switch len(servers) {
	case 0:
		return "", errors.New("no vim server running — start vim with --servername first")
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

func newModel(vim, server, path string) (model, error) {
	if server == "" {
		detected, err := detectVimServer(vim)
		if err != nil {
			return model{}, err
		}

		server = detected
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return model{}, err
	}

	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}

	root, err := newNode(abs, 0, nil)
	if err != nil {
		return model{}, err
	}

	if !root.isDir {
		return model{}, errors.New("root must be a directory")
	}

	m := model{root: root, server: server, vim: vim, hideIgnored: true, autoRefresh: true}
	if err := root.load(m.hideIgnored); err != nil {
		return model{}, err
	}

	root.expanded = true

	m.rebuildFlat()
	m.loadGitStatus()

	return m, nil
}

var runProgram = func(m tea.Model) error {
	_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

func run(args []string) error {
	fs := flag.NewFlagSet("vitree", flag.ContinueOnError)
	server := fs.String("server", "", "vim --servername to send files to (auto-detected if empty)")

	vim := fs.String("vim", "vim", "vim binary to invoke (e.g. mvim, gvim, /path/to/vim)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	m, err := newModel(*vim, *server, ".")
	if err != nil {
		return err
	}

	return runProgram(m)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
