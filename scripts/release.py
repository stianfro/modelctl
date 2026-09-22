"""Build release archives and the Homebrew formula. Run with just release vX.Y.Z."""
import gzip
import hashlib
import io
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile


def build(tag):
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", tag):
        raise ValueError("expected a stable version: vX.Y.Z")
    version = tag[1:]
    root = Path(__file__).resolve().parent.parent
    output = root / "dist" / tag
    output.mkdir(parents=True, exist_ok=True)
    checksums = {}
    for system in ("darwin", "linux"):
        for arch in ("amd64", "arm64"):
            binary = output / "modelctl"
            subprocess.run(
                ["go", "build", "-trimpath", "-buildvcs=false", "-ldflags",
                 f"-s -w -X main.version={version}", "-o", str(binary), "./cmd/modelctl"],
                cwd=root, check=True,
                env={**os.environ, "CGO_ENABLED": "0", "GOOS": system, "GOARCH": arch},
            )
            name = f"modelctl_{version}_{system}_{arch}.tar.gz"
            with (output / name).open("wb") as file:
                with gzip.GzipFile(filename="", fileobj=file, mode="wb", mtime=0) as compressed:
                    with tarfile.open(fileobj=compressed, mode="w") as archive:
                        for path in (binary, root / "LICENSE"):
                            data = path.read_bytes()
                            info = tarfile.TarInfo(path.name)
                            info.size = len(data)
                            info.mode = 0o755 if path == binary else 0o644
                            archive.addfile(info, io.BytesIO(data))
            checksums[name] = hashlib.sha256((output / name).read_bytes()).hexdigest()
            binary.unlink()
    (output / "SHA256SUMS").write_text("".join(f"{sha}  {name}\n" for name, sha in sorted(checksums.items())))
    formula = ['class Modelctl < Formula', '  desc "Manage OpenCode models and API tokens"',
               '  homepage "https://stianfro.github.io/modelctl/"', f'  version "{version}"', '  license "MIT"', '']
    for system, brew_system in (("darwin", "macos"), ("linux", "linux")):
        formula.append(f"  on_{brew_system} do")
        for arch, brew_arch in (("arm64", "arm"), ("amd64", "intel")):
            name = f"modelctl_{version}_{system}_{arch}.tar.gz"
            formula.extend([f"    on_{brew_arch} do", f'      url "https://github.com/stianfro/modelctl/releases/download/{tag}/{name}"',
                            f'      sha256 "{checksums[name]}"', '    end'])
        formula.extend(['  end', ''])
    formula.extend(['  def install', '    bin.install "modelctl"', '  end', '', '  test do',
                    '    assert_match version.to_s, shell_output("#{bin}/modelctl --version")', '  end', 'end', ''])
    (output / "modelctl.rb").write_text("\n".join(formula))
    print(output)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("usage: release.py vX.Y.Z")
    build(sys.argv[1])
