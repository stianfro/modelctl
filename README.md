# modelctl

Change OpenCode's default model, custom providers, and API tokens.

## Install

Linux and macOS, on Intel or ARM64:

```sh
curl -fsSL https://stianfro.github.io/modelctl/install.sh | sh
```

Or use Homebrew:

```sh
brew install stianfro/tap/modelctl
```

See [Get started](https://stianfro.github.io/modelctl/) for PATH setup and other install options.

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

Model discovery needs the selected OpenCode binary on your `PATH`.
If discovery fails, modelctl shows configured model IDs with a warning.

## OpenCode versions

Run `modelctl` without a target. It checks the selected config and installed
binaries, including V2 installed as `opencode`.

When both versions are installed, the config format selects the matching version.
If the config does not identify a version, modelctl asks you to choose explicitly:

```sh
modelctl --target opencode
modelctl --target opencode2
```

`MODELCTL_TARGET` sets an override. Use `--target auto` to ignore that override.
Use `--opencode-bin PATH` to select a specific executable.

Both versions use the same global config directory. Use `--config PATH` for
separate files. Provider edits retain an existing V1 layout; new V2 configs use
`providers`, `package`, and `settings`. Files are not migrated.

For both versions, paste an API token into the hidden prompt or use `--stdin`.
V2 saves tokens through OpenCode's local API in its native credential store, not
in the config file or V1's `auth.json`.

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
nonzero exit code. A partial model list succeeds with a warning on stderr. Never pass a token as a command argument.

## Config and tokens

- Edits the global OpenCode config by default. Uses `opencode.jsonc` before
  `opencode.json` in `~/.config/opencode`, with support for `XDG_CONFIG_HOME`.
- Use `--config PATH` to select another file. `current` reads that file, not a
  running session. Model changes do not affect existing sessions or agent overrides.
- For V1, saves tokens in `~/.local/share/opencode/auth.json`, with support for
  `XDG_DATA_HOME`. Tokens are plaintext with owner-only permissions (`0600`).
  `--config` does not change the token path. OAuth credentials are not replaced.
- Config and environment overrides can take priority over saved settings.
  JSONC comments and unrelated fields are kept.

Tested with OpenCode 1.18.32 and OpenCode 2 preview `0.0.0-beta-19059`.
Run `modelctl --help` or `modelctl COMMAND --help` for command options.
