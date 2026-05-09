# vitree

[![check](https://github.com/hoffa/vitree/actions/workflows/check.yml/badge.svg)](https://github.com/hoffa/vitree/actions/workflows/check.yml)

Interactive file tree that opens files in a running vim session.

## Install

```sh
brew install vim
go install github.com/hoffa/vitree@latest
```

## Use

```sh
vim --servername VIM    # in one terminal
vitree                  # in another, in the project root
```

Press `?` inside vitree for keys and the connected server.
