// Package main is the vitree TUI binary.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/mattn/go-runewidth"
)

type model struct {
	root         *node
	flat         []*node
	cursor       int
	scroll       int
	vim          string
	server       string
	syncing      bool
	activePath   string
	pendingPath  string
	refreshEvery time.Duration
	refreshing   bool
	w, h         int
	rows         []string
}

// defaultRefresh is the auto-refresh interval used when -refresh is not given.
const defaultRefresh = 2 * time.Second

func clamp(v, lo, hi int) int {
	return max(lo, min(hi, v))
}

// move shifts the cursor by delta if that stays in range, returning the vim
// sync request for the newly-highlighted node (coalesced via requestSync).
func (m *model) move(delta int) (string, bool) {
	next := m.cursor + delta
	if next < 0 || next >= len(m.flat) {
		return "", false
	}

	m.cursor = next
	m.ensureVisible()

	return m.syncCurrent()
}

// activate is the enter/click action: toggle a directory. Files are already
// opened in vim the moment the selection lands on them (see move), so there is
// nothing extra to do for a file here.
func (m *model) activate() (string, bool) {
	cur := m.current()
	if cur == nil || !cur.isDir {
		return "", false
	}

	if cur.expanded {
		cur.expanded = false
	} else {
		_ = cur.load()
		cur.expanded = true
	}

	m.rebuildFlat()

	return "", false
}

// expand opens a collapsed directory. Files are already opened in vim on
// selection (see move); an already-expanded dir is a no-op.
func (m *model) expand() (string, bool) {
	cur := m.current()
	if cur == nil || !cur.isDir || cur.expanded {
		return "", false
	}

	_ = cur.load()
	cur.expanded = true

	m.rebuildFlat()

	return "", false
}

// collapse closes the current expanded directory; otherwise it moves the
// cursor to the parent directory (the nearest shallower entry). At the top
// level with nothing to collapse it is a no-op.
func (m *model) collapse() (string, bool) {
	cur := m.current()
	if cur == nil {
		return "", false
	}

	if cur.isDir && cur.expanded {
		cur.expanded = false

		m.rebuildFlat()

		return "", false
	}

	// No parent pointer on node by design: the parent is just the nearest
	// earlier flat entry that is one level shallower, so scan back to it.
	for i := m.cursor - 1; i >= 0; i-- {
		if m.flat[i].depth == cur.depth-1 {
			m.cursor = i
			m.ensureVisible()

			return m.syncCurrent()
		}
	}

	return "", false
}

// onKey applies a keystroke. It reports whether to quit and, if a file became
// current, the path to forward to vim. hjkl mirrors the arrow keys.
func (m *model) onKey(ev *tcell.EventKey) (bool, string, bool) {
	switch ev.Key() {
	case tcell.KeyCtrlC:
		return true, "", false
	case tcell.KeyUp:
		path, ok := m.move(-1)
		return false, path, ok
	case tcell.KeyDown:
		path, ok := m.move(1)
		return false, path, ok
	case tcell.KeyLeft:
		path, ok := m.collapse()
		return false, path, ok
	case tcell.KeyRight:
		path, ok := m.expand()
		return false, path, ok
	case tcell.KeyEnter:
		path, ok := m.activate()
		return false, path, ok
	case tcell.KeyRune:
		switch ev.Str() {
		case "q":
			return true, "", false
		case "r":
			m.refresh()
		case "k":
			path, ok := m.move(-1)
			return false, path, ok
		case "j":
			path, ok := m.move(1)
			return false, path, ok
		case "h":
			path, ok := m.collapse()
			return false, path, ok
		case "l":
			path, ok := m.expand()
			return false, path, ok
		}
	}

	return false, "", false
}

