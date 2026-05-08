package main

import (
	"os"
	"path/filepath"
	"testing"
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
