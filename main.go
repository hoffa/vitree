package main

import (
	"context"
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
	"github.com/fsnotify/fsnotify"
)

const (
	ansiReset    = "\x1b[0m"
	ansiSelected = "\x1b[7m"
	ansiDir      = "\x1b[34m"
	ansiDim      = "\x1b[2m"
	ansiError    = "\x1b[31m"
	ansiClearFg  = "\x1b[22;39m"
)

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
	root   *node
	flat   []*node
	cursor int
	vim    string
	server string
	msg    string
	help   bool
	watch  bool
	w, h   int
}

type fsEventMsg struct{}

type fsErrorMsg struct{ err error }

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

func (n *node) load() error {
	if !n.isDir || n.loaded {
		return nil
	}
	return n.reload()
}

func (n *node) reload() error {
	if !n.isDir {
		return nil
	}
	entries, err := os.ReadDir(n.path)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	n.children = nil
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
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
}

func clamp(v, lo, hi int) int {
	return max(lo, min(hi, v))
}

func (m model) Init() tea.Cmd {
	if !m.watch || m.root == nil {
		return nil
	}
	return watchDir(m.root.path)
}

func (m *model) current() *node {
	if m.cursor < 0 || m.cursor >= len(m.flat) {
		return nil
	}
	return m.flat[m.cursor]
}

func (m *model) syncCurrent() {
	cur := m.current()
	if cur == nil || cur.isDir {
		return
	}
	if err := openInVim(m.vim, m.server, cur.path); err != nil {
		m.msg = "error: " + err.Error()
	} else {
		m.msg = ""
	}
}

func (m *model) refreshFromDisk() error {
	if m.root == nil {
		return nil
	}
	selected := ""
	if cur := m.current(); cur != nil {
		selected = cur.path
	}
	expanded := map[string]bool{}
	m.root.walk(func(n *node) {
		if n.isDir && n.expanded {
			expanded[n.path] = true
		}
	})
	if err := reloadExpanded(m.root, expanded); err != nil {
		return err
	}
	m.rebuildFlat()
	if selected != "" {
		for i, n := range m.flat {
			if n.path == selected {
				m.cursor = i
				return nil
			}
		}
	}
	return nil
}

