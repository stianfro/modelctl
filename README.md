# ocswitch

A small CLI for OpenCode model and API-token changes. Use commands in scripts,
or run `ocswitch` to open a Bubble Tea menu.

No profiles, daemon, or separate credential store. No inference requests are
sent to test a token or select a model.

## Install

Requires Go 1.26 or newer and `just` for development. Go can download the pinned
toolchain automatically. Linux and macOS are supported; Windows is not a target
for this version.

From this checkout:

```sh
just bootstrap
just install
```

Add Go's binary directory to `PATH` if needed (normally `$HOME/go/bin`). For a
local build instead, use `just build` and run `./bin/ocswitch`.
To install into an existing `~/.local/bin` on your `PATH`, use
`GOBIN="$HOME/.local/bin" just install`.

Only model listing needs `opencode` on `PATH`. Direct config and token commands
work without it.

## Quick start

```sh
ocswitch                         # interactive menu
ocswitch list                    # one available provider/model ID per line
ocswitch current                 # default in the selected config file
ocswitch use PROVIDER/MODEL       # set the default for new work
ocswitch token set PROVIDER      # hidden token input
```

For Intility Inference, use your deployment URL and exact model ID:

```sh
ocswitch provider set intility \
  --base-url https://DEPLOYMENT-llm.ai.intility.app/v1 \
  --model MODEL
ocswitch token set intility
ocswitch use intility/MODEL
```

The provider command does not select the model or save a token. These are
separate operations. Repeat `--model` to add more models. Existing model options
and unrelated config fields are kept.

For an existing custom provider, supply only the settings you want to change:

```sh
ocswitch provider set intility --model ANOTHER_MODEL
ocswitch provider set intility --base-url https://NEW-DEPLOYMENT-llm.ai.intility.app/v1
```

New custom providers need both `--base-url` and at least one `--model`.
They use `@ai-sdk/openai-compatible` by default. Use `--npm PACKAGE` to select a
different AI SDK package, and `--name NAME` to set a display name. Use the full
API base URL, including `/v1` when required. URLs with embedded credentials,
query parameters, or fragments are rejected.

In the menu, use the arrow keys and Enter. Type in the model list to filter it.
In forms, Enter moves to the next field and saves on the last field. Tab and
Shift+Tab move between fields. Escape cancels a form without saving. Quit from
the menu with `q`, Escape, or Ctrl+C.

## Scripts and AI agents

Commands with all required arguments never open the menu. A token prompt needs
a terminal; scripts must use `--stdin`. Never put a token in a command argument.

```sh
ocswitch list --json
ocswitch current --json
ocswitch use PROVIDER/MODEL --json
your-secret-command | ocswitch token set PROVIDER --stdin --json
```

JSON is written to stdout. Errors and warnings go to stderr. `list --json`
returns a sorted array of model-reference strings. `current --json` returns
`config_path` and `model`; an empty model means it is not set in that file.
Write commands return `action`, `path`, `changed`, and the applicable `model` or
`provider`. A token result can also contain `warnings`. No result contains the
token. Repeating a change reports `changed: false` if no update is needed.

Exit codes: `0` for success, `1` for an operation failure, `2` for command usage
errors, and `130` for a canceled token prompt or interrupted operation.
Running without a command and without a terminal returns a usage error.

## Files and limits

- By default, edit `$XDG_CONFIG_HOME/opencode/opencode.jsonc`, or
  `~/.config/opencode/opencode.jsonc` when XDG is unset. Use `opencode.json` if
  it exists and `opencode.jsonc` does not. If neither exists, create `.jsonc`.
- Use `--config PATH` to edit a specific file. There is no automatic project
  selection. Paths are relative to the current directory unless absolute.
- `current` reads **that file**, not the merged OpenCode config or a live session.
- `list` runs `opencode models` in the current directory, with a 20-second
  timeout. With `--config`, it passes the path as `OPENCODE_CONFIG`. OpenCode
  still applies its own project and environment overrides. Listing may load
  OpenCode plugins and refresh its model catalog; do this only in trusted projects.
- Model selection checks the `provider/model` format, not catalog membership.
  Model IDs can contain more slashes. Root model `#variant` suffixes are not
  supported. Existing sessions, agent models, and project overrides are unchanged.
- API tokens always go to `$XDG_DATA_HOME/opencode/auth.json`, or
  `~/.local/share/opencode/auth.json`. `--config` does not change this path.
  This file contains **plaintext secrets**, with owner-only permissions (`0600`).
  It is not a system keychain.
- Tokens can have one final newline from a pipe. Empty tokens, whitespace inside
  tokens, and tokens larger than 64 KiB are rejected. OAuth and other non-API
  credentials cannot be replaced by this tool.
- A selected config's `provider.PROVIDER.options.apiKey` can override the saved
  token. The tool warns but does not remove that setting. Other config files or
  OpenCode processes may also supply credentials. If `OPENCODE_AUTH_CONTENT` is
  set, token writes are refused. Saving a token does not check that it is valid.
- JSONC comments and unrelated fields are kept, but changed files can be
  reformatted. Malformed documents and duplicate keys are rejected. Symlinked
  files are refused; use `--config` with the real file path instead.
- Writes use temporary files and atomic replacement. Persistent
  `.ocswitch.lock` files prevent concurrent writes by this tool. Detected edits
  from other programs cause an error rather than an overwrite. OpenCode and
  editors do not share these locks; avoid simultaneous edits.

This first version targets the OpenCode **1.18.18 CLI config layout**:
`model`, singular `provider`, `options`, `npm`, and `auth.json`. It does not
migrate V2 configs, edit OpenCode's database, change `small_model`, manage OAuth,
or remove providers. Unknown fields are preserved, not translated.

## Development

```sh
just fmt
just ci           # formatting check, go vet, race-enabled tests, binary build
just run --help
```

Tests use temporary config directories and fake OpenCode executables. They do
not use live credentials or make inference requests. Go uses its standard shared
module and build caches.

The separate built-in Intility provider request is tracked in
[intility/blurite#547](https://github.com/intility/blurite/issues/547).
