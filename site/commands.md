# Commands

```sh
modelctl list                  # List available models
modelctl current               # Show the configured default
modelctl use PROVIDER/MODEL    # Set the default model
modelctl token set PROVIDER    # Enter a token with hidden input
```

Model discovery needs the selected OpenCode binary on your `PATH`.
If discovery fails, configured model IDs are shown with a warning.
Changes apply to new work, not existing sessions or agent overrides.

## OpenCode 2

```sh
modelctl --target opencode2
modelctl --target opencode2 use PROVIDER/MODEL
```

V1 is the default. Set `MODELCTL_TARGET=opencode2` to use V2 by default.
If V2 is named `opencode`, add `--opencode-bin opencode`.
Both versions share the global config directory. Use `--config PATH` for separate files.
Existing V1 files keep their format; modelctl does not migrate configs.

V2 token actions open OpenCode's login flow. `--stdin` and `--json` are V1-only
for `token set`.

## Custom providers

Use your provider's API URL and model ID:

```sh
modelctl provider set custom \
  --base-url https://api.example.com/v1 \
  --model model-id
modelctl token set custom
modelctl use custom/model-id
```

## Scripts

```sh
modelctl current --json
modelctl token set PROVIDER --stdin < /path/to/token
```

Never pass a token as a command argument.

## Config

By default, modelctl edits your global OpenCode config. Use `--config PATH`
to select another file. V1 tokens stay in OpenCode's global `auth.json`, stored
as plaintext with owner-only permissions. OAuth credentials are not replaced.

Run `modelctl --help` for all options.
