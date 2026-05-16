package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

	r, err := newNode(root, 0, nil)
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

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}

	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func send(m model, keys ...string) model {
	for _, k := range keys {
		nm, _ := m.Update(key(k))
		m = nm.(model)
	}

	return m
}

func drain(m model, cmd tea.Cmd) model {
	if cmd == nil {
		return m
	}

	nm, _ := m.Update(cmd())

	return nm.(model)
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}

	_, ok := cmd().(tea.QuitMsg)

	return ok
}

func TestNewNodeError(t *testing.T) {
	if _, err := newNode(filepath.Join(t.TempDir(), "missing"), 0, nil); err == nil {
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

	n, err := newNode(root, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := n.load(); err != nil {
		t.Fatal(err)
	}

	got := names(n.children)

	want := []string{".hidden", "a_dir", "b_dir", ".dotfile", "a_file.md", "z_file.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("children=%v want=%v", got, want)
	}
}

func TestLoadNonDirAndAlreadyLoaded(t *testing.T) {
	root := mkTree(t)

	f, err := newNode(filepath.Join(root, "z_file.txt"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := f.load(); err != nil || f.loaded {
		t.Fatalf("non-dir load: err=%v loaded=%v", err, f.loaded)
	}

	d, err := newNode(root, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := d.load(); err != nil {
		t.Fatal(err)
	}

	d.children = nil // second load must be a no-op (loaded already true)

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

	n, err := newNode(bad, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := n.load(); err == nil {
		t.Fatal("expected read error")
	}
}

func TestClamp(t *testing.T) {
	cases := []struct{ v, lo, hi, want int }{
		{5, 0, 10, 5},
		{-1, 0, 10, 0},
		{11, 0, 10, 10},
	}
	for _, c := range cases {
		if got := clamp(c.v, c.lo, c.hi); got != c.want {
			t.Fatalf("clamp(%d,%d,%d)=%d want %d", c.v, c.lo, c.hi, got, c.want)
		}
	}
}

func TestInitNil(t *testing.T) {
	if cmd := newTestModel(t).Init(); cmd != nil {
		t.Fatal("Init should return nil (no background refresh)")
	}
}

func TestRebuildFlatRespectsExpansion(t *testing.T) {
	m := newTestModel(t)

	if got := names(m.flat); len(got) != 4 {
		t.Fatalf("collapsed top-level: got %v", got)
	}

	var aDir *node

	for _, c := range m.root.children {
		if c.name == "a_dir" {
			aDir = c
		}
	}

	if aDir == nil {
		t.Fatal("a_dir not found")
	}

	if err := aDir.load(); err != nil {
		t.Fatal(err)
	}

	aDir.expanded = true

	m.rebuildFlat()

	want := []string{"a_dir", "x.go", "b_dir", "a_file.md", "z_file.txt"}
	if got := names(m.flat); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expanded got=%v want=%v", got, want)
	}
}

func TestWindowSizeRendersRows(t *testing.T) {
	m := newTestModel(t)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m = nm.(model)

	if m.w != 40 || m.h != 10 {
		t.Fatalf("size not set: w=%d h=%d", m.w, m.h)
	}

	if len(m.rows) != len(m.flat) {
		t.Fatalf("rows=%d flat=%d", len(m.rows), len(m.flat))
	}
}

func TestMoveBounds(t *testing.T) {
	m := newTestModel(t)

	if m = send(m, "up", "k"); m.cursor != 0 {
		t.Fatalf("cursor should stay at 0, got %d", m.cursor)
	}

	m = send(m, "down")
	if m.cursor != 1 {
		t.Fatalf("down -> 1, got %d", m.cursor)
	}

	m = send(m, "j")
	if m.cursor != 2 {
		t.Fatalf("j -> 2, got %d", m.cursor)
	}

	m.cursor = len(m.flat) - 1
	if m = send(m, "down"); m.cursor != len(m.flat)-1 {
		t.Fatalf("down at end should not move, got %d", m.cursor)
	}

	m = send(m, "up")
	if m.cursor != len(m.flat)-2 {
		t.Fatalf("up -> %d, got %d", len(m.flat)-2, m.cursor)
	}
}

func TestVimSyncCoalesces(t *testing.T) {
	m := newTestModel(t)
	m.vim = writeFakeVim(t, `exit 0`)

	// flat (dirs first): a_dir, b_dir, a_file.md, z_file.txt
	m = send(m, "j") // b_dir (dir, no sync)

	nm, cmd := m.Update(key("j")) // a_file.md (first file)
	m = nm.(model)

	if cmd == nil || !m.syncing || m.pendingPath != "" {
		t.Fatalf("first file sync should start: syncing=%v pending=%q cmd=%v", m.syncing, m.pendingPath, cmd)
	}

	active := m.activePath

	nm, next := m.Update(key("j")) // a_file.md, while syncing -> pending
	m = nm.(model)

	if next != nil || m.pendingPath == "" || m.pendingPath == active {
		t.Fatalf("second move should only set pending: next=%v pending=%q", next, m.pendingPath)
	}

	// Returning to the active path while it is in flight clears pending.
	nm, next = m.Update(key("k")) // back to z_file.txt (== active)
	m = nm.(model)

	if next != nil || m.pendingPath != "" {
		t.Fatalf("return to active should clear pending: next=%v pending=%q", next, m.pendingPath)
	}

	m = send(m, "j") // pending = a_file.md again

	nm, fin := m.Update(cmd()) // first sync completes -> follow-up for pending
	m = nm.(model)

	if fin == nil || !m.syncing {
		t.Fatalf("completion should start follow-up sync: syncing=%v fin=%v", m.syncing, fin)
	}

	m = drain(m, fin)
	if m.syncing || m.pendingPath != "" || m.err != "" {
		t.Fatalf("queue should drain clean: syncing=%v pending=%q err=%q", m.syncing, m.pendingPath, m.err)
	}
}

func TestSyncCurrentOnDirClearsPending(t *testing.T) {
	m := newTestModel(t)
	m.pendingPath = "/stale"

	if cmd := m.syncCurrent(); cmd != nil { // cursor on b_dir
		t.Fatal("dir selection should not sync")
	}

	if m.pendingPath != "" {
		t.Fatalf("pending should be cleared, got %q", m.pendingPath)
	}
}

func TestFinishSyncErrorAndStale(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "j", "j") // z_file.txt
	cur := m.current().path
	m.syncing = true
	m.activePath = cur

	nm, _ := m.Update(vimSyncDoneMsg{path: cur, err: os.ErrPermission})
	m = nm.(model)

	if !strings.HasPrefix(m.err, "error:") {
		t.Fatalf("error should surface, got %q", m.err)
	}

	// Stale completion (cursor moved away) must not overwrite the message.
	m.err = "keep"
	m.cursor = 0
	m.syncing = true

	nm, _ = m.Update(vimSyncDoneMsg{path: cur, err: os.ErrNotExist})
	m = nm.(model)

	if m.err != "keep" {
		t.Fatalf("stale error changed message: %q", m.err)
	}
}

func TestQuit(t *testing.T) {
	m := newTestModel(t)

	if _, cmd := m.Update(key("q")); !isQuit(cmd) {
		t.Fatal("q should quit")
	}

	if _, cmd := m.Update(key("ctrl+c")); !isQuit(cmd) {
		t.Fatal("ctrl+c should quit")
	}
}

func TestHelpToggle(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "?")

	if !m.help {
		t.Fatal("? should open help")
	}

	if v := m.View(); !strings.Contains(v, "keys") || !strings.Contains(v, "vim server") {
		t.Fatalf("help view missing content: %q", v)
	}

	m = send(m, "j") // any key closes
	if m.help {
		t.Fatal("any key should close help")
	}

	m.help = true

	_, cmd := m.Update(key("ctrl+c"))
	if !isQuit(cmd) {
		t.Fatal("ctrl+c in help should quit")
	}
}

func TestRefreshKeyPicksUpChanges(t *testing.T) {
	m := newTestModel(t)

	want := m.current().path

	if err := os.WriteFile(filepath.Join(m.root.path, "0_new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = send(m, "r")

	if !strings.Contains(strings.Join(names(m.flat), ","), "0_new.txt") {
		t.Fatalf("refresh missed new file: %v", names(m.flat))
	}

	if got := m.current().path; got != want {
		t.Fatalf("cursor not preserved: got %q want %q", got, want)
	}
}

func TestRefreshErrorSetsMessage(t *testing.T) {
	m := newTestModel(t)

	if err := os.Chmod(m.root.path, 0); err != nil {
		t.Skip(err)
	}

	defer func() { _ = os.Chmod(m.root.path, 0o755) }()

	m = send(m, "r")
	if !strings.HasPrefix(m.err, "error:") {
		t.Fatalf("refresh error should set message, got %q", m.err)
	}
}

func TestLeftCollapseAndParent(t *testing.T) {
	m := newTestModel(t) // flat: a_dir, b_dir, a_file.md, z_file.txt

	// a_dir is at cursor 0. Expand it, descend into its child.
	m = send(m, "l") // expand a_dir

	m = send(m, "j") // x.go (child)
	if m.current().name != "x.go" {
		t.Fatalf("expected x.go, got %q", m.current().name)
	}

	m = send(m, "h") // child -> parent (a_dir)
	if m.current().name != "a_dir" {
		t.Fatalf("h should jump to parent, got %q", m.current().name)
	}

	m = send(m, "h") // expanded dir -> collapse
	if m.current().name != "a_dir" || m.current().expanded {
		t.Fatalf("h should collapse a_dir: name=%q expanded=%v", m.current().name, m.current().expanded)
	}

	// Top-level file has no eligible parent -> no-op.
	m = newTestModel(t)
	m.cursor = len(m.flat) - 1 // a_file.md
	before := m.cursor
	m = send(m, "h")

	if m.cursor != before {
		t.Fatalf("h on top-level file should not move: %d -> %d", before, m.cursor)
	}
}

func TestRightExpand(t *testing.T) {
	m := newTestModel(t)

	m = send(m, "l") // a_dir expand (cursor 0)
	if !m.current().expanded {
		t.Fatal("l should expand dir")
	}

	if !strings.Contains(strings.Join(names(m.flat), ","), "x.go") {
		t.Fatalf("expanded dir children missing: %v", names(m.flat))
	}

	// l on a file is a no-op.
	m.cursor = len(m.flat) - 1 // a_file.md
	before := names(m.flat)
	m = send(m, "l")

	if strings.Join(names(m.flat), ",") != strings.Join(before, ",") {
		t.Fatal("l on file should do nothing")
	}
}

func TestRightLoadError(t *testing.T) {
	m := newTestModel(t)
	bad := filepath.Join(m.root.path, "a_dir")

	if err := os.Chmod(bad, 0); err != nil {
		t.Skip(err)
	}

	defer func() { _ = os.Chmod(bad, 0o755) }()

	m = send(m, "l") // a_dir is cursor 0; expand -> ReadDir error

	if !strings.HasPrefix(m.err, "error:") {
		t.Fatalf("expected load error message, got %q", m.err)
	}
}

func TestEnter(t *testing.T) {
	m := newTestModel(t)
	m.vim = writeFakeVim(t, `exit 0`)

	// Enter on a collapsed dir expands it.
	nm, cmd := m.Update(key("enter")) // b_dir
	m = nm.(model)

	if !m.current().expanded || cmd != nil {
		t.Fatalf("enter on dir should expand, no cmd: expanded=%v cmd=%v", m.current().expanded, cmd)
	}

	// Enter again collapses.
	m = send(m, "enter")
	if m.current().expanded {
		t.Fatal("enter on expanded dir should collapse")
	}

	// Enter on a file syncs.
	m.cursor = len(m.flat) - 1 // a_file.md
	_, cmd = m.Update(key("enter"))

	if cmd == nil {
		t.Fatal("enter on file should sync")
	}

	// cur == nil path.
	empty := model{root: &node{}, w: 80, h: 24}
	if _, c := empty.Update(key("enter")); c != nil {
		t.Fatal("enter with no current node should be nil")
	}
}

func TestViewBasics(t *testing.T) {
	m := newTestModel(t)
	v := m.View()

	if !strings.Contains(v, "b_dir") || !strings.Contains(v, "? help") {
		t.Fatalf("view missing rows/status: %q", v)
	}

	if !strings.Contains(v, filepath.Base(m.root.path)) {
		t.Fatal("status should show root path")
	}

	m.err = "error: boom"
	if !strings.Contains(m.View(), "error: boom") {
		t.Fatal("view should show error message")
	}
}

func TestViewTruncatesLongPath(t *testing.T) {
	m := newTestModel(t)
	m.root.path = "/" + strings.Repeat("a", 300)

	if !strings.Contains(m.View(), "? help") {
		t.Fatal("long path should still leave room for help hint")
	}
}

func TestRenderRowsMarkers(t *testing.T) {
	m := newTestModel(t) // collapsed: a_dir, b_dir, a_file.md, z_file.txt

	if !strings.HasPrefix(m.rows[0], "+ a_dir/") {
		t.Fatalf("collapsed dir marker: %q", m.rows[0])
	}

	if !strings.HasPrefix(m.rows[2], "  a_file.md") {
		t.Fatalf("file marker (two spaces): %q", m.rows[2])
	}

	m = send(m, "l") // expand a_dir (cursor 0)
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
			t.Fatalf("row not truncated to width: %q", r)
		}
	}
}

func TestTruncateLeft(t *testing.T) {
	if got := truncateLeft("abc", 5); got != "abc" {
		t.Fatalf("short changed: %q", got)
	}

	if got := truncateLeft("abcdef", 0); got != "" {
		t.Fatalf("non-positive width should be empty: %q", got)
	}

	if got := truncateLeft("abcdefgh", 5); got != "defgh" {
		t.Fatalf("truncateLeft should keep the last 5 runes with no ellipsis, got %q", got)
	}
}

func TestTruncateRight(t *testing.T) {
	if got := truncateRight("abc", 5); got != "abc" {
		t.Fatalf("short changed: %q", got)
	}

	if got := truncateRight("abcdef", 3); got != "abc" {
		t.Fatalf("truncateRight=%q", got)
	}
}

func TestMaxRows(t *testing.T) {
	m := newTestModel(t)
	m.h = 10

	if got := m.maxRows(); got != 9 {
		t.Fatalf("maxRows=%d want 9", got)
	}

	m.h = 0
	if got := m.maxRows(); got != len(m.flat) {
		t.Fatalf("maxRows with h=0 should be len(flat)=%d, got %d", len(m.flat), got)
	}
}

func TestEnsureVisible(t *testing.T) {
	m := newTestModel(t)
	m.h = 3 // maxRows = 2

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

	// Empty flat + tiny height -> mr<=0 path.
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

func TestExpandedPaths(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "l") // expand a_dir (cursor 0)

	ep := m.expandedPaths()
	if !ep[filepath.Join(m.root.path, "a_dir")] {
		t.Fatalf("expandedPaths missing a_dir: %v", ep)
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

func TestBuildTreeRootReadError(t *testing.T) {
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

func TestRestoreCursor(t *testing.T) {
	m := newTestModel(t)
	m.cursor = 2

	m.restoreCursor("") // no-op

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

func TestNewModel(t *testing.T) {
	dir := mkTree(t)
	vim := writeFakeVim(t, `echo "EDIT"`)

	if _, err := newModel(vim, "S", dir); err != nil {
		t.Fatalf("newModel: %v", err)
	}

	// server auto-detected via fake vim.
	if _, err := newModel(vim, "", dir); err != nil {
		t.Fatalf("newModel detect: %v", err)
	}

	if _, err := newModel(writeFakeVim(t, `echo ""`), "", dir); err == nil {
		t.Fatal("expected detect error (no servers)")
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
	runProgram = func(_ tea.Model) error { called = true; return nil }

	if err := run([]string{"-vim", vim, "-server", "S"}); err != nil || !called {
		t.Fatalf("run: err=%v called=%v", err, called)
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

	if _, err := detectVimServer(writeFakeVim(t, `echo ""`)); err == nil {
		t.Fatal("expected error for no servers")
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

func TestSyncVimCmd(t *testing.T) {
	cmd := syncVimCmd(writeFakeVim(t, `exit 0`), "S", "/tmp/x")

	msg, ok := cmd().(vimSyncDoneMsg)
	if !ok || msg.err != nil || msg.path != "/tmp/x" {
		t.Fatalf("syncVimCmd msg=%+v ok=%v", msg, ok)
	}
}