// onMouse applies a mouse event: the wheel moves the selection one row; a left
// click selects the clicked row and activates it. A click outside the list and
// button-release events are no-ops.
func (m *model) onMouse(ev *tcell.EventMouse) (string, bool) {
	switch b := ev.Buttons(); {
	case b&tcell.WheelUp != 0:
		return m.move(-1)
	case b&tcell.WheelDown != 0:
		return m.move(1)
	case b&tcell.ButtonPrimary != 0:
		_, y := ev.Position()

		idx := m.scroll + y
		if y < 0 || y >= m.maxRows() || idx >= len(m.flat) {
			return "", false
		}

		m.cursor = idx
		m.ensureVisible()

		// Clicking selects the row: a file opens (like moving onto it), a
		// directory toggles.
		if cur := m.current(); cur != nil && cur.isDir {
			return m.activate()
		}

		return m.syncCurrent()
	}

	return "", false
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

// ensureVisible scrolls the minimum amount so the cursor sits inside the
// visible window [scroll, scroll+mr): pin to the top when the cursor went
// above it, to the bottom when it went below, then clamp so the last screen
// isn't scrolled past the end.
func (m *model) ensureVisible() {
	mr := m.maxRows()
	if mr <= 0 { // no usable height yet (e.g. size unknown at startup)
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

// syncCurrent returns the path to forward to vim for the highlighted node, or
// ok=false when it is a directory / nothing is selected.
func (m *model) syncCurrent() (string, bool) {
	cur := m.current()
	if cur == nil || cur.isDir {
		m.pendingPath = ""
		return "", false
	}

	return m.requestSync(cur.path)
}

// requestSync coalesces vim forwarding: only one vim exec runs at a time. While
// one is in flight the latest requested path is remembered and returned by
// finishSync once the running one completes, so flying through files collapses
// to the last. ok=true means the caller should start the exec now.
func (m *model) requestSync(path string) (string, bool) {
	if m.syncing {
		if path == m.activePath {
			m.pendingPath = ""
		} else {
			m.pendingPath = path
		}

		return "", false
	}

	m.syncing = true
	m.activePath = path

	return path, true
}

func (m *model) finishSync(donePath string) (string, bool) {
	m.syncing = false
	m.activePath = ""

	pending := m.pendingPath
	m.pendingPath = ""

	if pending == "" || pending == donePath {
		return "", false
	}

	return m.requestSync(pending)
}

// applyRoot swaps in a freshly built tree, keeping the cursor on the same file
// path. Cheap (no disk) so it is safe to run on the UI goroutine.
func (m *model) applyRoot(root *node) {
	var cursorPath string
	if cur := m.current(); cur != nil {
		cursorPath = cur.path
	}

	m.root = root

	m.rebuildFlat()
	m.restoreCursor(cursorPath)
}

// refresh re-reads the tree from disk synchronously. Bound to `r`; the
// background auto-refresh uses the same buildTree off the UI goroutine.
func (m *model) refresh() {
	if root, err := buildTree(m.root.path, m.root.expandedPaths()); err == nil {
		m.applyRoot(root)
	}
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

// syncDoneEvent is posted to the tcell loop by the background vim exec started
// in startSync, so completion is handled on the single UI goroutine.
type syncDoneEvent struct {
	at   time.Time
	path string
}

func (e *syncDoneEvent) When() time.Time { return e.at }

// refreshTickEvent is posted by the auto-refresh ticker; the loop reacts by
// spawning a background rebuild. refreshDoneEvent carries that rebuilt tree
// back (root nil = the rebuild failed; the current tree is kept).
type refreshTickEvent struct{}

func (*refreshTickEvent) When() time.Time { return time.Time{} }

type refreshDoneEvent struct{ root *node }

func (*refreshDoneEvent) When() time.Time { return time.Time{} }

// startRefreshTicker posts a refreshTickEvent every d. It returns a stop func;
// the goroutine itself is left to die with the process (CLI, exits on quit).
func startRefreshTicker(s ui, d time.Duration) func() {
	t := time.NewTicker(d)

	go func() {
		for range t.C {
			func() {
				defer func() { _ = recover() }() // EventQ closed on shutdown

				s.EventQ() <- &refreshTickEvent{}
			}()
		}
	}()

	return t.Stop
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

func startSync(s ui, vim, server, path string) {
	go func() {
		_ = openInVim(vim, server, path)

		// EventQ is closed on shutdown; a send racing Fini would panic.
		defer func() { _ = recover() }()

		s.EventQ() <- &syncDoneEvent{at: time.Now(), path: path}
	}()
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
		// Order matters: the selected row is always plain reverse video, even
		// if it is itself gitignored — reverse wins over dim so the cursor
		// never looks washed out.
		st := tcell.StyleDefault

		switch {
		case i == m.cursor:
			st = st.Reverse(true)
		case m.flat[i].ignored:
			st = st.Dim(true)
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

	if m.refreshEvery > 0 {
		defer startRefreshTicker(s, m.refreshEvery)()
	}

	for {
		ev, ok := <-s.EventQ()
		if !ok {
			return // screen shut down: EventQ closed
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
				startSync(s, m.vim, m.server, path)
			}

			draw(s, &m)
		case *tcell.EventMouse:
			if path, ok := m.onMouse(ev); ok {
				startSync(s, m.vim, m.server, path)
			}

			draw(s, &m)
		case *refreshTickEvent:
			// One rebuild at a time; a tick while one is in flight is dropped.
			if m.refreshing {
				break
			}

			// Snapshot what the rebuild needs here, on the UI goroutine — the
			// tree is owned by this goroutine and must not be read off it. The
			// slow part (buildTree: disk + git) then runs in a goroutine so it
			// never blocks scrolling; it reports back via refreshDoneEvent.
			m.refreshing = true
			rootPath := m.root.path
			expanded := m.root.expandedPaths()

			go func() {
				root, err := buildTree(rootPath, expanded)

				defer func() { _ = recover() }() // EventQ closed on shutdown

				if err != nil {
					s.EventQ() <- &refreshDoneEvent{root: nil}
					return
				}

				s.EventQ() <- &refreshDoneEvent{root: root}
			}()
		case *refreshDoneEvent:
			m.refreshing = false

			if ev.root != nil {
				m.applyRoot(ev.root)
				draw(s, &m)
			}
		case *syncDoneEvent:
			if path, ok := m.finishSync(ev.path); ok {
				startSync(s, m.vim, m.server, path)
			}
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
	s.EnableMouse()
	loop(s, m)

	return nil
}

var version = "dev"

func run(args []string) error {
	fs := flag.NewFlagSet("vitree", flag.ContinueOnError)
	server := fs.String("server", "", "vim --servername to send files to (auto-detected if empty)")

	vim := fs.String("vim", "vim", "vim binary to invoke (e.g. mvim, gvim, /path/to/vim)")
	refresh := fs.Duration("refresh", defaultRefresh, "auto-refresh interval; 0 disables")
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

	m.refreshEvery = *refresh

	return runProgram(m)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
