// Package main is the vitree TUI binary.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// selectedStyle is the only styling vitree applies: reverse video on the
// highlighted row. No colors, no bold anywhere else.
var selectedStyle = lipgloss.NewStyle().Reverse(true)

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
	err         string
	help        bool
	syncing     bool
	activePath  string
	pendingPath string
	w, h        int
	rows        []string
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

func (n *node) load() error {
	if !n.isDir || n.loaded {
		return nil
	}

	entries, err := os.ReadDir(n.path)
	if err != nil {
		return err
	}

	n.children = childrenFrom(n, entries)
	n.loaded = true

	return nil
}

func childrenFrom(parent *node, entries []os.DirEntry) []*node {
	sort.Slice(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di
		}

		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	var out []*node

	for _, e := range entries {
		abs := filepath.Join(parent.path, e.Name())

		c, err := newNode(abs, parent.depth+1, parent)
		if err != nil {
			continue
		}

		out = append(out, c)
	}

	return out
}

func clamp(v, lo, hi int) int {
	return max(lo, min(hi, v))
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.renderRows()
	case vimSyncDoneMsg:
		return m, m.finishSync(msg)
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

			if err := cur.load(); err != nil {
				m.err = "error: " + err.Error()
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
				if err := cur.load(); err != nil {
					m.err = "error: " + err.Error()
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
		line := m.rows[i]
		if i == m.cursor {
			line = selectedStyle.Width(m.w).Render(line)
		}

		b.WriteString(line + "\n")

		rendered++
	}

	gap := max(0, m.h-rendered-1)
	b.WriteString(strings.Repeat("\n", gap))

	left := m.root.path + "/"
	if m.err != "" {
		left = m.err
	}

	right := "? help"
	rightWidth := lipgloss.Width(right)

	left = truncateLeft(left, max(0, m.w-rightWidth-1))
	pad := max(1, m.w-lipgloss.Width(left)-rightWidth)
	b.WriteString(left + strings.Repeat(" ", pad) + right)

	return b.String()
}

// renderRows rebuilds the m.rows display cache from m.flat. It is the only
// place row text is built/truncated, so it must be called whenever the tree or
// terminal width changes. View() then reads the cache and only applies the
// cursor overlay, keeping a held cursor move O(1).
func (m *model) renderRows() {
	m.rows = make([]string, len(m.flat))

	for i, n := range m.flat {
		indent := strings.Repeat("  ", n.depth-1)

		marker := "  "
		suffix := ""

		if n.isDir {
			if n.expanded {
				marker = "- "
			} else {
				marker = "+ "
			}

			suffix = "/"
		}

		row := indent + marker + n.name + suffix

		if m.w > 0 && lipgloss.Width(row) > m.w {
			row = truncateRight(row, m.w)
		}

		m.rows[i] = row
	}
}

func truncateLeft(s string, maxWidth int) string {
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}

	if maxWidth <= 0 {
		return ""
	}

	return string(runes[len(runes)-maxWidth:])
}

func truncateRight(s string, maxWidth int) string {
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}

	return string(runes[:max(0, maxWidth)])
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
	m.renderRows()
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

// requestSync forwards path to vim, but only one vim exec runs at a time. While
// one is in flight, the latest requested path is remembered and fired once the
// running one completes, so flying through files coalesces to the last.
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
			m.err = "error: " + msg.err.Error()
		} else {
			m.err = ""
		}
	}

	pending := m.pendingPath
	m.pendingPath = ""

	if pending == "" || pending == msg.path {
		return nil
	}

	return m.requestSync(pending)
}

// refresh re-reads the tree from disk, preserving expansion and the cursor's
// file. It is synchronous and bound to `r`; there is no background refresh.
func (m *model) refresh() {
	expanded := m.expandedPaths()

	root, err := buildTree(m.root.path, expanded)
	if err != nil {
		m.err = "error: " + err.Error()
		return
	}

	var cursorPath string
	if cur := m.current(); cur != nil {
		cursorPath = cur.path
	}

	m.root = root
	m.err = ""

	m.rebuildFlat()
	m.restoreCursor(cursorPath)
}

func (m model) expandedPaths() map[string]bool {
	expanded := map[string]bool{}

	m.root.walk(func(n *node) {
		if n.isDir && n.expanded {
			expanded[n.path] = true
		}
	})

	return expanded
}

// buildTree reads the directory tree at rootPath plus every directory in
// expanded, marking those expanded.
func buildTree(rootPath string, expanded map[string]bool) (*node, error) {
	root, err := newNode(rootPath, 0, nil)
	if err != nil {
		return nil, err
	}

	rootEntries, err := os.ReadDir(rootPath)
	if err != nil {
		return nil, err
	}

	scanned := map[string][]os.DirEntry{rootPath: rootEntries}

	for path := range expanded {
		if path == rootPath {
			continue
		}

		entries, err := os.ReadDir(path)
		if err != nil {
			continue
		}

		scanned[path] = entries
	}

	var build func(*node)

	build = func(n *node) {
		entries, ok := scanned[n.path]
		if !ok {
			return
		}

		n.children = childrenFrom(n, entries)
		n.loaded = true

		for _, c := range n.children {
			if !c.isDir {
				continue
			}

			if expanded[c.path] {
				c.expanded = true
			}

			if _, ok := scanned[c.path]; ok {
				build(c)
			}
		}
	}

	root.expanded = true
	build(root)

	return root, nil
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

func (m model) helpView() string {
	body := `keys
  up down       j k     move
  left right    h l     collapse  expand
  enter                 toggle dir  open file
  r                     refresh
  ?                     toggle this help
  q                     quit

vim server
  ` + m.server
	hint := "press any key to close"
	gap := max(0, m.h-strings.Count(body, "\n")-2)
	pad := max(0, m.w-lipgloss.Width(hint))

	return body + "\n" + strings.Repeat("\n", gap) + strings.Repeat(" ", pad) + hint
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

	m := model{root: root, server: server, vim: vim}
	if err := root.load(); err != nil {
		return model{}, err
	}

	root.expanded = true

	m.rebuildFlat()

	return m, nil
}

var runProgram = func(m tea.Model) error {
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

var version = "dev"

func run(args []string) error {
	fs := flag.NewFlagSet("vitree", flag.ContinueOnError)
	server := fs.String("server", "", "vim --servername to send files to (auto-detected if empty)")

	vim := fs.String("vim", "vim", "vim binary to invoke (e.g. mvim, gvim, /path/to/vim)")
	showVersion := fs.Bool("version", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *showVersion {
		_, err := fmt.Fprintln(os.Stdout, version)
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
