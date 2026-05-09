package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"
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

func TestCollapseDropsCache(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "l") // expand a_dir, loads from disk
	cur := m.current()
	if !cur.loaded || len(cur.children) == 0 {
		t.Fatal("expand should load children")
	}
	m = send(m, "h") // collapse
	if cur.loaded || cur.children != nil {
		t.Fatal("collapse should drop cache for re-read on next expand")
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

func TestOpenInVimTimeout(t *testing.T) {
	vim := writeFakeVim(t, `sleep 2`)
	if err := openInVim(vim, "EDIT", "/tmp/x"); err == nil || !strings.Contains(err.Error(), "not responding") {
		t.Fatalf("expected timeout error, got %v", err)
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
	m.syncCurrent()
	if m.msg != "" {
		t.Fatalf("expected msg cleared on success, got %q", m.msg)
	}

	m.vim = "/no/such/binary"
	m.syncCurrent()
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
	m.root.walk(func(n *node) { count++ })
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
	if !strings.Contains(m.View(), "▼ \x1b[34ma_dir/") {
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
	m = send(m, "enter")
	if m.msg != "" {
		t.Fatalf("expected msg cleared, got %q", m.msg)
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
	cwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(cwd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	vim := writeFakeVim(t, `echo "EDIT"`)

	prev := runProgram
	defer func() { runProgram = prev }()
	called := false
	runProgram = func(m tea.Model) error { called = true; return nil }

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

func TestLoadNoops(t *testing.T) {
	root := mkTree(t)
	file, err := newNode(filepath.Join(root, "z_file.txt"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.load(); err != nil {
		t.Fatal(err)
	}

	dir, err := newNode(root, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir.loaded = true
	if err := dir.load(); err != nil {
		t.Fatal(err)
	}
	if len(dir.children) != 0 {
		t.Fatal("loaded dir should not reload")
	}
}

func TestRefreshFromDiskNoRoot(t *testing.T) {
	m := model{}
	if err := m.refreshFromDisk(); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshFromDiskSelectionDeleted(t *testing.T) {
	m := newTestModel(t)
	for i, n := range m.flat {
		if n.name == "z_file.txt" {
			m.cursor = i
			break
		}
	}
	if err := os.Remove(filepath.Join(m.root.path, "z_file.txt")); err != nil {
		t.Fatal(err)
	}
	if err := m.refreshFromDisk(); err != nil {
		t.Fatal(err)
	}
	if m.current() == nil {
		t.Fatal("cursor should clamp to an existing row")
	}
}

func TestRefreshFromDiskUpdatesTopLevel(t *testing.T) {
	m := newTestModel(t)
	root := m.root.path
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "z_file.txt")); err != nil {
		t.Fatal(err)
	}
	if err := m.refreshFromDisk(); err != nil {
		t.Fatal(err)
	}
	got := names(m.flat)
	if !contains(got, "new.txt") {
		t.Fatalf("refresh did not add new file: %v", got)
	}
	if contains(got, "z_file.txt") {
		t.Fatalf("refresh did not remove deleted file: %v", got)
	}
}

func TestRefreshFromDiskPreservesExpansionAndSelection(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "l", "j") // expand a_dir and select x.go
	selected := m.current().path
	if err := os.WriteFile(filepath.Join(m.root.path, "a_dir", "new.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.refreshFromDisk(); err != nil {
		t.Fatal(err)
	}
	if m.current() == nil || m.current().path != selected {
		t.Fatalf("selection changed: got=%v want=%s", m.current(), selected)
	}
	got := names(m.flat)
	if !contains(got, "new.go") {
		t.Fatalf("expanded dir did not refresh: %v", got)
	}
}

func TestUpdateHandlesFsMessages(t *testing.T) {
	m := newTestModel(t)
	m.watch = true
	m.msg = "error: watching files: stale"
	nm, cmd := m.Update(fsEventMsg{})
	m = nm.(model)
	if m.msg != "" {
		t.Fatalf("expected watcher error to clear after refresh, got %q", m.msg)
	}
	if cmd == nil {
		t.Fatal("fs event should restart watcher")
	}

	nm, cmd = m.Update(fsErrorMsg{err: os.ErrNotExist})
	m = nm.(model)
	if !strings.Contains(m.msg, "watching files") {
		t.Fatalf("expected watcher error message, got %q", m.msg)
	}
	if cmd == nil {
		t.Fatal("fs error should restart watcher")
	}
}

func TestInitStartsWatcherOnlyWhenEnabled(t *testing.T) {
	m := newTestModel(t)
	m.watch = false
	if cmd := m.Init(); cmd != nil {
		t.Fatal("watch disabled should not start watcher")
	}
	m.watch = true
	if cmd := m.Init(); cmd == nil {
		t.Fatal("watch enabled should start watcher")
	}
	m.root = nil
	if cmd := m.Init(); cmd != nil {
		t.Fatal("nil root should not start watcher")
	}
}

func TestAddRecursiveWatches(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"a", "a/b", ".hidden", ".hidden/child"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "plain-file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := addRecursiveWatches(w, root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-w.Events:
		if !strings.HasSuffix(event.Name, "file.txt") {
			t.Fatalf("unexpected event: %v", event)
		}
	case err := <-w.Errors:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watched dir event")
	}
}

func TestAddRecursiveWatchesErrors(t *testing.T) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := addRecursiveWatches(w, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected error for missing root")
	}
}

func TestWatchDirReturnsEvent(t *testing.T) {
	root := t.TempDir()
	cmd := watchDir(root)
	msgs := make(chan tea.Msg, 1)
	go func() { msgs <- cmd() }()
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(root, "created.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-msgs:
		if _, ok := msg.(fsEventMsg); !ok {
			t.Fatalf("got %T, want fsEventMsg", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watch event")
	}
}

func TestWatchDirReturnsErrorForMissingRoot(t *testing.T) {
	msg := watchDir(filepath.Join(t.TempDir(), "missing"))()
	if _, ok := msg.(fsErrorMsg); !ok {
		t.Fatalf("got %T, want fsErrorMsg", msg)
	}
}

func TestWaitForChange(t *testing.T) {
	events := make(chan fsnotify.Event, 2)
	errors := make(chan error)
	events <- fsnotify.Event{Name: ".hidden", Op: fsnotify.Create}
	events <- fsnotify.Event{Name: "visible", Op: fsnotify.Create}
	if _, ok := waitForChange(events, errors).(fsEventMsg); !ok {
		t.Fatal("visible event should return fsEventMsg")
	}

	events = make(chan fsnotify.Event)
	errors = make(chan error, 1)
	errors <- os.ErrPermission
	if _, ok := waitForChange(events, errors).(fsErrorMsg); !ok {
		t.Fatal("watcher error should return fsErrorMsg")
	}

	events = make(chan fsnotify.Event)
	errors = make(chan error)
	close(events)
	if _, ok := waitForChange(events, errors).(fsErrorMsg); !ok {
		t.Fatal("closed events should return fsErrorMsg")
	}

	events = make(chan fsnotify.Event)
	errors = make(chan error)
	close(errors)
	if _, ok := waitForChange(events, errors).(fsErrorMsg); !ok {
		t.Fatal("closed errors should return fsErrorMsg")
	}
}

func TestReloadExpandedBranches(t *testing.T) {
	m := newTestModel(t)
	if err := reloadExpanded(m.flat[0], map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if m.flat[0].loaded || m.flat[0].children != nil {
		t.Fatal("collapsed reload should clear cached children")
	}
	for _, n := range m.flat {
		if !n.isDir {
			if err := reloadExpanded(n, map[string]bool{}); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("no file found")
}

func TestShouldRefreshIgnoresHiddenFiles(t *testing.T) {
	if shouldRefresh(fsnotify.Event{Name: ".hidden", Op: fsnotify.Create}) {
		t.Fatal("hidden file event should not refresh")
	}
	if !shouldRefresh(fsnotify.Event{Name: "visible", Op: fsnotify.Create}) {
		t.Fatal("visible create event should refresh")
	}
	if !shouldRefresh(fsnotify.Event{Name: "visible", Op: fsnotify.Remove}) {
		t.Fatal("visible remove event should refresh")
	}
	if !shouldRefresh(fsnotify.Event{Name: "visible", Op: fsnotify.Rename}) {
		t.Fatal("visible rename event should refresh")
	}
	if !shouldRefresh(fsnotify.Event{Name: "visible", Op: fsnotify.Write}) {
		t.Fatal("visible write event should refresh")
	}
	if shouldRefresh(fsnotify.Event{Name: "visible", Op: fsnotify.Chmod}) {
		t.Fatal("chmod event should not refresh")
	}
}

func contains(xs []string, x string) bool {
	for _, got := range xs {
		if got == x {
			return true
		}
	}
	return false
}

func TestWindowSizeMsg(t *testing.T) {
	m := newTestModel(t)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if nm.(model).w != 120 || nm.(model).h != 40 {
		t.Fatal("window size not applied")
	}
}
