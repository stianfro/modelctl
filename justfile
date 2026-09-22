set shell := ["bash", "-euo", "pipefail", "-c"]

default:
    @just --list

# Download the dependencies pinned in go.mod.
bootstrap:
    go mod download

# Add or update Go dependencies, then use just tidy.
deps +modules:
    go get {{modules}}

build:
    go build -o bin/ocswitch ./cmd/ocswitch

install:
    go install ./cmd/ocswitch

run *args:
    go run ./cmd/ocswitch {{args}}

fmt:
    go fmt ./...

lint:
    @files="$(gofmt -l cmd internal)"; if [[ -n "$files" ]]; then printf 'Run just fmt:\n%s\n' "$files"; exit 1; fi
    go vet ./...

test:
    go test -race ./...

tidy:
    go mod tidy

ci: lint test build
