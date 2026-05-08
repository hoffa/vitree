.PHONY: check fix build

build:
	go build -o vitree .

check:
	test -z "$$(gofmt -l .)"
	go vet .
	go test .

fix:
	gofmt -w .
	go mod tidy
