package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	ansiReset    = "\x1b[0m"
	ansiSelected = "\x1b[7m"
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
	w, h   int
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

func (n *node) load() error {
	if !n.isDir || n.loaded {
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
	if m.cursor >= len(m.flat) {
		m.cursor = len(m.flat) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m model) Init() tea.Cmd { return nil }

func (m *model) syncCurrent() {
	if m.cursor >= len(m.flat) {
		return
	}
	cur := m.flat[m.cursor]
	if cur.isDir {
		return
	}
	if err := openInVim(m.vim, m.server, cur.path); err != nil {
		m.msg = "vim error: " + err.Error()
	} else {
		m.msg = "opened " + cur.path
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "r":
			expanded := map[string]bool{}
			var walk func(n *node)
			walk = func(n *node) {
				if n.isDir && n.expanded {
					expanded[n.path] = true
				}
				for _, c := range n.children {
					walk(c)
				}
			}
			walk(m.root)
			m.root.children = nil
			m.root.loaded = false
			if err := m.root.load(); err != nil {
				m.msg = err.Error()
			}
			var rewalk func(n *node)
			rewalk = func(n *node) {
				if n.isDir && expanded[n.path] {
					_ = n.load()
					n.expanded = true
					for _, c := range n.children {
						rewalk(c)
					}
				}
			}
			for _, c := range m.root.children {
				rewalk(c)
			}
			m.rebuildFlat()
			m.msg = "refreshed"
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
			if m.cursor < len(m.flat) {
				cur := m.flat[m.cursor]
				if cur.isDir && cur.expanded {
					cur.expanded = false
					m.rebuildFlat()
				} else if cur.parent != nil && cur.parent != m.root {
					for i, n := range m.flat {
						if n == cur.parent {
							m.cursor = i
							break
						}
					}
				}
			}
		case "right", "l":
			if m.cursor < len(m.flat) {
				cur := m.flat[m.cursor]
				if cur.isDir {
					if err := cur.load(); err != nil {
						m.msg = err.Error()
					}
					cur.expanded = true
					m.rebuildFlat()
				}
			}
		case "enter":
			if m.cursor < len(m.flat) {
				cur := m.flat[m.cursor]
				if cur.isDir {
					if err := cur.load(); err != nil {
						m.msg = err.Error()
					}
					cur.expanded = !cur.expanded
					m.rebuildFlat()
				} else {
					m.syncCurrent()
				}
			}
		}
	}
	return m, nil
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(m.root.path + "/\n")

	maxRows := m.h - 4
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

	rendered := 1 // root path line
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
		var line string
		if i == m.cursor {
			pad := m.w - utf8.RuneCountInString(raw)
			if pad < 0 {
				pad = 0
			}
			line = ansiSelected + raw + strings.Repeat(" ", pad) + ansiReset
		} else {
			line = raw
		}
		b.WriteString(line + "\n")
		rendered++
	}

	footerLines := 1
	if m.msg != "" {
		footerLines = 2
	}
	gap := m.h - rendered - footerLines
	if gap < 1 {
		gap = 1
	}
	b.WriteString(strings.Repeat("\n", gap))
	b.WriteString("↑/↓ move  ←/→ collapse/expand  ⏎ open  r refresh  q quit")
	if m.msg != "" {
		b.WriteString("\n" + m.msg)
	}
	return b.String()
}

func vimServerRunning(vim, server string) bool {
	out, err := exec.Command(vim, "--serverlist").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.EqualFold(strings.TrimSpace(line), server) {
			return true
		}
	}
	return false
}

func openInVim(vim, server, path string) error {
	if server == "" {
		return fmt.Errorf("no vim --servername set (export VIM_SERVERNAME or pass -server)")
	}
	if !vimServerRunning(vim, server) {
		return fmt.Errorf("no running %s server %q", vim, server)
	}
	cmd := exec.Command(vim, "--servername", server, "--remote-silent", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func main() {
	server := flag.String("server", os.Getenv("VIM_SERVERNAME"), "vim --servername to send files to (default $VIM_SERVERNAME)")
	vim := flag.String("vim", "vim", "vim binary to invoke (e.g. mvim, gvim, /path/to/vim)")
	flag.Parse()

	abs, err := filepath.Abs(".")
	if err != nil {
		log.Fatal(err)
	}
	root, err := newNode(abs, 0, nil)
	if err != nil {
		log.Fatal(err)
	}
	if !root.isDir {
		log.Fatal("root must be a directory")
	}
	if err := root.load(); err != nil {
		log.Fatal(err)
	}
	root.expanded = true

	m := model{root: root, server: *server, vim: *vim}
	m.rebuildFlat()

	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		log.Fatal(err)
	}
}
