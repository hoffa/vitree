# vitree

[![check](https://github.com/hoffa/vitree/actions/workflows/check.yml/badge.svg)](https://github.com/hoffa/vitree/actions/workflows/check.yml)

Interactive file tree that opens files in a running vim session via `--servername` / `--remote-silent`.

## Install

```sh
go install github.com/hoffa/vitree@latest
```

## Usage

Start vim with a server name, then run vitree from the directory you want to browse:

```sh
vim --servername VIM    # in one terminal
vitree                  # in another, in the project root
```

Flags:

- `-server NAME` — vim servername to target (default `VIM`)
- `-vim PATH` — vim binary to invoke (default `vim`; use `mvim`/`gvim` if your `vim` lacks `+clientserver`)

## Keys

| key       | action                            |
|-----------|-----------------------------------|
| `j` / `k` | move; selected file syncs to vim  |
| `h` / `l` | collapse / expand                 |
| `enter`   | toggle dir, or open file          |
| `r`       | refresh tree from disk            |
| `q`       | quit                              |

## Requirements

A vim built with `+clientserver`. On macOS the system `vim` usually isn't — install MacVim (`brew install macvim`) and use `mvim --servername VIM` plus `vitree -vim mvim`.
