# vitree

[![check](https://github.com/hoffa/vitree/actions/workflows/check.yml/badge.svg)](https://github.com/hoffa/vitree/actions/workflows/check.yml)

Vim file browser in a separate process.

<img width="1012" height="711" alt="스크린샷 2026-05-10 01 16 09" src="https://github.com/user-attachments/assets/23e92baa-c4a2-46f7-b010-492cbc70fdda" />

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

Press `?` inside vitree for help.
