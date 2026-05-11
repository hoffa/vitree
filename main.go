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
	changedOnly bool
	refreshing  bool
	syncing     bool
	activePath  string
	pendingPath string
	gitStatus   map[string]string
	w, h        int
}

type autoRefreshTickMsg struct{}

type refreshResultMsg struct {
	root      *node
	gitStatus map[string]string
	err       error
}

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

		if hideIgnored && e.Name() == ".git" && e.IsDir() {
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

func clamp(v, lo, hi int) int {
	return max(lo, min(hi, v))
}

func (m model) Init() tea.Cmd {
	return autoRefreshTick()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case vimSyncDoneMsg:
		return m, m.finishSync(msg)
	case autoRefreshTickMsg:
		if m.refreshing {
			return m, autoRefreshTick()
		}

		m.refreshing = true

		return m, tea.Batch(m.asyncRefreshCmd(), autoRefreshTick())
	case refreshResultMsg:
		m.applyRefresh(msg)

		return m, nil
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
		case "c":
			m.changedOnly = !m.changedOnly
			m.rebuildFlat()
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

	if m.changedOnly {
		right = "changed only · " + right
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
		if m.changedOnly && m.gitStatus[n.path] == "" {
			return
		}

		m.flat = append(m.flat, n)
		if n.isDir && (n.expanded || m.changedOnly) {
			if m.changedOnly && !n.loaded {
				_ = n.load(m.hideIgnored)
			}

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

func (m model) asyncRefreshCmd() tea.Cmd {
	rootPath := m.root.path
	hideIgnored := m.hideIgnored

	expanded := map[string]bool{}

	m.root.walk(func(n *node) {
		if n.isDir && n.expanded {
			expanded[n.path] = true
		}
	})

	return func() tea.Msg {
		root, err := newNode(rootPath, 0, nil)
		if err != nil {
			return refreshResultMsg{err: err}
		}

		if err := root.load(hideIgnored); err != nil {
			return refreshResultMsg{err: err}
		}

		root.expanded = true

		root.walk(func(n *node) {
			if n.isDir && expanded[n.path] {
				_ = n.load(hideIgnored)
				n.expanded = true
			}
		})

		return refreshResultMsg{root: root, gitStatus: gitStatus(rootPath)}
	}
}

func (m *model) applyRefresh(msg refreshResultMsg) {
	m.refreshing = false

	if msg.err != nil {
		m.msg = "error: " + msg.err.Error()
		return
	}

	nowExpanded := map[string]bool{}

	m.root.walk(func(n *node) {
		if n.isDir && n.expanded {
			nowExpanded[n.path] = true
		}
	})

	msg.root.walk(func(n *node) {
		if n.isDir && nowExpanded[n.path] && !n.expanded {
			_ = n.load(m.hideIgnored)
			n.expanded = true
		}
	})

	var cursorPath string
	if cur := m.current(); cur != nil {
		cursorPath = cur.path
	}

	m.root = msg.root
	m.gitStatus = msg.gitStatus

	m.rebuildFlat()
	m.restoreCursor(cursorPath)
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

func (m model) helpView() string {
	body := `keys
  up/down    j/k     move
  left/right h/l     collapse / expand
  g / G              jump to top / bottom
  enter              toggle dir / open file
  i                  toggle gitignore hiding
  c                  toggle changed-only (git status)
  ?                  toggle this help
  q                  quit

vim server
  ` + m.server
	hint := "press any key to close"
	gap := max(0, m.h-strings.Count(body, "\n")-2)
	pad := max(0, m.w-utf8.RuneCountInString(hint))

	return body + "\n" + strings.Repeat("\n", gap) + strings.Repeat(" ", pad) + ansiDim + hint + ansiReset
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

	m := model{root: root, server: server, vim: vim, hideIgnored: true}
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
