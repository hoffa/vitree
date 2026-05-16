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
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}

	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func update(m model, msg tea.Msg) (model, tea.Cmd) {
	nm, cmd := m.Update(msg)
	return nm.(model), cmd
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}

	_, ok := cmd().(tea.QuitMsg)

	return ok
}

func fileIndex(m model, name string) int {
	for i, n := range m.flat {
		if n.name == name {
			return i
		}
	}

	return -1
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

	want := []string{".hidden", "a_dir", "b_dir", ".dotfile", "a_file.md", "z_file.txt"}
	if got := names(n.children); strings.Join(got, ",") != strings.Join(want, ",") {
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

	n, err := newNode(bad, 0, nil)
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

func TestInitNil(t *testing.T) {
	if cmd := newTestModel(t).Init(); cmd != nil {
		t.Fatal("Init should return nil")
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

func TestWindowSizeRendersRows(t *testing.T) {
	m, _ := update(newTestModel(t), tea.WindowSizeMsg{Width: 40, Height: 10})

	if m.w != 40 || m.h != 10 {
		t.Fatalf("size not set: w=%d h=%d", m.w, m.h)
	}

	if len(m.rows) != len(m.flat) {
		t.Fatalf("rows=%d flat=%d", len(m.rows), len(m.flat))
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

func TestMoveUpDown(t *testing.T) {
	m := newTestModel(t)

	m, _ = update(m, key("up")) // at top, no-op
	if m.cursor != 0 {
		t.Fatalf("up at top should stay 0, got %d", m.cursor)
	}

	m, _ = update(m, key("down"))
	if m.cursor != 1 {
		t.Fatalf("down -> 1, got %d", m.cursor)
	}

	m.cursor = len(m.flat) - 1
	m, _ = update(m, key("down")) // at bottom, no-op

	if m.cursor != len(m.flat)-1 {
		t.Fatalf("down at bottom should not move, got %d", m.cursor)
	}

	m, _ = update(m, key("up"))
	if m.cursor != len(m.flat)-2 {
		t.Fatalf("up -> %d, got %d", len(m.flat)-2, m.cursor)
	}
}

func TestUnknownKeyNoop(t *testing.T) {
	m := newTestModel(t)

	if _, cmd := m.Update(key("x")); cmd != nil {
		t.Fatal("unknown key should be a no-op")
	}
}

func TestEnterTogglesDir(t *testing.T) {
	m := newTestModel(t) // cursor 0 = a_dir

	m, cmd := update(m, key("enter"))
	if !m.current().expanded || cmd != nil {
		t.Fatalf("enter should expand dir, no cmd: expanded=%v cmd=%v", m.current().expanded, cmd)
	}

	if !strings.Contains(strings.Join(names(m.flat), ","), "x.go") {
		t.Fatalf("expanded children missing: %v", names(m.flat))
	}

	m, _ = update(m, key("enter"))
	if m.current().expanded {
		t.Fatal("enter again should collapse")
	}
}

func TestEnterOpensFile(t *testing.T) {
	m := newTestModel(t)
	m.vim = writeFakeVim(t, `exit 0`)
	m.cursor = fileIndex(m, "a_file.md")

	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("enter on file should sync")
	}
}

func TestEnterNilNode(t *testing.T) {
	empty := model{root: &node{}, w: 80, h: 24}

	if _, cmd := empty.Update(key("enter")); cmd != nil {
		t.Fatal("enter with no current node should be nil")
	}
}

func TestEnterExpandLoadErrorIsSilent(t *testing.T) {
	m := newTestModel(t)
	bad := filepath.Join(m.root.path, "a_dir")

	if err := os.Chmod(bad, 0); err != nil {
		t.Skip(err)
	}

	defer func() { _ = os.Chmod(bad, 0o755) }()

	m, _ = update(m, key("enter")) // a_dir: load fails, still expands, no crash

	if !m.current().expanded || len(m.current().children) != 0 {
		t.Fatalf("expected expanded dir with no children: expanded=%v n=%d",
			m.current().expanded, len(m.current().children))
	}
}

func TestVimSyncCoalesces(t *testing.T) {
	m := newTestModel(t)
	m.vim = writeFakeVim(t, `exit 0`)

	aFile := fileIndex(m, "a_file.md")
	zFile := fileIndex(m, "z_file.txt")

	m.cursor = aFile

	cmd := m.syncCurrent()
	if cmd == nil || !m.syncing || m.pendingPath != "" {
		t.Fatalf("first sync should start: syncing=%v pending=%q cmd=%v", m.syncing, m.pendingPath, cmd)
	}

	active := m.activePath

	m.cursor = zFile
	if next := m.syncCurrent(); next != nil || m.pendingPath == "" || m.pendingPath == active {
		t.Fatalf("in-flight move should only set pending: next=%v pending=%q", next, m.pendingPath)
	}

	m.cursor = aFile // back to active path
	if next := m.syncCurrent(); next != nil || m.pendingPath != "" {
		t.Fatalf("return to active should clear pending: next=%v pending=%q", next, m.pendingPath)
	}

	m.cursor = zFile
	m.syncCurrent() // pending = z_file again

	m, fin := update(m, cmd()) // first completes -> follow-up for pending
	if fin == nil || !m.syncing {
		t.Fatalf("completion should start follow-up: syncing=%v fin=%v", m.syncing, fin)
	}

	m, _ = update(m, fin())
	if m.syncing || m.pendingPath != "" {
		t.Fatalf("queue should drain clean: syncing=%v pending=%q", m.syncing, m.pendingPath)
	}
}

func TestSyncCurrentOnDirClearsPending(t *testing.T) {
	m := newTestModel(t)
	m.pendingPath = "/stale"

	if cmd := m.syncCurrent(); cmd != nil { // cursor 0 = a_dir
		t.Fatal("dir selection should not sync")
	}

	if m.pendingPath != "" {
		t.Fatalf("pending should be cleared, got %q", m.pendingPath)
	}
}

func TestFinishSyncIgnoresError(t *testing.T) {
	m := newTestModel(t)
	m.cursor = fileIndex(m, "a_file.md")
	m.syncing = true
	m.activePath = m.current().path

	m, next := update(m, vimSyncDoneMsg{path: m.current().path, err: os.ErrPermission})

	if m.syncing || next != nil {
		t.Fatalf("completion should clear syncing, no follow-up: syncing=%v next=%v", m.syncing, next)
	}
}

func TestRefreshKeyPicksUpChanges(t *testing.T) {
	m := newTestModel(t)
	want := m.current().path

	if err := os.WriteFile(filepath.Join(m.root.path, "0_new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ = update(m, key("r"))

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

	m, _ = update(m, key("r")) // buildTree fails -> unchanged, no crash

	if got := strings.Join(names(m.flat), ","); got != before {
		t.Fatalf("failed refresh should leave tree unchanged: %q -> %q", before, got)
	}
}

func TestViewRendersRows(t *testing.T) {
	m := newTestModel(t)
	v := m.View()

	if !strings.Contains(v, "a_dir") || !strings.Contains(v, "z_file.txt") {
		t.Fatalf("view missing rows: %q", v)
	}

	if strings.Contains(v, "? help") {
		t.Fatal("status bar should be gone")
	}

	if (model{root: &node{}}).View() != "" {
		t.Fatal("empty tree should render empty")
	}
}

func TestRenderRowsMarkers(t *testing.T) {
	m := newTestModel(t) // a_dir, b_dir, a_file.md, z_file.txt

	if !strings.HasPrefix(m.rows[0], "+ a_dir/") {
		t.Fatalf("collapsed dir marker: %q", m.rows[0])
	}

	if !strings.HasPrefix(m.rows[2], "  a_file.md") {
		t.Fatalf("file marker (two spaces): %q", m.rows[2])
	}

	m, _ = update(m, key("enter")) // expand a_dir
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

func TestExpandedPaths(t *testing.T) {
	m := newTestModel(t)
	m, _ = update(m, key("enter")) // expand a_dir

	if !m.expandedPaths()[filepath.Join(m.root.path, "a_dir")] {
		t.Fatalf("expandedPaths missing a_dir: %v", m.expandedPaths())
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

func TestNewModel(t *testing.T) {
	dir := mkTree(t)
	vim := writeFakeVim(t, `echo "EDIT"`)

	if _, err := newModel(vim, "S", dir); err != nil {
		t.Fatalf("newModel: %v", err)
	}

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
