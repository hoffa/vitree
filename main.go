// Package main is the vitree TUI binary.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// selectedStyle is the only styling vitree applies: reverse video on the
// highlighted row. No colors, no bold anywhere else.
var selectedStyle = lipgloss.NewStyle().Reverse(true)

type model struct {
	root        *node
	flat        []*node
	cursor      int
	scroll      int
	vim         string
	server      string
	syncing     bool
	activePath  string
	pendingPath string
	w, h        int
	rows        []string
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
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "r":
			m.refresh()
		case "up":
			if m.cursor > 0 {
				m.cursor--
				m.ensureVisible()

				return m, m.syncCurrent()
			}
		case "down":
			if m.cursor < len(m.flat)-1 {
				m.cursor++
				m.ensureVisible()

				return m, m.syncCurrent()
			}
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
				_ = cur.load()
				cur.expanded = true
			}

			m.rebuildFlat()
		}
	}

	return m, nil
}

func (m model) View() string {
	var b strings.Builder

	start := m.scroll
	end := min(start+m.maxRows(), len(m.flat))

	for i := start; i < end; i++ {
		line := m.rows[i]
		if i == m.cursor {
			line = selectedStyle.Width(m.w).Render(line)
		}

		if i > start {
			b.WriteByte('\n')
		}

		b.WriteString(line)
	}

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

func truncateRight(s string, maxWidth int) string {
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}

	return string(runes[:max(0, maxWidth)])
}

func (m *model) rebuildFlat() {
	m.flat = m.root.flatten(m.flat[:0])
	m.cursor = clamp(m.cursor, 0, len(m.flat)-1)
	m.ensureVisible()
	m.renderRows()
}

func (m *model) maxRows() int {
	if m.h <= 0 {
		return len(m.flat)
	}

	return m.h
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
	root, err := buildTree(m.root.path, m.root.expandedPaths())
	if err != nil {
		return
	}

	var cursorPath string
	if cur := m.current(); cur != nil {
		cursorPath = cur.path
	}

	m.root = root

	m.rebuildFlat()
	m.restoreCursor(cursorPath)
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

	root, err := buildTree(abs, nil)
	if err != nil {
		return model{}, err
	}

	m := model{root: root, server: server, vim: vim}
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
