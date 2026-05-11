# vitree

[![check](https://github.com/hoffa/vitree/actions/workflows/check.yml/badge.svg)](https://github.com/hoffa/vitree/actions/workflows/check.yml)

Vim file browser in a separate process.

<img width="1029" height="717" alt="스크린샷 2026-05-10 21 28 37" src="https://github.com/user-attachments/assets/1a2c15ab-ac99-4e58-a382-d1ea766f9664" />

## Install

```sh
go install github.com/hoffa/vitree@latest
```

Or download a [prebuilt binary](https://github.com/hoffa/vitree/releases/latest).

> [!NOTE]
>
> You also need Vim compiled with `+clientserver`. On macOS, you can install Vim using:
>
> ```bash
> brew install vim
> ```

## Usage

Start Vim with a server name:

```sh
vim --servername vim
```

In another terminal, run:

```sh
vitree
```

Files matched by `.gitignore` are hidden by default. Press `i` to toggle them, or `?` for the full key list.
