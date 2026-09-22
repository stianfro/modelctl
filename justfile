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
    go build -o bin/modelctl ./cmd/modelctl

install:
    go install ./cmd/modelctl

run *args:
    go run ./cmd/modelctl {{args}}

fmt:
    go fmt ./...

lint:
    @files="$(gofmt -l cmd internal)"; if [[ -n "$files" ]]; then printf 'Run just fmt:\n%s\n' "$files"; exit 1; fi
    go vet ./...

test:
    go test -race ./...

tidy:
    go mod tidy

docs-lock:
    npm --prefix site install --package-lock-only

docs-install:
    npm --prefix site ci

docs-build: docs-install
    npm --prefix site run build

docs-test: docs-build
    npm --prefix site audit --audit-level=high
    test -s site/.vitepress/dist/index.html
    test -s site/.vitepress/dist/commands.html
    cmp site/public/install.sh site/.vitepress/dist/install.sh

docs-dev: docs-install
    npm --prefix site run dev -- --host 127.0.0.1

docs-preview:
    npm --prefix site run preview -- --host 127.0.0.1

distribution-test:
    shellcheck site/public/install.sh
    python3 -m unittest discover -s tests -p 'test_*.py' -v

# Build all release archives, checksums, and a Homebrew formula.
release version:
    python3 scripts/release.py {{quote(version)}}

yaml:
    @for file in .github/workflows/*.yml; do yq eval '.' "$file" >/dev/null; done

ci: lint test build

# Optional integration check. Requires both installed OpenCode binaries.
smoke-opencode: build
    python3 scripts/smoke-opencode.py
