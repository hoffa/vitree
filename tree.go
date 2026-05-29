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

func childrenFrom(parent *node, entries []os.DirEntry) []*node {
	sort.Slice(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di
		}

		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	var out []*node

	for _, e := range entries {
		abs := filepath.Join(parent.path, e.Name())

		c, err := newNode(abs, parent.depth+1)
		if err != nil {
			continue
		}

		out = append(out, c)
	}

	return out
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
