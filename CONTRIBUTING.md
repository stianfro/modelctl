# Development

Run `just ci distribution-test docs-test yaml` before pushing changes.
Use `just docs-dev` to preview the docs.

## Release

1. Push a tag such as `v0.1.0`.
2. Wait for the Release workflow. It publishes Linux and macOS archives, checksums, and a Homebrew formula.
3. Download `modelctl.rb` from the release. Copy it to `Formula/modelctl.rb` in `stianfro/homebrew-tap`.
4. Open a tap pull request. Check the formula and test installation before merging.

For a local build, run `just release v0.1.0`. Files go in `dist/v0.1.0/`.
