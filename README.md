# modelctl

Change OpenCode's default model, custom providers, and API tokens.

## Install

Requires Go 1.26 or newer. Supports Linux and macOS.

```sh
go install github.com/stianfro/modelctl/cmd/modelctl@latest
```

Add `$HOME/go/bin` to your `PATH`. To install from this checkout, run `just install`.

## Use

```sh
modelctl                         # Open the interactive menu
modelctl list                    # List available models
modelctl current                 # Show the configured default
modelctl use PROVIDER/MODEL       # Set the default model
modelctl token set PROVIDER       # Enter a token with hidden input
```

In the menu, use the arrow keys and Enter. Type to filter the model list.
In forms, Enter moves to the next field and saves on the last field.
Press Escape to cancel or `q` to quit from the menu.

Only `list` needs `opencode` on your `PATH`.

## Add a custom provider

Replace the URL and model ID with the values from your provider:

```sh
modelctl provider set custom \
  --base-url https://api.example.com/v1 \
  --model model-id
modelctl token set custom
modelctl use custom/model-id
```

Repeat `--model` to add more models. For an existing provider, supply only the
settings you want to change. New providers use `@ai-sdk/openai-compatible`.
Use `--npm PACKAGE` to select another SDK package.

## Use in scripts

```sh
modelctl current --json
modelctl list --json
modelctl token set PROVIDER --stdin < /path/to/token
```

Use `--json` for machine-readable results. Errors go to stderr and return a
nonzero exit code. Never pass a token as a command argument.

## Config and tokens

- Edits the global OpenCode config by default. Uses `opencode.jsonc` before
  `opencode.json` in `~/.config/opencode`, with support for `XDG_CONFIG_HOME`.
- Use `--config PATH` to select another file. `current` reads that file, not a
  running session. Model changes do not affect existing sessions or agent overrides.
- Saves tokens in `~/.local/share/opencode/auth.json`, with support for
  `XDG_DATA_HOME`. Tokens are plaintext with owner-only permissions (`0600`).
  `--config` does not change the token path. OAuth credentials are not replaced.
- Config and environment overrides can take priority over saved settings.
  JSONC comments and unrelated fields are kept.

Targets the OpenCode 1.18.18 CLI config format. Does not migrate V2 configs.
Run `modelctl --help` or `modelctl COMMAND --help` for command options.
