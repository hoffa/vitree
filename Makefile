.PHONY: check fix build test

build:
	go build -o vitree .

check: test
	test -z "$$(gofmt -l .)"
	go vet .
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run --default=all -D varnamelen -D paralleltest -D wrapcheck -D wsl -D noinlineerr -D goconst -D exhaustruct -D unparam -D nlreturn -D lll -D recvcheck -D mnd -D gosec -D cyclop -D gocognit -D funlen -D gochecknoglobals -D depguard -D err113 -D ireturn -D forcetypeassert -D gomodguard .

test:
	./test.sh

fix:
	gofmt -w .
	go mod tidy
