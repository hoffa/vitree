package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

func mkTree(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	for _, p := range []string{
		"b_dir/inner.txt",
		"a_dir/x.go",
		"z_file.txt",
		"a_file.md",
	} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

func mkGitignoredTree(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	for _, p := range []string{"keep.txt", "ignored.log", "build/out.txt"} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\nbuild/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.CommandContext(t.Context(), "git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	return root
}

func names(ns []*node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.name
	}

	return out
}

func writeFakeVim(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()

	p := filepath.Join(dir, "vim")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}

	return p
}

func newTestModel(t *testing.T) model {
	t.Helper()
	root := mkTree(t)

	r, err := newNode(root, 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := r.load(); err != nil {
		t.Fatal(err)
	}

	r.expanded = true
	m := model{root: r, w: 80, h: 24, server: "EDIT"}
	m.rebuildFlat()

	return m
}

func fileIndex(m model, name string) int {
	for i, n := range m.flat {
		if n.name == name {
			return i
		}
	}

	return -1
}

// fakeScreen implements the ui interface so draw/loop/startSync can be tested
// without a terminal (tcell v3 has no SimulationScreen).
type fakeScreen struct {
	w, h   int
	q      chan tcell.Event
	cells  map[[2]int]rune
	styles map[[2]int]tcell.Style
}

func newFakeScreen(w, h int) *fakeScreen {
	return &fakeScreen{
		w: w, h: h,
		q:      make(chan tcell.Event, 64),
		cells:  map[[2]int]rune{},
		styles: map[[2]int]tcell.Style{},
	}
}

func (f *fakeScreen) Size() (int, int) { return f.w, f.h }

func (f *fakeScreen) SetContent(x, y int, r rune, _ []rune, st tcell.Style) {
	f.cells[[2]int{x, y}] = r
	f.styles[[2]int{x, y}] = st
}

func (f *fakeScreen) Show()                    {}
func (f *fakeScreen) Sync()                    {}
func (f *fakeScreen) EventQ() chan tcell.Event { return f.q }

func TestNewNodeError(t *testing.T) {
	if _, err := newNode(filepath.Join(t.TempDir(), "missing"), 0); err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestLoadSortsDirsFirst(t *testing.T) {
	root := mkTree(t)

	if err := os.WriteFile(filepath.Join(root, ".dotfile"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(root, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}

	n, err := newNode(root, 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := n.load(); err != nil {
		t.Fatal(err)
	}

	want := []string{".hidden", "a_dir", "b_dir", ".dotfile", "a_file.md", "z_file.txt"}
	if got := names(n.children); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("children=%v want=%v", got, want)
	}
}

func TestLoadNonDirAndAlreadyLoaded(t *testing.T) {
	root := mkTree(t)

	f, err := newNode(filepath.Join(root, "z_file.txt"), 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := f.load(); err != nil || f.loaded {
		t.Fatalf("non-dir load: err=%v loaded=%v", err, f.loaded)
	}

	d, err := newNode(root, 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := d.load(); err != nil {
		t.Fatal(err)
	}

	d.children = nil

	if err := d.load(); err != nil || d.children != nil {
		t.Fatalf("reload should be no-op: err=%v children=%v", err, d.children)
	}
}

func TestLoadReadError(t *testing.T) {
	root := mkTree(t)
	bad := filepath.Join(root, "a_dir")

	if err := os.Chmod(bad, 0); err != nil {
		t.Skip(err)
	}

	defer func() { _ = os.Chmod(bad, 0o755) }()

	n, err := newNode(bad, 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := n.load(); err == nil {
		t.Fatal("expected read error")
	}
}

func TestClamp(t *testing.T) {
	for _, c := range []struct{ v, lo, hi, want int }{
		{5, 0, 10, 5},
		{-1, 0, 10, 0},
		{11, 0, 10, 10},
	} {
		if got := clamp(c.v, c.lo, c.hi); got != c.want {
			t.Fatalf("clamp(%d,%d,%d)=%d want %d", c.v, c.lo, c.hi, got, c.want)
		}
	}
}

func TestOnKeyQuit(t *testing.T) {
	m := newTestModel(t)

	if quit, _, _ := m.onKey(ekey(tcell.KeyRune, "q")); !quit {
		t.Fatal("q should quit")
	}

	if quit, _, _ := m.onKey(ekey(tcell.KeyCtrlC, "")); !quit {
		t.Fatal("ctrl+c should quit")
	}
}

func TestOnKeyUnknownNoop(t *testing.T) {
	m := newTestModel(t)

	for _, ev := range []*tcell.EventKey{
		ekey(tcell.KeyRune, "x"), // unhandled rune
		ekey(tcell.KeyEsc, ""),   // unhandled key
	} {
		if quit, _, ok := m.onKey(ev); quit || ok {
			t.Fatalf("unknown key %v should be a no-op", ev.Key())
		}
	}
}

func TestOnKeyMove(t *testing.T) {
	m := newTestModel(t) // a_dir, b_dir, a_file.md, z_file.txt

	if _, _, ok := m.onKey(ekey(tcell.KeyUp, "")); m.cursor != 0 || ok {
		t.Fatalf("up at top: cursor=%d ok=%v", m.cursor, ok)
	}

	if _, _, ok := m.onKey(ekey(tcell.KeyDown, "")); m.cursor != 1 || ok {
		t.Fatalf("down onto b_dir: cursor=%d ok=%v (dir, no sync)", m.cursor, ok)
	}

	_, path, ok := m.onKey(ekey(tcell.KeyDown, "")) // a_file.md
	if m.cursor != 2 || !ok || path != m.current().path || !m.syncing {
		t.Fatalf("down onto file: cursor=%d ok=%v path=%q syncing=%v", m.cursor, ok, path, m.syncing)
	}

	m.cursor = len(m.flat) - 1
	if _, _, _ = m.onKey(ekey(tcell.KeyDown, "")); m.cursor != len(m.flat)-1 {
		t.Fatalf("down at bottom should not move, got %d", m.cursor)
	}

	if _, _, _ = m.onKey(ekey(tcell.KeyUp, "")); m.cursor != len(m.flat)-2 {
		t.Fatalf("up should move, got %d", m.cursor)
	}
}

func TestOnKeyEnter(t *testing.T) {
	m := newTestModel(t) // cursor 0 = a_dir

	if _, _, ok := m.onKey(ekey(tcell.KeyEnter, "")); ok || !m.current().expanded {
		t.Fatalf("enter on dir: ok=%v expanded=%v", ok, m.current().expanded)
	}

	if !strings.Contains(strings.Join(names(m.flat), ","), "x.go") {
		t.Fatalf("expanded children missing: %v", names(m.flat))
	}

	if _, _, _ = m.onKey(ekey(tcell.KeyEnter, "")); m.current().expanded {
		t.Fatal("enter again should collapse")
	}

	// Enter on a file does nothing: it is already open from selection.
	m.cursor = fileIndex(m, "a_file.md")

	if _, path, ok := m.onKey(ekey(tcell.KeyEnter, "")); ok || path != "" {
		t.Fatalf("enter on file should be a no-op: ok=%v path=%q", ok, path)
	}

	empty := model{root: &node{}}
	if _, _, ok := empty.onKey(ekey(tcell.KeyEnter, "")); ok {
		t.Fatal("enter with no current node should not sync")
	}
}

func TestOnKeyJK(t *testing.T) {
	m := newTestModel(t)

	if _, _, _ = m.onKey(ekey(tcell.KeyRune, "j")); m.cursor != 1 {
		t.Fatalf("j should move down, cursor=%d", m.cursor)
	}

	if _, _, _ = m.onKey(ekey(tcell.KeyRune, "k")); m.cursor != 0 {
		t.Fatalf("k should move up, cursor=%d", m.cursor)
	}
}

func TestOnKeyExpandCollapse(t *testing.T) {
	m := newTestModel(t) // cursor 0 = a_dir (collapsed)

	// l / Right expands a collapsed dir.
	if _, _, ok := m.onKey(ekey(tcell.KeyRune, "l")); ok || !m.current().expanded {
		t.Fatalf("l on dir: ok=%v expanded=%v", ok, m.current().expanded)
	}

	if !strings.Contains(strings.Join(names(m.flat), ","), "x.go") {
		t.Fatalf("expanded children missing: %v", names(m.flat))
	}

	// l again on an already-expanded dir is a no-op.
	n := len(m.flat)
	if _, _, ok := m.onKey(ekey(tcell.KeyRune, "l")); ok || len(m.flat) != n {
		t.Fatalf("l on expanded dir should be a no-op: ok=%v flat=%d", ok, len(m.flat))
	}

	// Move onto the child, then h jumps to the parent.
	m.cursor = fileIndex(m, "x.go")
	if _, _, _ = m.onKey(ekey(tcell.KeyRune, "h")); m.current().name != "a_dir" {
		t.Fatalf("h on child should select parent, got %q", m.current().name)
	}

	// h on the expanded parent collapses it.
	if _, _, _ = m.onKey(ekey(tcell.KeyLeft, "")); m.current().expanded {
		t.Fatal("Left on expanded dir should collapse it")
	}

	if strings.Contains(strings.Join(names(m.flat), ","), "x.go") {
		t.Fatal("collapsed dir should hide children")
	}

	// h on a collapsed top-level dir (no shallower parent) is a no-op.
	before := m.cursor
	if _, _, ok := m.onKey(ekey(tcell.KeyRune, "h")); ok || m.cursor != before {
		t.Fatalf("h at top level should be a no-op: ok=%v cursor=%d", ok, m.cursor)
	}

	// l / Right on a file does nothing: it is already open from selection.
	m.cursor = fileIndex(m, "a_file.md")
	if _, path, ok := m.onKey(ekey(tcell.KeyRight, "")); ok || path != "" {
		t.Fatalf("Right on file should be a no-op: ok=%v path=%q", ok, path)
	}

	// Empty model: collapse/expand with no current node are no-ops.
	empty := model{root: &node{}}
	if _, _, ok := empty.onKey(ekey(tcell.KeyRune, "h")); ok {
		t.Fatal("h with no node should be a no-op")
	}

	if _, _, ok := empty.onKey(ekey(tcell.KeyRune, "l")); ok {
		t.Fatal("l with no node should be a no-op")
	}
}

func emouse(y int, b tcell.ButtonMask) *tcell.EventMouse {
	return tcell.NewEventMouse(0, y, b, tcell.ModNone)
}

func TestOnMouseWheel(t *testing.T) {
	m := newTestModel(t) // a_dir, b_dir, a_file.md, z_file.txt

	if _, ok := m.onMouse(emouse(0, tcell.WheelUp)); m.cursor != 0 || ok {
		t.Fatalf("wheel up at top: cursor=%d ok=%v", m.cursor, ok)
	}

	if _, ok := m.onMouse(emouse(0, tcell.WheelDown)); m.cursor != 1 || ok {
		t.Fatalf("wheel down onto b_dir: cursor=%d ok=%v (dir, no sync)", m.cursor, ok)
	}

	if _, ok := m.onMouse(emouse(0, tcell.WheelDown)); m.cursor != 2 || !ok {
		t.Fatalf("wheel down onto a_file.md: cursor=%d ok=%v (file syncs)", m.cursor, ok)
	}
}

func TestOnMouseClick(t *testing.T) {
	// Click a file row: selects it and forwards to vim.
	m := newTestModel(t)
	if _, ok := m.onMouse(emouse(2, tcell.ButtonPrimary)); m.cursor != 2 || !ok {
		t.Fatalf("click file: cursor=%d ok=%v", m.cursor, ok)
	}

	// Click a directory row: selects and toggles it, no sync.
	m = newTestModel(t)
	if _, ok := m.onMouse(emouse(0, tcell.ButtonPrimary)); ok || !m.current().expanded {
		t.Fatalf("click dir: ok=%v expanded=%v", ok, m.current().expanded)
	}

	// Click below the list and button-release are no-ops.
	m = newTestModel(t)
	before := m.cursor

	if _, ok := m.onMouse(emouse(999, tcell.ButtonPrimary)); ok || m.cursor != before {
		t.Fatalf("click out of range moved/synced: cursor=%d ok=%v", m.cursor, ok)
	}

	if _, ok := m.onMouse(emouse(0, tcell.ButtonNone)); ok {
		t.Fatal("button release should be a no-op")
	}
}

func TestOnKeyRefresh(t *testing.T) {
	m := newTestModel(t)
	want := m.current().path

	if err := os.WriteFile(filepath.Join(m.root.path, "0_new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	m.onKey(ekey(tcell.KeyRune, "r"))

	if !strings.Contains(strings.Join(names(m.flat), ","), "0_new.txt") {
		t.Fatalf("refresh missed new file: %v", names(m.flat))
	}

	if got := m.current().path; got != want {
		t.Fatalf("cursor not preserved: got %q want %q", got, want)
	}
}

func TestRefreshErrorIsNoop(t *testing.T) {
	m := newTestModel(t)
	before := strings.Join(names(m.flat), ",")

	if err := os.Chmod(m.root.path, 0); err != nil {
		t.Skip(err)
	}

	defer func() { _ = os.Chmod(m.root.path, 0o755) }()

	m.refresh()

	if got := strings.Join(names(m.flat), ","); got != before {
		t.Fatalf("failed refresh should leave tree unchanged: %q -> %q", before, got)
	}
}

func TestOnResize(t *testing.T) {
	m := newTestModel(t)
	m.onResize(40, 12)

	if m.w != 40 || m.h != 12 || len(m.rows) != len(m.flat) {
		t.Fatalf("resize: w=%d h=%d rows=%d flat=%d", m.w, m.h, len(m.rows), len(m.flat))
	}
}

func TestRenderRowsMarkers(t *testing.T) {
	m := newTestModel(t)

	if !strings.HasPrefix(m.rows[0], "+ a_dir/") {
		t.Fatalf("collapsed dir marker: %q", m.rows[0])
	}

	if !strings.HasPrefix(m.rows[2], "  a_file.md") {
		t.Fatalf("file marker (two spaces): %q", m.rows[2])
	}

	m.onKey(ekey(tcell.KeyEnter, "")) // expand a_dir

	if !strings.HasPrefix(m.rows[0], "- a_dir/") {
		t.Fatalf("expanded dir marker: %q", m.rows[0])
	}

	if !strings.HasPrefix(m.rows[1], "    x.go") {
		t.Fatalf("nested file indent: %q", m.rows[1])
	}
}

func TestRenderRowsTruncates(t *testing.T) {
	m := newTestModel(t)
	m.w = 4
	m.renderRows()

	for _, r := range m.rows {
		if len([]rune(r)) > 4 {
			t.Fatalf("row not truncated: %q", r)
		}
	}
}

func TestTruncateRight(t *testing.T) {
	if got := truncateRight("abc", 5); got != "abc" {
		t.Fatalf("short changed: %q", got)
	}

	if got := truncateRight("abcdef", 3); got != "abc" {
		t.Fatalf("truncateRight=%q", got)
	}

	if got := truncateRight("abcdef", 0); got != "" {
		t.Fatalf("zero width should be empty: %q", got)
	}
}

func TestRebuildFlatRespectsExpansion(t *testing.T) {
	m := newTestModel(t)

	if got := names(m.flat); len(got) != 4 {
		t.Fatalf("collapsed top-level: got %v", got)
	}

	for _, c := range m.root.children {
		if c.name == "a_dir" {
			if err := c.load(); err != nil {
				t.Fatal(err)
			}

			c.expanded = true
		}
	}

	m.rebuildFlat()

	want := []string{"a_dir", "x.go", "b_dir", "a_file.md", "z_file.txt"}
	if got := names(m.flat); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expanded got=%v want=%v", got, want)
	}
}

func TestMaxRows(t *testing.T) {
	m := newTestModel(t)
	m.h = 10

	if got := m.maxRows(); got != 10 {
		t.Fatalf("maxRows=%d want 10", got)
	}

	m.h = 0
	if got := m.maxRows(); got != len(m.flat) {
		t.Fatalf("maxRows h=0 should be len(flat)=%d, got %d", len(m.flat), got)
	}
}

func TestEnsureVisible(t *testing.T) {
	m := newTestModel(t)
	m.h = 2

	m.cursor = 3
	m.ensureVisible()

	if m.scroll != 2 {
		t.Fatalf("scroll should follow cursor down, got %d", m.scroll)
	}

	m.cursor = 0
	m.ensureVisible()

	if m.scroll != 0 {
		t.Fatalf("scroll should follow cursor up, got %d", m.scroll)
	}

	e := model{root: &node{}}
	e.ensureVisible()

	if e.scroll != 0 {
		t.Fatalf("empty ensureVisible scroll=%d", e.scroll)
	}
}

func TestCurrentOutOfRange(t *testing.T) {
	m := newTestModel(t)

	m.cursor = -1
	if m.current() != nil {
		t.Fatal("negative cursor should be nil")
	}

	m.cursor = len(m.flat)
	if m.current() != nil {
		t.Fatal("overflow cursor should be nil")
	}
}

func TestSyncCoalesce(t *testing.T) {
	m := newTestModel(t)
	aFile := fileIndex(m, "a_file.md")
	zFile := fileIndex(m, "z_file.txt")

	m.cursor = aFile

	path, ok := m.syncCurrent()
	if !ok || !m.syncing || path != m.current().path || m.pendingPath != "" {
		t.Fatalf("first sync should start: ok=%v syncing=%v path=%q", ok, m.syncing, path)
	}

	active := m.activePath

	m.cursor = zFile
	if _, ok := m.syncCurrent(); ok || m.pendingPath == "" || m.pendingPath == active {
		t.Fatalf("in-flight move should only set pending: ok=%v pending=%q", ok, m.pendingPath)
	}

	m.cursor = aFile // back to active
	if _, ok := m.syncCurrent(); ok || m.pendingPath != "" {
		t.Fatalf("return to active should clear pending: ok=%v pending=%q", ok, m.pendingPath)
	}

	m.cursor = zFile
	m.syncCurrent() // pending = z again

	next, ok := m.finishSync(m.flat[aFile].path)
	if !ok || !m.syncing || next != m.flat[zFile].path {
		t.Fatalf("completion should start follow-up: ok=%v next=%q", ok, next)
	}

	if _, ok := m.finishSync(m.flat[zFile].path); ok || m.syncing || m.pendingPath != "" {
		t.Fatalf("queue should drain clean: ok=%v syncing=%v pending=%q", ok, m.syncing, m.pendingPath)
	}
}

func TestSyncCurrentOnDir(t *testing.T) {
	m := newTestModel(t)
	m.pendingPath = "/stale"

	if _, ok := m.syncCurrent(); ok { // cursor 0 = a_dir
		t.Fatal("dir selection should not sync")
	}

	if m.pendingPath != "" {
		t.Fatalf("pending should be cleared, got %q", m.pendingPath)
	}
}

func TestRestoreCursor(t *testing.T) {
	m := newTestModel(t)
	m.cursor = 2

	m.restoreCursor("")

	if m.cursor != 2 {
		t.Fatalf("empty path should be no-op, got %d", m.cursor)
	}

	want := m.flat[1].path
	m.restoreCursor(want)

	if m.flat[m.cursor].path != want {
		t.Fatalf("cursor not restored to %q", want)
	}

	m.cursor = 999
	m.restoreCursor("/does/not/exist")

	if m.cursor != len(m.flat)-1 {
		t.Fatalf("missing path should clamp cursor, got %d", m.cursor)
	}
}

func TestExpandedPaths(t *testing.T) {
	m := newTestModel(t)
	m.onKey(ekey(tcell.KeyEnter, "")) // expand a_dir

	if !m.root.expandedPaths()[filepath.Join(m.root.path, "a_dir")] {
		t.Fatalf("expandedPaths missing a_dir: %v", m.root.expandedPaths())
	}
}

func TestBuildTree(t *testing.T) {
	root := mkTree(t)
	aDir := filepath.Join(root, "a_dir")

	r, err := buildTree(root, map[string]bool{root: true, aDir: true})
	if err != nil {
		t.Fatal(err)
	}

	var a, b *node

	for _, c := range r.children {
		switch c.name {
		case "a_dir":
			a = c
		case "b_dir":
			b = c
		}
	}

	if a == nil || !a.expanded || len(a.children) == 0 {
		t.Fatalf("a_dir should be expanded with children: %+v", a)
	}

	if b == nil || b.expanded || b.loaded {
		t.Fatalf("b_dir should be collapsed and unloaded: %+v", b)
	}
}

func TestBuildTreeErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0); err != nil {
		t.Skip(err)
	}

	defer func() { _ = os.Chmod(dir, 0o755) }()

	if _, err := buildTree(dir, nil); err == nil {
		t.Fatal("expected root read error")
	}

	if _, err := buildTree(filepath.Join(dir, "nope"), nil); err == nil {
		t.Fatal("expected newNode error for missing root")
	}

	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := buildTree(file, nil); err == nil {
		t.Fatal("expected not-a-directory error")
	}
}

func TestBuildTreeTolerantOfSubdirError(t *testing.T) {
	root := mkTree(t)
	bad := filepath.Join(root, "a_dir")

	if err := os.Chmod(bad, 0); err != nil {
		t.Skip(err)
	}

	defer func() { _ = os.Chmod(bad, 0o755) }()

	r, err := buildTree(root, map[string]bool{root: true, bad: true})
	if err != nil {
		t.Fatalf("root should still build: %v", err)
	}

	if len(r.children) == 0 {
		t.Fatal("root children expected")
	}
}

func TestGitIgnored(t *testing.T) {
	root := mkGitignoredTree(t)

	keep := filepath.Join(root, "keep.txt")
	log := filepath.Join(root, "ignored.log")
	build := filepath.Join(root, "build")

	ig := gitIgnored(root, []string{keep, log, build})
	if ig[keep] || !ig[log] || !ig[build] {
		t.Fatalf("gitIgnored wrong: keep=%v log=%v build=%v", ig[keep], ig[log], ig[build])
	}

	// .git and its contents are reported by gitIgnored itself (check-ignore
	// never reports them), even outside a repo.
	gitDir := filepath.Join(root, ".git")
	gitHead := filepath.Join(root, ".git", "HEAD")

	ig = gitIgnored(t.TempDir(), []string{gitDir, gitHead, "/plain/x"})
	if !ig[gitDir] || !ig[gitHead] || ig["/plain/x"] {
		t.Fatalf("gitIgnored .git handling wrong: %v", ig)
	}

	// Empty input is a graceful no-op.
	if len(gitIgnored(root, nil)) != 0 {
		t.Fatal("empty paths should yield empty set")
	}
}

func TestBuildTreeMarksIgnored(t *testing.T) {
	root := mkGitignoredTree(t)

	r, err := buildTree(root, map[string]bool{
		root:                         true,
		filepath.Join(root, "build"): true,
		filepath.Join(root, ".git"):  true,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}

	r.walk(func(n *node) { got[n.name] = n.ignored })

	// keep.txt clean; *.log and build/ ignored; out.txt ignored via its
	// ignored parent; .git and everything under it (e.g. HEAD) dimmed.
	if got["keep.txt"] || !got["ignored.log"] || !got["build"] ||
		!got["out.txt"] || !got[".git"] || !got["HEAD"] {
		t.Fatalf("ignored flags wrong: %v", got)
	}
}

func TestLoadInheritsIgnored(t *testing.T) {
	root := mkTree(t) // no git needed: pure inheritance

	n, err := newNode(filepath.Join(root, "a_dir"), 1)
	if err != nil {
		t.Fatal(err)
	}

	n.ignored = true
	if err := n.load(); err != nil {
		t.Fatal(err)
	}

	if len(n.children) == 0 {
		t.Fatal("expected children")
	}

	for _, c := range n.children {
		if !c.ignored {
			t.Fatalf("child %q under an ignored dir must be ignored", c.name)
		}
	}
}

func TestNewModel(t *testing.T) {
	dir := mkTree(t)
	vim := writeFakeVim(t, `echo "EDIT"`)

	if _, err := newModel(vim, "S", dir); err != nil {
		t.Fatalf("newModel: %v", err)
	}

	if _, err := newModel(vim, "", dir); err != nil {
		t.Fatalf("newModel detect: %v", err)
	}

	if _, err := newModel(writeFakeVim(t, `echo ""`), "", dir); err != nil {
		t.Fatalf("newModel with no servers should fall back to default: %v", err)
	}

	if _, err := newModel(vim, "S", filepath.Join(dir, "z_file.txt")); err == nil {
		t.Fatal("expected non-dir error")
	}

	if _, err := newModel(vim, "S", filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected missing path error")
	}
}

func TestNewModelLoadError(t *testing.T) {
	dir := t.TempDir()
	vim := writeFakeVim(t, `echo "EDIT"`)

	if err := os.Chmod(dir, 0); err != nil {
		t.Skip(err)
	}

	defer func() { _ = os.Chmod(dir, 0o755) }()

	if _, err := newModel(vim, "S", dir); err == nil {
		t.Fatal("expected load error")
	}
}

func TestRun(t *testing.T) {
	dir := mkTree(t)
	t.Chdir(dir)

	vim := writeFakeVim(t, `echo "EDIT"`)

	prev := runProgram
	defer func() { runProgram = prev }()

	called := false

	var got model

	runProgram = func(m model) error { called = true; got = m; return nil }

	if err := run([]string{"-vim", vim, "-server", "S"}); err != nil || !called {
		t.Fatalf("run: err=%v called=%v", err, called)
	}

	if got.refreshEvery != defaultRefresh {
		t.Fatalf("default refresh = %v, want %v", got.refreshEvery, defaultRefresh)
	}

	if err := run([]string{"-vim", vim, "-server", "S", "-refresh", "0"}); err != nil {
		t.Fatalf("run -refresh 0: %v", err)
	}

	if got.refreshEvery != 0 {
		t.Fatalf("-refresh 0 should disable, got %v", got.refreshEvery)
	}

	called = false
	if err := run([]string{"-version"}); err != nil || called {
		t.Fatalf("run -version: err=%v called=%v", err, called)
	}

	if err := run([]string{"-bogus"}); err == nil {
		t.Fatal("expected flag parse error")
	}

	if err := run([]string{"-vim", "/no/such/vim"}); err == nil {
		t.Fatal("expected newModel error (vim detect fails)")
	}
}

func TestDetectVimServer(t *testing.T) {
	if got, err := detectVimServer(writeFakeVim(t, `echo "EDIT"`)); err != nil || got != "EDIT" {
		t.Fatalf("single: got=%q err=%v", got, err)
	}

	if got, err := detectVimServer(writeFakeVim(t, `echo ""`)); err != nil || got != defaultServer {
		t.Fatalf("no servers: got=%q err=%v, want %q", got, err, defaultServer)
	}

	if _, err := detectVimServer(writeFakeVim(t, `echo "A"; echo "B"`)); err == nil {
		t.Fatal("expected error for multiple servers")
	}

	if _, err := detectVimServer("/no/such/vim"); err == nil {
		t.Fatal("expected error for bad binary")
	}
}

func TestOpenInVim(t *testing.T) {
	if err := openInVim("vim", "", "/tmp/x"); err == nil {
		t.Fatal("expected error for empty server")
	}

	if err := openInVim(writeFakeVim(t, `exit 0`), "S", "/tmp/x"); err != nil {
		t.Fatalf("fake vim exit 0: %v", err)
	}

	if err := openInVim(writeFakeVim(t, `echo boom >&2; exit 1`), "S", "/tmp/x"); err == nil {
		t.Fatal("expected error for non-zero exit")
	}
}

func ekey(k tcell.Key, s string) *tcell.EventKey {
	return tcell.NewEventKey(k, s, tcell.ModNone)
}

func TestDraw(t *testing.T) {
	s := newFakeScreen(30, 10)
	m := newTestModel(t)
	m.onResize(s.Size())

	draw(s, &m)

	if got := s.cells[[2]int{0, 0}]; got != '+' { // rows[0] = "+ a_dir/"
		t.Fatalf("first cell=%q want '+'", got)
	}

	if got := s.cells[[2]int{29, 9}]; got != ' ' {
		t.Fatalf("blank tail cell=%q want ' '", got)
	}
}

func TestDrawDimsIgnored(t *testing.T) {
	m := newTestModel(t) // a_dir, b_dir, a_file.md, z_file.txt
	m.flat[1].ignored = true
	m.cursor = 0

	s := newFakeScreen(30, 10)
	m.onResize(s.Size())
	draw(s, &m)

	if !s.styles[[2]int{0, 1}].HasDim() {
		t.Fatal("ignored row should be dim")
	}

	if s.styles[[2]int{0, 0}].HasDim() {
		t.Fatal("non-ignored row should not be dim")
	}

	if !s.styles[[2]int{0, 0}].HasReverse() {
		t.Fatal("selected row should be reverse video")
	}

	// The selected row wins over dim even if it is itself ignored.
	m.flat[0].ignored = true
	draw(s, &m)

	if st := s.styles[[2]int{0, 0}]; !st.HasReverse() || st.HasDim() {
		t.Fatalf("selected ignored row: reverse=%v dim=%v", st.HasReverse(), st.HasDim())
	}
}

func TestDrawColorsDirs(t *testing.T) {
	m := newTestModel(t) // a_dir, b_dir (dirs), a_file.md, z_file.txt (files)
	m.cursor = 2         // keep the cursor off the dirs we assert on

	s := newFakeScreen(30, 10)
	m.onResize(s.Size())
	draw(s, &m)

	if fg := s.styles[[2]int{0, 0}].GetForeground(); fg != color.Navy {
		t.Fatalf("dir row fg=%v want navy", fg)
	}

	if fg := s.styles[[2]int{0, 2}].GetForeground(); fg == color.Navy {
		t.Fatal("file row should not be navy")
	}

	// The selected row stays plain reverse video, not blue, even on a dir.
	m.cursor = 0
	draw(s, &m)

	if st := s.styles[[2]int{0, 0}]; !st.HasReverse() {
		t.Fatal("selected dir row should be reverse video")
	}

	if fg := s.styles[[2]int{0, 0}].GetForeground(); fg == color.Navy {
		t.Fatal("selected dir row should not carry a navy foreground")
	}
}

func TestStartSync(t *testing.T) {
	s := newFakeScreen(10, 10)
	startSync(s, writeFakeVim(t, `exit 0`), "S", "/some/path")

	select {
	case ev := <-s.q:
		sd, ok := ev.(*syncDoneEvent)
		if !ok || sd.path != "/some/path" {
			t.Fatalf("unexpected event %#v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no syncDoneEvent posted")
	}
}

func TestLoop(t *testing.T) {
	s := newFakeScreen(30, 10)
	m := newTestModel(t)
	m.vim = writeFakeVim(t, `exit 0`)

	s.q <- tcell.NewEventResize(30, 10)

	s.q <- ekey(tcell.KeyDown, "") // b_dir (dir)

	s.q <- ekey(tcell.KeyDown, "") // a_file.md -> startSync

	s.q <- &syncDoneEvent{at: time.Now(), path: "/x"}

	s.q <- tcell.NewEventMouse(0, 0, tcell.WheelUp, tcell.ModNone) // wheel

	s.q <- ekey(tcell.KeyRune, "z") // unknown

	s.q <- ekey(tcell.KeyRune, "q") // quit

	done := make(chan struct{})

	go func() { loop(s, m); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not return on q")
	}
}

func TestLoopReturnsOnClosedQueue(t *testing.T) {
	s := newFakeScreen(10, 5)
	close(s.q)

	done := make(chan struct{})

	go func() { loop(s, newTestModel(t)); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop should return when the event queue closes")
	}
}

func TestLoopAutoRefresh(t *testing.T) {
	s := newFakeScreen(20, 10)
	m := newTestModel(t) // refreshEvery 0: no real ticker, events driven manually

	fresh, err := buildTree(m.root.path, nil)
	if err != nil {
		t.Fatal(err)
	}

	s.q <- &refreshTickEvent{} // not refreshing -> spawn rebuild

	s.q <- &refreshTickEvent{} // refreshing -> guard skips

	s.q <- &refreshDoneEvent{root: fresh} // apply + draw

	s.q <- &refreshDoneEvent{root: nil} // failed rebuild -> tree kept

	s.q <- ekey(tcell.KeyRune, "q")

	done := make(chan struct{})

	go func() { loop(s, m); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not return on q")
	}
}

func TestRefreshTickerFires(t *testing.T) {
	s := newFakeScreen(20, 10)
	m := newTestModel(t)
	m.refreshEvery = 5 * time.Millisecond // real ticker -> tick -> rebuild -> apply

	done := make(chan struct{})

	go func() { loop(s, m); close(done) }()

	time.Sleep(80 * time.Millisecond) // let several ticks + rebuilds run

	s.q <- ekey(tcell.KeyRune, "q")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop with refresh ticker did not return on q")
	}
}

func TestRefreshWorkerBuildError(t *testing.T) {
	s := newFakeScreen(20, 10)
	m := newTestModel(t)

	if err := os.Chmod(m.root.path, 0); err != nil {
		t.Skip(err)
	}

	defer func() { _ = os.Chmod(m.root.path, 0o755) }()

	s.q <- &refreshTickEvent{} // worker buildTree fails -> posts root:nil

	done := make(chan struct{})

	go func() { loop(s, m); close(done) }()

	time.Sleep(50 * time.Millisecond) // let the worker run and post

	s.q <- ekey(tcell.KeyRune, "q")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not return on q")
	}
}
