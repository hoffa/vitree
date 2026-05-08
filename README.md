# vitree

[![check](https://github.com/hoffa/vitree/actions/workflows/check.yml/badge.svg)](https://github.com/hoffa/vitree/actions/workflows/check.yml)

Interactive file tree that opens files in a running vim session.

## Install

```sh
go install github.com/hoffa/vitree@latest
```

## Usage

Start vim with `--servername`, then run `vitree` from the project root in another terminal. The selected file syncs to vim as you move.

Flags:

- `-server NAME` — pick a specific vim server (auto-detected if there's only one running)
- `-vim PATH` — vim binary to invoke (default `vim`)

## Keys

| key       | action                            |
|-----------|-----------------------------------|
| `j` / `k` | move; selected file syncs to vim  |
| `h` / `l` | collapse / expand                 |
| `enter`   | toggle dir, or open file          |
| `r`       | refresh tree from disk            |
| `q`       | quit                              |

## Requirements

A vim built with `+clientserver`. On macOS the system `vim` doesn't have it — `brew install vim`.
