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
	m.cursor = clamp(m.cursor, 0, len(m.flat)-1)
}

func clamp(v, lo, hi int) int {
	return max(lo, min(hi, v))
}

func (m model) Init() tea.Cmd { return nil }

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

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
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
	keys := [][2]string{
		{"↑/↓  j/k", "move"},
		{"←/→  h/l", "collapse / expand"},
		{"⏎", "toggle dir / open file"},
		{"?", "toggle this help"},
		{"q", "quit"},
	}
	keyW := 0
	for _, k := range keys {
		if w := utf8.RuneCountInString(k[0]); w > keyW {
			keyW = w
		}
	}

	var b strings.Builder
	rendered := 0
	write := func(s string) {
		b.WriteString(s + "\n")
		rendered++
	}
	write(ansiDim + "keys" + ansiReset)
	for _, k := range keys {
		pad := strings.Repeat(" ", keyW-utf8.RuneCountInString(k[0]))
		write("  " + k[0] + pad + "   " + ansiDim + k[1] + ansiReset)
	}
	write("")
	write(ansiDim + "vim server" + ansiReset)
	write("  " + m.server)

	gap := max(0, m.h-rendered-1)
	b.WriteString(strings.Repeat("\n", gap))
	hint := "press any key to close"
	pad := max(0, m.w-utf8.RuneCountInString(hint))
	b.WriteString(strings.Repeat(" ", pad) + ansiDim + hint + ansiReset)
	return b.String()
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
	m := model{root: root, server: server, vim: vim}
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
