# vitree

[![check](https://github.com/hoffa/vitree/actions/workflows/check.yml/badge.svg)](https://github.com/hoffa/vitree/actions/workflows/check.yml)
[![release](https://github.com/hoffa/vitree/actions/workflows/release.yml/badge.svg)](https://github.com/hoffa/vitree/actions/workflows/release.yml)

A Vim file browser in a separate terminal.

<img width="790" height="560" alt="demo" src="https://github.com/user-attachments/assets/60fcb5df-629a-4446-8569-32f3231c92df" />

## Why

In-editor file trees mean learning another set of bindings for resizing, splitting, and focus. `vitree` skips that — it's just a terminal you can put wherever you want.

## Features

- Moving the selection forwards the highlighted file to a running Vim server
- Async TUI with no scroll lag
- Vim-style (`hjkl`), arrow, and mouse navigation
- Background auto-refresh (configurable)
- Dims `.gitignore`d files and `.git`

## Install

```sh
brew install hoffa/tap/vitree
```

Or with Go:

```sh
go install github.com/hoffa/vitree@latest
```

Or download a [prebuilt binary](https://github.com/hoffa/vitree/releases/latest).

## Usage

Start Vim with a server name:

```sh
vim --servername vim
```

> [!NOTE]
>
> You need Vim compiled with `+clientserver`:
>
> ```bash
> brew install vim
> ```

In another terminal, run:

```sh
vitree
```

Moving the selection onto a file opens it in the Vim server; directories
expand and collapse in place. `.gitignore`d files and `.git` are dimmed, not
hidden.

### Keys

| Key | Action |
| --- | --- |
| `j` / `k`, `↓` / `↑` | move selection |
| `l` / `→` | expand directory, or open file |
| `h` / `←` | collapse directory, or go to parent |
| `Enter` | toggle directory / open file |
| `r` | refresh now |
| `q` / `Ctrl-C` | quit |

The mouse works too: wheel scrolls the selection, left-click selects and
opens.

### Options

| Flag | Default | Description |
| --- | --- | --- |
| `-server` | auto-detected | Vim `--servername` to send files to |
| `-vim` | `vim` | Vim binary to invoke (e.g. `mvim`, `gvim`) |
| `-refresh` | `2s` | auto-refresh interval; `0` disables |
| `-version` | | print version and exit |
