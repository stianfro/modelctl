# Commands

```sh
modelctl list                  # List available models
modelctl current               # Show the configured default
modelctl use PROVIDER/MODEL    # Set the default model
modelctl token set PROVIDER    # Enter a token with hidden input
```

Model listing needs `opencode` on your `PATH`.
Changes apply to new work, not existing sessions or agent overrides.

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
to select another file. Tokens stay in OpenCode's global `auth.json`, stored
as plaintext with owner-only permissions. OAuth credentials are not replaced.

Run `modelctl --help` for all options.
