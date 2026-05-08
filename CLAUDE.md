# vitree

Tiny Go TUI that browses the current directory and forwards the highlighted file to a running vim session via `--servername` / `--remote-silent`.

## Workflow

After any code change, run `make check` before considering the task done. It runs `./test.sh` (tests + 80% coverage floor), `gofmt -l`, and `go vet`. CI runs the same target on push and PR, so a failing `make check` will fail CI.

If coverage drops below 80%, add tests rather than lowering the threshold — the floor is a project invariant.
