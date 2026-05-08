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
	m = send(m, "l", "r")
	if m.msg != "refreshed" {
		t.Fatalf("msg=%q", m.msg)
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
	out := m.View()
	if !strings.Contains(out, "a_dir/") {
		t.Fatalf("view missing a_dir: %q", out)
	}
	if !strings.Contains(out, "↑/↓ move") {
		t.Fatal("view missing help line")
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
	m.syncCurrent()
	if !strings.HasPrefix(m.msg, "opened ") {
		t.Fatalf("expected opened msg, got %q", m.msg)
	}

	m.vim = "/no/such/binary"
	m.syncCurrent()
	if !strings.HasPrefix(m.msg, "vim error") {
		t.Fatalf("expected vim error msg, got %q", m.msg)
	}
}

func TestInit(t *testing.T) {
	m := newTestModel(t)
	if cmd := m.Init(); cmd != nil {
		t.Fatal("Init should return nil")
	}
}

func TestViewFitsHeight(t *testing.T) {
	m := newTestModel(t)
	m.h = 5
	out := m.View()
	if !strings.Contains(out, "↑/↓ move") {
		t.Fatal("help line missing in tight height")
	}
	m.h = 100
	m.msg = "hello"
	out = m.View()
	if !strings.Contains(out, "hello") {
		t.Fatal("msg missing")
	}
}

func TestWindowSizeMsg(t *testing.T) {
	m := newTestModel(t)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if nm.(model).w != 120 || nm.(model).h != 40 {
		t.Fatal("window size not applied")
	}
}
