"""Test installed OpenCode versions with isolated config, data, and credentials."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
MODELCTL = str(ROOT / "bin/modelctl")


def run(args, env, cwd):
    result = subprocess.run(args, env=env, cwd=cwd, capture_output=True, text=True, timeout=45)
    if result.returncode:
        raise RuntimeError(f"{Path(args[0]).name} {args[1]} failed with exit {result.returncode}")
    return result.stdout


for target in ("opencode", "opencode2"):
    binary = shutil.which(target)
    if binary is None:
        raise RuntimeError(f"{target} is required for this smoke test")
    with tempfile.TemporaryDirectory(prefix="modelctl-smoke-") as tmp:
        env = {k: v for k, v in os.environ.items() if not k.startswith(("OPENCODE_", "MODELCTL_"))}
        env.update(HOME=tmp, OPENCODE_TEST_HOME=tmp, TMPDIR=tmp)
        for key in ("XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"):
            env[key] = str(Path(tmp) / key)
        config = str(Path(tmp) / "opencode.jsonc")
        cli = [MODELCTL, "--target", target, "--opencode-bin", binary, "--config", config]
        run(cli + ["provider", "set", "custom", "--base-url", "https://example.test/v1", "--model", "test-model"], env, tmp)
        run(cli + ["use", "custom/test-model"], env, tmp)
        assert json.loads(run(cli + ["current", "--json"], env, tmp))["model"] == "custom/test-model"
        listed = json.loads(run(cli + ["list", "--json"], env, tmp))
        assert "custom/test-model" in listed
        # The same installed binaries also work without an explicit target.
        auto = [MODELCTL, "--opencode-bin", binary, "--config", config]
        assert json.loads(run(auto + ["current", "--json"], env, tmp))["model"] == "custom/test-model"
        assert "custom/test-model" in json.loads(run(auto + ["list", "--json"], env, tmp))
        if target == "opencode2":
            # Ask the actual preview to decode the config. Its cold-start model
            # discovery can return an empty list; modelctl retains configured IDs.
            env["OPENCODE_CONFIG"] = config
            entries = json.loads(run([binary, "api", "--standalone", "get", "/api/config"], env, tmp))
            documents = [e["info"] for e in entries if e.get("path") == config and e.get("type") == "document"]
            assert documents, "V2 did not load the explicit config"
            provider = documents[-1]["providers"]["custom"]
            assert provider["package"] == "aisdk:@ai-sdk/openai-compatible"
            assert provider["settings"]["baseURL"] == "https://example.test/v1"
            assert documents[-1]["model"] == {"providerID": "custom", "model": "test-model"}
        print(f"PASS {target}: provider, current, use, list, auto detection (isolated files)")
