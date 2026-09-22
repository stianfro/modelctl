import hashlib
import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


class InstallerTests(unittest.TestCase):
    def run_installer(self, corrupt=False):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fixtures = root / 'fixtures'
            fixtures.mkdir()
            name = 'modelctl_0.1.0_linux_amd64.tar.gz'
            with tarfile.open(fixtures / name, 'w:gz') as archive:
                data = b'#!/bin/sh\necho modelctl 0.1.0\n'
                info = tarfile.TarInfo('modelctl')
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))
            sha = hashlib.sha256((fixtures / name).read_bytes()).hexdigest()
            (fixtures / 'SHA256SUMS').write_text(f'{"0" * 64 if corrupt else sha}  {name}\n')
            (fixtures / 'latest').write_text('{"tag_name": "v0.1.0"}')
            fake = root / 'bin'
            fake.mkdir()
            (fake / 'uname').write_text('#!/bin/sh\ncase "$1" in -s) echo Linux;; -m) echo x86_64;; esac\n')
            (fake / 'curl').write_text('#!/bin/sh\nwhile [ "$#" -gt 0 ]; do case "$1" in https:*) url=$1;; -o) shift; output=$1;; esac; shift; done\ncp "$FIXTURES/${url##*/}" "$output"\n')
            for path in fake.iterdir():
                path.chmod(0o755)
            target = root / 'install'
            target.mkdir()
            (target / 'modelctl').write_text('old binary')
            result = subprocess.run(['sh', str(ROOT / 'site/public/install.sh')], text=True, capture_output=True,
                                    env={**os.environ, 'PATH': f'{fake}:{os.environ["PATH"]}',
                                         'FIXTURES': str(fixtures), 'MODELCTL_VERSION': '', 'MODELCTL_INSTALL_DIR': str(target)})
            if corrupt:
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('checksum mismatch', result.stderr)
                self.assertEqual((target / 'modelctl').read_text(), 'old binary')
            else:
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(subprocess.check_output([str(target / 'modelctl')], text=True), 'modelctl 0.1.0\n')
            self.assertEqual(sorted(p.name for p in target.iterdir()), ['modelctl'])

    def test_install_latest(self):
        self.run_installer()

    def test_checksum_failure_preserves_existing_binary(self):
        self.run_installer(corrupt=True)

    def test_invalid_version(self):
        result = subprocess.run(['sh', str(ROOT / 'site/public/install.sh'), '--version', '../bad'], capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b'expected a stable version', result.stderr)
