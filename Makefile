.PHONY: check fix build test

build:
	go build -o vitree .

check: test
	test -z "$$(gofmt -l .)"
	go vet .

test:
	./test.sh

fix:
	gofmt -w .
	go mod tidy
