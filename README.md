# vitree

[![check](https://github.com/hoffa/vitree/actions/workflows/check.yml/badge.svg)](https://github.com/hoffa/vitree/actions/workflows/check.yml)
[![release](https://github.com/hoffa/vitree/actions/workflows/release.yml/badge.svg)](https://github.com/hoffa/vitree/actions/workflows/release.yml)

A Vim file browser running in a separate terminal

<img width="790" height="560" alt="demo" src="https://github.com/user-attachments/assets/60fcb5df-629a-4446-8569-32f3231c92df" />

## Why

In-editor file trees mean learning another set of bindings for resizing, splitting, and focus. `vitree` skips that — it's just a terminal you can put wherever you want.

## Features

- Fast async TUI
- Automatic tree refresh
- `.gitignore` and Git status marker support
- Mouse support
- Vim-like navigation
- ANSI colors

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

Files matched by `.gitignore` are hidden by default. Press `f` to cycle filter modes (default → changed only → show all), or `?` for the full key list.
