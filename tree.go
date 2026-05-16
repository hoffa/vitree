package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// node is a single entry in the on-disk directory tree. It has no UI, vim, or
// model coupling — this file is the whole filesystem-tree concern.
type node struct {
	path     string
	name     string
	isDir    bool
	expanded bool
	loaded   bool
	depth    int
	children []*node
}

func newNode(path string, depth int) (*node, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}

	return &node{
		path:  path,
		name:  filepath.Base(path),
		isDir: info.IsDir(),
		depth: depth,
	}, nil
}

// walk visits n and every descendant, preorder, regardless of expansion.
func (n *node) walk(fn func(*node)) {
	fn(n)

	for _, c := range n.children {
		c.walk(fn)
	}
}

// flatten appends, preorder, every visible descendant of n: its children,
// recursing only into expanded directories. n itself is not included. The
// backing array of `into` is reused so callers can pass a trimmed slice.
func (n *node) flatten(into []*node) []*node {
	for _, c := range n.children {
		into = append(into, c)
		if c.isDir && c.expanded {
			into = c.flatten(into)
		}
	}

	return into
}

// expandedPaths is the set of directory paths currently expanded in n's tree.
func (n *node) expandedPaths() map[string]bool {
	expanded := map[string]bool{}

	n.walk(func(n *node) {
		if n.isDir && n.expanded {
			expanded[n.path] = true
		}
	})

	return expanded
}

// load reads n's directory once, populating children. Non-dirs and
// already-loaded nodes are no-ops.
func (n *node) load() error {
	if !n.isDir || n.loaded {
		return nil
	}

	entries, err := os.ReadDir(n.path)
	if err != nil {
		return err
	}

	n.children = childrenFrom(n, entries)
	n.loaded = true

	return nil
}

// childrenFrom builds parent's child nodes straight from the directory
// entries. It deliberately does NOT stat each entry: os.ReadDir already
// reported the name and type, and vitree needs nothing else — so a directory
// with N files costs zero extra syscalls instead of N. Sort keys are
// lowercased once (O(n)) rather than inside the comparator (O(n log n)).
func childrenFrom(parent *node, entries []os.DirEntry) []*node {
	s := dirsFirst{
		nodes: make([]*node, len(entries)),
		keys:  make([]string, len(entries)),
	}

	for i, e := range entries {
		s.nodes[i] = &node{
			path:  filepath.Join(parent.path, e.Name()),
			name:  e.Name(),
			isDir: e.IsDir(),
			depth: parent.depth + 1,
		}
		s.keys[i] = strings.ToLower(e.Name())
	}

	sort.Sort(&s)

	return s.nodes
}

// dirsFirst orders directories before files, then case-insensitively by name,
// using precomputed lowercase keys so no allocation happens per comparison.
type dirsFirst struct {
	nodes []*node
	keys  []string
}

func (s *dirsFirst) Len() int { return len(s.nodes) }

func (s *dirsFirst) Swap(i, j int) {
	s.nodes[i], s.nodes[j] = s.nodes[j], s.nodes[i]
	s.keys[i], s.keys[j] = s.keys[j], s.keys[i]
}

func (s *dirsFirst) Less(i, j int) bool {
	if s.nodes[i].isDir != s.nodes[j].isDir {
		return s.nodes[i].isDir
	}

	return s.keys[i] < s.keys[j]
}

// buildTree is the one way a tree is constructed: read rootPath, then
// recursively load and re-expand every directory in `expanded`. A subdir that
// fails to read is left collapsed rather than failing the whole tree.
func buildTree(rootPath string, expanded map[string]bool) (*node, error) {
	root, err := newNode(rootPath, 0)
	if err != nil {
		return nil, err
	}

	if !root.isDir {
		return nil, fmt.Errorf("not a directory: %s", rootPath)
	}

	root.expanded = true
	if err := root.load(); err != nil {
		return nil, err
	}

	var expand func(n *node)

	expand = func(n *node) {
		for _, c := range n.children {
			if c.isDir && expanded[c.path] {
				c.expanded = true
				_ = c.load()
				expand(c)
			}
		}
	}

	expand(root)

	return root, nil
}
