# Get started

**modelctl** changes your OpenCode model, provider, and API token.

## Install

For Linux and macOS, on Intel or ARM64.

::: code-group

```sh [Script]
curl -fsSL https://stianfro.github.io/modelctl/install.sh | sh
```

```sh [Homebrew]
brew install stianfro/tap/modelctl
```

```sh [Go]
go install github.com/stianfro/modelctl/cmd/modelctl@latest
```

:::

The script checks the download's SHA-256 checksum and installs to `~/.local/bin`.
If needed, add that directory to your shell's `PATH`:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Go installation requires Go 1.26 or newer and uses `$HOME/go/bin` by default.

## Run

```sh
modelctl
```

Use the arrow keys and Enter. Type to filter models. Press Escape to cancel.

For OpenCode 2, run `modelctl --target opencode2`.
See [Commands](./commands) for direct commands and config selection.

## Update

Run the installer again, or use `brew upgrade modelctl` for Homebrew installs.
