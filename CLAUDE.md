# vitree

Tiny Go TUI that browses the current directory and forwards the highlighted file to a running vim session via `--servername` / `--remote-silent`.

## Workflow

After any code change, run `make check` before considering the task done. It runs `./test.sh` (tests + 95% coverage floor), `gofmt -l`, `go vet`, and `golangci-lint run`. CI runs the same target on push and PR, so a failing `make check` will fail CI.

If `make check` flags fixable issues (formatting, simple lint autofixes, stale `go.mod`), run `make fix` to apply them — it runs `gofmt -w`, `go mod tidy`, and `golangci-lint run --fix`.

If coverage drops below 95%, add tests rather than lowering the threshold — the floor is a project invariant.