func reloadExpanded(n *node, expanded map[string]bool) error {
	if !n.isDir {
		return nil
	}
	n.expanded = expanded[n.path]
	if !n.expanded {
		n.children = nil
		n.loaded = false
		return nil
	}
	if err := n.reload(); err != nil {
		return err
	}
	for _, c := range n.children {
		if expanded[c.path] {
			if err := reloadExpanded(c, expanded); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case fsEventMsg:
		if err := m.refreshFromDisk(); err != nil {
			m.msg = "error: " + err.Error()
		} else if strings.HasPrefix(m.msg, "error: watching files:") {
			m.msg = ""
		}
		if m.watch {
			return m, watchDir(m.root.path)
		}
	case fsErrorMsg:
		m.msg = "error: watching files: " + msg.err.Error()
		if m.watch {
			return m, watchDir(m.root.path)
		}
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
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.syncCurrent()
			}
		case "down", "j":
			if m.cursor < len(m.flat)-1 {
				m.cursor++
				m.syncCurrent()
			}
		case "left", "h":
			cur := m.current()
			if cur == nil {
				break
			}
			if cur.isDir && cur.expanded {
				cur.expanded = false
				cur.children = nil
				cur.loaded = false
				m.rebuildFlat()
			} else if cur.parent != nil && cur.parent != m.root {
				for i, n := range m.flat {
					if n == cur.parent {
						m.cursor = i
						break
					}
				}
			}
		case "right", "l":
			cur := m.current()
			if cur == nil || !cur.isDir {
				break
			}
			if err := cur.load(); err != nil {
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
				m.syncCurrent()
				break
			}
			if cur.expanded {
				cur.expanded = false
				cur.children = nil
				cur.loaded = false
			} else {
				if err := cur.load(); err != nil {
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
	maxRows := m.h - 1
	if maxRows <= 0 {
		maxRows = len(m.flat)
	}
	start := 0
	if m.cursor >= maxRows {
		start = m.cursor - maxRows + 1
	}
	end := start + maxRows
	if end > len(m.flat) {
		end = len(m.flat)
	}

	rendered := 0
	for i := start; i < end; i++ {
		n := m.flat[i]
		indent := strings.Repeat("  ", n.depth-1)
		var marker, suffix string
		if n.isDir {
			if n.expanded {
				marker = "▼ "
			} else {
				marker = "▶ "
			}
			suffix = "/"
		} else {
			marker = "  "
		}
		raw := indent + marker + n.name + suffix
		var styled string
		if n.isDir {
			styled = indent + marker + ansiDir + n.name + suffix + ansiClearFg
		} else {
			styled = raw
		}
		var line string
		if i == m.cursor {
			pad := max(0, m.w-utf8.RuneCountInString(raw))
			line = ansiSelected + raw + strings.Repeat(" ", pad) + ansiReset
		} else {
			line = styled + ansiReset
		}
		b.WriteString(line + "\n")
		rendered++
	}

	gap := max(0, m.h-rendered-1)
	b.WriteString(strings.Repeat("\n", gap))

	leftRaw := m.root.path + "/"
	leftStyled := ansiDim + leftRaw + ansiReset
	if m.msg != "" {
		leftRaw = m.msg
		color := ansiDim
		if strings.HasPrefix(m.msg, "error:") {
			color = ansiError
		}
		leftStyled = color + m.msg + ansiReset
	}
	right := "? help"
	rightStyled := ansiDim + right + ansiReset
	pad := m.w - utf8.RuneCountInString(leftRaw) - utf8.RuneCountInString(right)
	if pad < 1 {
		b.WriteString(leftStyled)
	} else {
		b.WriteString(leftStyled + strings.Repeat(" ", pad) + rightStyled)
	}
	return b.String()
}

func (m model) helpView() string {
	body := fmt.Sprintf(`keys
  ↑/↓  j/k   `+ansiDim+`move`+ansiReset+`
  ←/→  h/l   `+ansiDim+`collapse / expand`+ansiReset+`
  ⏎          `+ansiDim+`toggle dir / open file`+ansiReset+`
  ?          `+ansiDim+`toggle this help`+ansiReset+`
  q          `+ansiDim+`quit`+ansiReset+`

vim server
  %s`, m.server)
	hint := "press any key to close"
	gap := max(0, m.h-strings.Count(body, "\n")-2)
	pad := max(0, m.w-utf8.RuneCountInString(hint))
	return body + "\n" + strings.Repeat("\n", gap) + strings.Repeat(" ", pad) + ansiDim + hint + ansiReset
}

func detectVimServer(vim string) (string, error) {
	out, err := exec.Command(vim, "--serverlist").Output()
	if err != nil {
		return "", fmt.Errorf("could not run %s --serverlist: %w", vim, err)
	}
	var servers []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			servers = append(servers, line)
		}
	}
	switch len(servers) {
	case 0:
		return "", fmt.Errorf("no vim server running — start vim with --servername first")
	case 1:
		return servers[0], nil
	default:
		return "", fmt.Errorf("multiple vim servers running (%s); pick one with -server", strings.Join(servers, ", "))
	}
}

func openInVim(vim, server, path string) error {
	if server == "" {
		return fmt.Errorf("no vim --servername set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, vim, "--servername", server, "--remote-silent", path)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("vim server %q not responding", server)
	}
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func watchDir(path string) tea.Cmd {
	return func() tea.Msg {
		w, err := fsnotify.NewWatcher()
		if err != nil {
			return fsErrorMsg{err: err}
		}
		defer w.Close()

		if err := addRecursiveWatches(w, path); err != nil {
			return fsErrorMsg{err: err}
		}

		return waitForChange(w.Events, w.Errors)
	}
}

func waitForChange(events <-chan fsnotify.Event, errors <-chan error) tea.Msg {
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return fsErrorMsg{err: fmt.Errorf("watcher closed")}
			}
			if shouldRefresh(event) {
				return fsEventMsg{}
			}
		case err, ok := <-errors:
			if !ok {
				return fsErrorMsg{err: fmt.Errorf("watcher closed")}
			}
			return fsErrorMsg{err: err}
		}
	}
}

func addRecursiveWatches(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		return w.Add(path)
	})
}

func shouldRefresh(event fsnotify.Event) bool {
	if strings.HasPrefix(filepath.Base(event.Name), ".") {
		return false
	}
	return event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) || event.Has(fsnotify.Write)
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
	root, err := newNode(abs, 0, nil)
	if err != nil {
		return model{}, err
	}
	if !root.isDir {
		return model{}, fmt.Errorf("root must be a directory")
	}
	if err := root.load(); err != nil {
		return model{}, err
	}
	root.expanded = true
	m := model{root: root, server: server, vim: vim, watch: true}
	m.rebuildFlat()
	return m, nil
}

var runProgram = func(m tea.Model) error {
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
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
