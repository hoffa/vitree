// Package main is the vitree TUI binary.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/mattn/go-runewidth"
)

type model struct {
	root   *node
	flat   []*node
	cursor int
	scroll int
	vim    string
	server string
	w, h   int
	rows   []string
}

func clamp(v, lo, hi int) int {
	return max(lo, min(hi, v))
}

// onKey applies a keystroke. It reports whether to quit and, when enter opens a
// file, the path to forward to vim. Cursor movement never touches vim — only
// enter does — so holding up/down is pure navigation.
func (m *model) onKey(ev *tcell.EventKey) (bool, string, bool) {
	switch ev.Key() {
	case tcell.KeyCtrlC:
		return true, "", false
	case tcell.KeyUp:
		if m.cursor > 0 {
			m.cursor--
			m.ensureVisible()
		}
	case tcell.KeyDown:
		if m.cursor < len(m.flat)-1 {
			m.cursor++
			m.ensureVisible()
		}
	case tcell.KeyEnter:
		cur := m.current()
		if cur == nil {
			break
		}

		if !cur.isDir {
			return false, cur.path, true
		}

		if cur.expanded {
			cur.expanded = false
		} else {
			_ = cur.load()
			cur.expanded = true
		}

		m.rebuildFlat()
	case tcell.KeyRune:
		switch ev.Str() {
		case "q":
			return true, "", false
		case "r":
			m.refresh()
		}
	}

	return false, "", false
}

func (m *model) onResize(w, h int) {
	m.w, m.h = w, h
	m.renderRows()
}

// renderRows rebuilds the m.rows display cache from m.flat. It is the only
// place row text is built/truncated, so it must be called whenever the tree or
// terminal width changes. draw() then reads the cache and only applies the
// cursor overlay, keeping a held cursor move cheap.
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

		if m.w > 0 && runewidth.StringWidth(row) > m.w {
			row = truncateRight(row, m.w)
		}

		m.rows[i] = row
	}
}

func truncateRight(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}

	return runewidth.Truncate(s, maxWidth, "")
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

// ui is the slice of tcell.Screen the render loop needs. Kept narrow so it can
// be faked in tests — tcell v3 removed the public SimulationScreen.
type ui interface {
	Size() (int, int)
	SetContent(x, y int, primary rune, combining []rune, style tcell.Style)
	Show()
	Sync()
	EventQ() chan tcell.Event
}

// startSync forwards path to vim off the UI goroutine. Fire-and-forget: sync is
// a deliberate single keypress now, so there's nothing to coalesce or report.
func startSync(vim, server, path string) {
	go func() { _ = openInVim(vim, server, path) }()
}

// drawRow writes one screen row and pads it to full width with spaces in the
// same style. The padding makes the row self-clearing (no screen Clear needed)
// and gives the selected row a full-width reverse bar.
func drawRow(s ui, y int, text string, w int, st tcell.Style) {
	x := 0

	for _, r := range text {
		if x >= w {
			break
		}

		s.SetContent(x, y, r, nil, st)
		x += runewidth.RuneWidth(r)
	}

	for ; x < w; x++ {
		s.SetContent(x, y, ' ', nil, st)
	}
}

func draw(s ui, m *model) {
	w, h := s.Size()

	start := m.scroll
	end := min(start+m.maxRows(), len(m.flat))

	y := 0

	for i := start; i < end; i++ {
		st := tcell.StyleDefault
		if i == m.cursor {
			st = st.Reverse(true)
		}

		drawRow(s, y, m.rows[i], w, st)
		y++
	}

	for ; y < h; y++ {
		drawRow(s, y, "", w, tcell.StyleDefault)
	}

	s.Show()
}

// loop is the whole event loop: poll, mutate the model, draw. One goroutine, no
// frame-rate throttle — every keystroke draws immediately.
func loop(s ui, m model) {
	w, h := s.Size()
	m.onResize(w, h)
	draw(s, &m)

	for {
		ev, ok := <-s.EventQ()
		if !ok {
			return
		}

		switch ev := ev.(type) {
		case *tcell.EventResize:
			s.Sync()
			w, h := s.Size()
			m.onResize(w, h)
			draw(s, &m)
		case *tcell.EventKey:
			quit, path, ok := m.onKey(ev)
			if quit {
				return
			}

			if ok {
				startSync(m.vim, m.server, path)
			}

			draw(s, &m)
		}
	}
}

var runProgram = func(m model) error {
	s, err := tcell.NewScreen()
	if err != nil {
		return err
	}

	if err := s.Init(); err != nil {
		return err
	}

	defer s.Fini()

	s.HideCursor()
	loop(s, m)

	return nil
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
