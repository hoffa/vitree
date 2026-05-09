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
		".hidden/secret",
		".dotfile",
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

func TestLoadSortsDirsFirstAndSkipsHidden(t *testing.T) {
	root := mkTree(t)

	n, err := newNode(root, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := n.load(); err != nil {
		t.Fatal(err)
	}

	got := names(n.children)

	want := []string{"a_dir", "b_dir", "a_file.md", "z_file.txt"}
	if len(got) != len(want) {
		t.Fatalf("children=%v want=%v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("at %d got=%q want=%q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestRebuildFlatRespectsExpansion(t *testing.T) {
	root := mkTree(t)

	r, err := newNode(root, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := r.load(); err != nil {
		t.Fatal(err)
	}

	r.expanded = true

	m := &model{root: r}
	m.rebuildFlat()

	if got := names(m.flat); len(got) != 4 {
		t.Fatalf("collapsed children top-level: got %v", got)
	}

	var aDir *node

	for _, c := range r.children {
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

	got := names(m.flat)

	want := []string{"a_dir", "x.go", "b_dir", "a_file.md", "z_file.txt"}
	if len(got) != len(want) {
		t.Fatalf("expanded got=%v want=%v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("at %d got=%q want=%q", i, got[i], want[i])
		}
	}
}

func TestOpenInVimNoServer(t *testing.T) {
	if err := openInVim("vim", "", "/tmp/x"); err == nil {
		t.Fatal("expected error for empty server")
	}
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
	m := model{root: r, w: 80, h: 24}
	m.rebuildFlat()

	return m
}

func key(s string) tea.KeyMsg {
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}

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
	}

	return tea.KeyMsg{}
}

func send(m model, keys ...string) model {
	for _, k := range keys {
		nm, _ := m.Update(key(k))
		m = nm.(model)
	}

	return m
}

func TestUpdateNavigation(t *testing.T) {
	m := newTestModel(t)
	if m.cursor != 0 {
		t.Fatalf("initial cursor=%d", m.cursor)
	}

	m = send(m, "j", "j")
	if m.cursor != 2 {
		t.Fatalf("after jj cursor=%d", m.cursor)
	}

	m = send(m, "k")
	if m.cursor != 1 {
		t.Fatalf("after k cursor=%d", m.cursor)
	}
}

func TestUpdateExpandCollapse(t *testing.T) {
	m := newTestModel(t)
	// cursor on "a_dir"
	before := len(m.flat)

	m = send(m, "l")
	if len(m.flat) <= before {
		t.Fatalf("expand did not add children: before=%d after=%d", before, len(m.flat))
	}

	m = send(m, "h")
	if len(m.flat) != before {
		t.Fatalf("collapse did not restore: got=%d want=%d", len(m.flat), before)
	}
}

func TestUpdateEnterTogglesDir(t *testing.T) {
	m := newTestModel(t)
	before := len(m.flat)

	m = send(m, "enter")
	if len(m.flat) <= before {
		t.Fatal("enter did not expand dir")
	}

	m = send(m, "enter")
	if len(m.flat) != before {
		t.Fatal("enter did not collapse dir")
	}
}

func TestUpdateRefresh(t *testing.T) {
	m := newTestModel(t)

	m = send(m, "l", "r") // expand a_dir, refresh
	if m.msg != "refreshed" {
		t.Fatalf("msg=%q", m.msg)
	}
	// expanded state preserved after refresh
	var aDir *node

	for _, n := range m.flat {
		if n.name == "a_dir" {
			aDir = n
			break
		}
	}

	if aDir == nil || !aDir.expanded {
		t.Fatal("refresh dropped the expanded state")
	}
}

func TestRefreshHandlesLoadError(t *testing.T) {
	dir := t.TempDir()
	r, _ := newNode(dir, 0, nil)
	_ = r.load()
	r.expanded = true
	m := model{root: r, w: 80, h: 24}
	m.rebuildFlat()

	if err := os.Chmod(dir, 0); err != nil {
		t.Skip(err)
	}

	defer func() { _ = os.Chmod(dir, 0o755) }()

	m.refresh()

	if m.msg == "refreshed" {
		t.Fatal("expected error msg, got refreshed")
	}
}

func TestCollapseKeepsCache(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "l") // expand a_dir

	cur := m.current()
	if !cur.loaded || len(cur.children) == 0 {
		t.Fatal("expand should load children")
	}

	m = send(m, "h") // collapse

	if !cur.loaded || cur.children == nil {
		t.Fatal("collapse should keep cache; refresh is the only way to drop it")
	}
}

func TestUpdateLeftJumpsToParent(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "l", "j") // expand a_dir, move into child
	parentBefore := m.cursor

	m = send(m, "h") // should jump to parent
	if m.cursor >= parentBefore {
		t.Fatalf("left did not move to parent: cursor=%d", m.cursor)
	}
}

func TestUpdateQuitsOnQ(t *testing.T) {
	m := newTestModel(t)

	_, cmd := m.Update(key("q"))
	if cmd == nil {
		t.Fatal("q should return tea.Quit cmd")
	}
}

func TestView(t *testing.T) {
	m := newTestModel(t)
	m.cursor = 0

	out := m.View()
	if !strings.Contains(out, "a_dir/") {
		t.Fatalf("view missing a_dir: %q", out)
	}

	if !strings.Contains(out, "? help") {
		t.Fatal("view missing help hint")
	}
}

func TestHelpToggle(t *testing.T) {
	m := newTestModel(t)
	m.server = "VIM"

	m = send(m, "?")
	if !m.help {
		t.Fatal("? did not enable help")
	}

	out := m.View()
	if !strings.Contains(out, "VIM") {
		t.Fatalf("help missing server: %q", out)
	}

	m = send(m, "j") // any key dismisses
	if m.help {
		t.Fatal("any key did not dismiss help")
	}
}

func TestHelpCtrlCQuits(t *testing.T) {
	m := newTestModel(t)
	m.help = true

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c in help should quit")
	}
}

func TestViewBottomMsgOverridesHelp(t *testing.T) {
	m := newTestModel(t)
	m.cursor = 0
	m.msg = "error: boom"

	out := m.View()
	if !strings.Contains(out, "error: boom") {
		t.Fatalf("missing error: %q", out)
	}
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

func TestDetectVimServer(t *testing.T) {
	one := writeFakeVim(t, `echo "EDIT"`)

	got, err := detectVimServer(one)
	if err != nil || got != "EDIT" {
		t.Fatalf("single: got=%q err=%v", got, err)
	}

	none := writeFakeVim(t, `echo ""`)
	if _, err := detectVimServer(none); err == nil {
		t.Fatal("expected error for no servers")
	}

	many := writeFakeVim(t, `echo "EDIT"; echo "OTHER"`)
	if _, err := detectVimServer(many); err == nil {
		t.Fatal("expected error for multiple servers")
	}

	if _, err := detectVimServer("/no/such/binary"); err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestOpenInVimSuccess(t *testing.T) {
	vim := writeFakeVim(t, `
if [ "$1" = "--serverlist" ]; then echo "EDIT"; exit 0; fi
exit 0
`)
	if err := openInVim(vim, "EDIT", "/tmp/x"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestOpenInVimRemoteFails(t *testing.T) {
	vim := writeFakeVim(t, `
if [ "$1" = "--serverlist" ]; then echo "EDIT"; exit 0; fi
echo "boom" >&2
exit 2
`)
	if err := openInVim(vim, "EDIT", "/tmp/x"); err == nil {
		t.Fatal("expected error from --remote-silent failure")
	}
}

func TestSyncCurrentOnDirIsNoop(t *testing.T) {
	m := newTestModel(t)
	m.msg = ""
	m.syncCurrent() // cursor on a dir

	if m.msg != "" {
		t.Fatalf("expected no msg for dir, got %q", m.msg)
	}
}

func TestSyncCurrentOnFile(t *testing.T) {
	m := newTestModel(t)
	m.vim = writeFakeVim(t, `exit 0`)

	m.server = "EDIT"
	for i, n := range m.flat {
		if !n.isDir {
			m.cursor = i
			break
		}
	}

	m.msg = "stale"

	cmd := m.syncCurrent()
	if cmd == nil {
		t.Fatal("expected sync command")
	}

	nm, next := m.Update(cmd())
	m = nm.(model)

	if next != nil {
		t.Fatal("unexpected follow-up command")
	}

	if m.msg != "" {
		t.Fatalf("expected msg cleared on success, got %q", m.msg)
	}

	m.vim = "/no/such/binary"
	cmd = m.syncCurrent()
	nm, _ = m.Update(cmd())

	m = nm.(model)
	if !strings.HasPrefix(m.msg, "error:") {
		t.Fatalf("expected error msg, got %q", m.msg)
	}
}

func TestUpdateNoopsOnEmptyTree(t *testing.T) {
	m := newTestModel(t)

	m.flat = nil
	for _, k := range []string{"left", "right", "enter", "h", "l"} {
		nm, _ := m.Update(key(k))
		m = nm.(model)
	}
}

func TestCurrentOutOfRange(t *testing.T) {
	m := newTestModel(t)

	m.cursor = -1
	if m.current() != nil {
		t.Fatal("expected nil for negative cursor")
	}

	m.cursor = 999
	if m.current() != nil {
		t.Fatal("expected nil for over-range cursor")
	}
}

func TestNodeWalk(t *testing.T) {
	m := newTestModel(t)
	count := 0

	m.root.walk(func(_ *node) { count++ })

	if count < 5 {
		t.Fatalf("expected to walk at least root + children, got %d", count)
	}
}

func TestClamp(t *testing.T) {
	if clamp(5, 0, 10) != 5 || clamp(-1, 0, 10) != 0 || clamp(99, 0, 10) != 10 {
		t.Fatal("clamp broken")
	}
}

func TestNewModel(t *testing.T) {
	dir := mkTree(t)
	vim := writeFakeVim(t, `echo "EDIT"`)

	m, err := newModel(vim, "", dir)
	if err != nil || m.server != "EDIT" {
		t.Fatalf("auto-detect: server=%q err=%v", m.server, err)
	}

	m, err = newModel(vim, "OTHER", dir)
	if err != nil || m.server != "OTHER" {
		t.Fatalf("explicit: server=%q err=%v", m.server, err)
	}

	if _, err := newModel("/no/such", "", dir); err == nil {
		t.Fatal("expected detect error")
	}

	if _, err := newModel(vim, "X", "/no/such/dir"); err == nil {
		t.Fatal("expected node error")
	}

	file := filepath.Join(dir, "z_file.txt")
	if _, err := newModel(vim, "X", file); err == nil {
		t.Fatal("expected non-dir error")
	}
}

func TestInit(t *testing.T) {
	m := newTestModel(t)
	if cmd := m.Init(); cmd != nil {
		t.Fatal("Init should return nil")
	}
}

func TestViewBottomMsg(t *testing.T) {
	m := newTestModel(t)
	m.cursor = 0

	m.msg = "hello"
	if !strings.Contains(m.View(), "hello") {
		t.Fatal("msg missing")
	}
}

func TestViewSmallHeight(t *testing.T) {
	m := newTestModel(t)
	m.h = 2
	_ = m.View()
}

func TestViewScrolls(t *testing.T) {
	m := newTestModel(t)
	m.h = 6

	m.cursor = len(m.flat) - 1
	if !strings.Contains(m.View(), m.flat[len(m.flat)-1].name) {
		t.Fatal("cursor row not visible after scroll")
	}
}

func TestViewWithExpandedDir(t *testing.T) {
	m := newTestModel(t)

	m = send(m, "l", "j") // expand a_dir, move off it
	if !strings.Contains(m.View(), "▼ a_dir/") {
		t.Fatal("expanded dir marker missing")
	}
}

func TestUpdateEnterOnFile(t *testing.T) {
	m := newTestModel(t)
	m.vim = writeFakeVim(t, `exit 0`)

	m.server = "EDIT"
	for i, n := range m.flat {
		if !n.isDir {
			m.cursor = i
			break
		}
	}

	m.msg = "stale"
	nm, cmd := m.Update(key("enter"))
	m = nm.(model)

	if cmd == nil {
		t.Fatal("enter on file should return sync command")
	}

	nm, _ = m.Update(cmd())

	m = nm.(model)
	if m.msg != "" {
		t.Fatalf("expected msg cleared, got %q", m.msg)
	}
}

func TestSyncCurrentOnDirReturnsNoCommand(t *testing.T) {
	m := newTestModel(t)

	m.msg = ""
	if cmd := m.syncCurrent(); cmd != nil {
		t.Fatal("dir selection should not return sync command")
	}

	if m.msg != "" {
		t.Fatalf("expected no msg for dir, got %q", m.msg)
	}
}

func TestCoalescesVimSyncs(t *testing.T) {
	m := newTestModel(t)
	m.vim = writeFakeVim(t, `exit 0`)
	m.server = "EDIT"

	// Move to the first file and start an async sync.
	nm, cmd := m.Update(key("j")) // b_dir, no sync
	m = nm.(model)

	if cmd != nil {
		t.Fatal("moving to dir should not sync")
	}

	nm, cmd = m.Update(key("j")) // a_file.md

	m = nm.(model)
	if cmd == nil || !m.syncing || m.pendingPath != "" {
		t.Fatalf("expected first file sync to start: syncing=%v pending=%q cmd=%v", m.syncing, m.pendingPath, cmd)
	}

	active := m.activePath

	// Move again while the first sync is in flight. This must not start a
	// second process; it should only remember the latest requested file.
	nm, next := m.Update(key("j")) // z_file.txt
	m = nm.(model)

	if next != nil {
		t.Fatal("second file movement while syncing should not start another command")
	}

	if !m.syncing || m.pendingPath == "" || m.pendingPath == active {
		t.Fatalf("expected pending latest path: syncing=%v active=%q pending=%q", m.syncing, active, m.pendingPath)
	}

	// Once the first sync completes, exactly one follow-up sync starts for the
	// latest pending path.
	nm, next = m.Update(cmd())

	m = nm.(model)
	if next == nil || !m.syncing || m.activePath != m.current().path {
		t.Fatalf("expected follow-up sync for current path: syncing=%v active=%q current=%v next=%v", m.syncing, m.activePath, m.current(), next)
	}

	nm, next = m.Update(next())

	m = nm.(model)
	if next != nil || m.syncing || m.pendingPath != "" || m.msg != "" {
		t.Fatalf("expected sync queue to drain cleanly: syncing=%v pending=%q msg=%q next=%v", m.syncing, m.pendingPath, m.msg, next)
	}
}

func TestMovingToDirClearsPendingSync(t *testing.T) {
	m := newTestModel(t)
	m.vim = writeFakeVim(t, `exit 0`)
	m.server = "EDIT"
	m = send(m, "j")              // b_dir
	nm, cmd := m.Update(key("j")) // a_file.md
	m = nm.(model)

	if cmd == nil {
		t.Fatal("expected first sync command")
	}

	nm, _ = m.Update(key("j")) // z_file.txt, pending

	m = nm.(model)
	if m.pendingPath == "" {
		t.Fatal("expected pending path")
	}

	nm, next := m.Update(key("k")) // a_file.md, active
	m = nm.(model)

	if next != nil {
		t.Fatal("returning to active path should not start a command")
	}

	nm, next = m.Update(key("k")) // b_dir
	m = nm.(model)

	if next != nil {
		t.Fatal("moving to dir should not start a command")
	}

	if m.pendingPath != "" {
		t.Fatalf("moving to dir should clear pending sync, got %q", m.pendingPath)
	}
}

func TestReturningToActiveSyncClearsPending(t *testing.T) {
	m := newTestModel(t)
	m.vim = writeFakeVim(t, `exit 0`)
	m.server = "EDIT"
	m = send(m, "j")              // b_dir
	nm, cmd := m.Update(key("j")) // a_file.md
	m = nm.(model)

	if cmd == nil {
		t.Fatal("expected first sync command")
	}

	active := m.activePath
	nm, _ = m.Update(key("j")) // z_file.txt, pending

	m = nm.(model)
	if m.pendingPath == "" {
		t.Fatal("expected pending path")
	}

	nm, next := m.Update(key("k")) // back to a_file.md, active path
	m = nm.(model)

	if next != nil {
		t.Fatal("returning to active path should not start a command")
	}

	if m.current().path != active || m.pendingPath != "" {
		t.Fatalf("expected pending path cleared when returning to active path: current=%q active=%q pending=%q", m.current().path, active, m.pendingPath)
	}
}

func TestStaleSyncErrorDoesNotOverwriteCurrentMessage(t *testing.T) {
	m := newTestModel(t)
	m.server = "EDIT"
	m = send(m, "j", "j") // a_file.md
	stale := m.current().path
	m.msg = "keep"
	m.cursor++ // z_file.txt
	nm, next := m.Update(vimSyncDoneMsg{path: stale, err: os.ErrNotExist})
	m = nm.(model)

	if next != nil {
		t.Fatal("unexpected follow-up command")
	}

	if m.msg != "keep" {
		t.Fatalf("stale error changed msg: %q", m.msg)
	}
}

func TestUpdateLoadErrorOnExpand(t *testing.T) {
	root := mkTree(t)
	r, _ := newNode(root, 0, nil)
	_ = r.load()
	r.expanded = true
	m := model{root: r, w: 80, h: 24}
	m.rebuildFlat()
	// pick the first dir, chmod it to break load
	for i, n := range m.flat {
		if n.isDir {
			m.cursor = i

			if err := os.Chmod(n.path, 0); err != nil {
				t.Skip(err)
			}
			defer func(p string) { _ = os.Chmod(p, 0o755) }(n.path)

			break
		}
	}

	m = send(m, "l")
	if m.msg == "" {
		t.Fatal("expected error msg from failed load")
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

	if err := run([]string{"-vim", vim}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if !called {
		t.Fatal("runProgram not invoked")
	}

	if err := run([]string{"-vim", "/no/such"}); err == nil {
		t.Fatal("expected error for missing vim")
	}

	if err := run([]string{"-bogus"}); err == nil {
		t.Fatal("expected flag parse error")
	}
}

func TestNewModelLoadError(t *testing.T) {
	dir := t.TempDir()

	vim := writeFakeVim(t, `echo "EDIT"`)
	if err := os.Chmod(dir, 0); err != nil {
		t.Skip(err)
	}
	defer func() { _ = os.Chmod(dir, 0o755) }()

	if _, err := newModel(vim, "X", dir); err == nil {
		t.Fatal("expected load error")
	}
}

func TestWindowSizeMsg(t *testing.T) {
	m := newTestModel(t)

	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if nm.(model).w != 120 || nm.(model).h != 40 {
		t.Fatal("window size not applied")
	}
}
