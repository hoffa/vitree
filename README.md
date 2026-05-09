# vitree

[![check](https://github.com/hoffa/vitree/actions/workflows/check.yml/badge.svg)](https://github.com/hoffa/vitree/actions/workflows/check.yml)

Vim file browser in a separate process.

<img width="1012" height="711" alt="스크린샷 2026-05-10 01 16 09" src="https://github.com/user-attachments/assets/23e92baa-c4a2-46f7-b010-492cbc70fdda" />

## Install

```sh
brew install vim
go install github.com/hoffa/vitree@latest
```

## Use

Start vim with a server name:

```sh
vim --servername vim
```

In another terminal, from the project root:

```sh
vitree
```

Press `?` inside vitree for keys and the connected server.
