#!/usr/bin/env python3
"""Isolated installer regression tests; no Go, Google account, or network needed."""
import hashlib
import io
import os
import shutil
import ssl
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
import platform
import subprocess
import tarfile
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('install.sh').resolve()
VERSION = 'v0.0.0-test.1'
SYSTEM = {'Darwin': 'darwin', 'Linux': 'linux'}[platform.system()]
ARCH = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}[platform.machine()]
ASSET = f'searchprobe_{VERSION[1:]}_{SYSTEM}_{ARCH}.tar.gz'
NOTICE = ('THIRD_PARTY_NOTICES.txt', b'fixture notices', tarfile.REGTYPE)

class Installer(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='searchprobe-test-')
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.home = self.root / 'user home'
        self.home.mkdir()
        self.bundle = self.root / 'bundle'
        self.bundle.mkdir()
        self.bin = self.home / '.local/share/searchprobe/bin/gsc'
        self.env = {'HOME': str(self.home), 'PATH': '/usr/bin:/bin:/usr/sbin:/sbin'}
        self.make_bundle()

    def make_bundle(self, members=None):
        # If the installer ever executes the binary, it fails visibly.
        members = members or [('gsc', b'#!/bin/sh\nexit 99\n', tarfile.REGTYPE), ('LICENSE', b'MIT', tarfile.REGTYPE), NOTICE]
        archive = self.bundle / ASSET
        with tarfile.open(archive, 'w:gz', format=tarfile.USTAR_FORMAT) as tar:
            for name, data, kind in members:
                item = tarfile.TarInfo(name)
                item.type, item.mode = kind, 0o755
                if kind in (tarfile.SYMTYPE, tarfile.LNKTYPE):
                    item.linkname = '/tmp/searchprobe-escape'
                    tar.addfile(item)
                else:
                    item.size = len(data)
                    tar.addfile(item, io.BytesIO(data))
        (self.bundle / 'VERSION').write_text(VERSION + '\n')
        (self.bundle / 'checksums.txt').write_text(hashlib.sha256(archive.read_bytes()).hexdigest() + '  ' + ASSET + '\n')

    def run_install(self, args=None, ok=True):
        result = subprocess.run(['/bin/sh', str(SCRIPT), *(args if args is not None else ['--from-dir', str(self.bundle)])], env=self.env, text=True, capture_output=True)
        self.assertEqual(result.returncode == 0, ok, result.stdout + result.stderr)
        self.assertFalse(list(self.home.glob('.local/share/searchprobe/bin/.install.*')))
        self.assertFalse((self.home / '.local/share/searchprobe/.install-lock').exists())
        return result.stdout + result.stderr

    def test_handoff_to_setup_without_agent_writes(self):
        output = self.run_install()
        self.assertIn('gsc setup', output)
        self.assertFalse((self.home / '.claude').exists())
        self.assertFalse((self.home / '.agents').exists())
        self.assertFalse((self.home / '.codex').exists())
        self.assertFalse((self.home / '.config/gsc').exists())

    def test_install_update_uninstall_preserves_credentials(self):
        creds = self.home / '.config/gsc/credentials.json'
        creds.parent.mkdir(parents=True)
        creds.write_text('test sentinel, not credentials')
        self.run_install()
        self.assertEqual(self.bin.stat().st_mode & 0o777, 0o755)
        self.make_bundle([('gsc', b'updated', tarfile.REGTYPE), ('LICENSE', b'MIT', tarfile.REGTYPE), NOTICE])
        self.run_install()
        self.assertEqual(self.bin.read_bytes(), b'updated')
        self.run_install(['--uninstall'])
        self.assertFalse(self.bin.exists())
        self.assertTrue(creds.exists())

    def test_ghostscript_path_collision_never_executes_or_modifies_it(self):
        other = self.home / 'other'
        other.mkdir()
        ghost = other / 'gsc'
        ghost.write_text('#!/bin/sh\ntouch "' + str(self.home / 'executed') + '"\n')
        ghost.chmod(0o755)
        self.env['PATH'] = str(other) + ':' + self.env['PATH']
        before = ghost.read_bytes()
        self.assertIn('PATH collision', self.run_install())
        self.assertEqual(ghost.read_bytes(), before)
        self.assertFalse((self.home / 'executed').exists())

    def test_unmanaged_and_modified_binary_refused(self):
        self.bin.parent.mkdir(parents=True)
        self.bin.write_text('Ghostscript')
        self.run_install(ok=False)
        self.run_install(['--uninstall'], ok=False)
        self.assertEqual(self.bin.read_text(), 'Ghostscript')
        self.bin.unlink()
        self.run_install()
        self.bin.write_text('modified')
        self.run_install(ok=False)
        self.assertEqual(self.bin.read_text(), 'modified')

    def test_checksum_failures_preserve_old_binary(self):
        self.run_install()
        before = self.bin.read_bytes()
        sums = self.bundle / 'checksums.txt'
        original = sums.read_text()
        for value in ['0' * 64 + '  ' + ASSET + '\n', original * 2, 'malformed\n']:
            sums.write_text(value)
            self.run_install(ok=False)
            self.assertEqual(self.bin.read_bytes(), before)

    def test_archive_attacks(self):
        for members in [
            [('gsc', b'', tarfile.SYMTYPE), ('LICENSE', b'MIT', tarfile.REGTYPE), NOTICE],
            [('gsc', b'', tarfile.LNKTYPE), ('LICENSE', b'MIT', tarfile.REGTYPE), NOTICE],
            [('../gsc', b'bad', tarfile.REGTYPE), ('LICENSE', b'MIT', tarfile.REGTYPE), NOTICE],
            [('/tmp/gsc', b'bad', tarfile.REGTYPE), ('LICENSE', b'MIT', tarfile.REGTYPE), NOTICE],
            [('gsc', b'one', tarfile.REGTYPE), ('gsc', b'two', tarfile.REGTYPE), ('LICENSE', b'MIT', tarfile.REGTYPE), NOTICE],
            [('gsc', b'', tarfile.REGTYPE), ('LICENSE', b'MIT', tarfile.REGTYPE), NOTICE],
        ]:
            with self.subTest(members=members):
                self.make_bundle(members)
                self.run_install(ok=False)
                self.assertFalse(self.bin.exists())

    def test_symlink_directory_and_binary(self):
        (self.home / '.local').symlink_to(self.bundle, target_is_directory=True)
        self.run_install(ok=False)
        (self.home / '.local').unlink()
        self.bin.parent.mkdir(parents=True)
        self.bin.symlink_to(self.bundle / 'VERSION')
        self.run_install(ok=False)
        self.assertEqual((self.bundle / 'VERSION').read_text().strip(), VERSION)

    def test_dry_run_and_input_rejection(self):
        self.run_install(['--from-dir', str(self.bundle), '--dry-run'])
        self.assertFalse((self.home / '.local').exists())
        for args in [['--version'], ['--version', '../bad'], ['--version', VERSION, '--base-url', 'http://example.com'], ['--wat'], ['--uninstall', '--version', VERSION]]:
            self.run_install(args, ok=False)

    def test_permissions_and_symlink_receipt(self):
        local = self.home / '.local'
        local.mkdir(mode=0o777)
        local.chmod(0o777)
        self.run_install(ok=False)
        local.chmod(0o700)
        self.run_install()
        receipt = self.bin.parent / '.searchprobe-sha256'
        receipt.unlink()
        receipt.symlink_to(self.bundle / 'VERSION')
        self.run_install(ok=False)
        self.run_install(['--uninstall'], ok=False)
        self.assertTrue(self.bin.exists())

    def test_platform_selection(self):
        fake = self.root / 'tools'
        fake.mkdir()
        self.env['PATH'] = str(fake) + ':' + self.env['PATH']
        for system, arch, expected in [('Darwin', 'arm64', 'darwin_arm64'), ('Darwin', 'x86_64', 'darwin_amd64'), ('Linux', 'aarch64', 'linux_arm64'), ('Linux', 'x86_64', 'linux_amd64'), ('Windows', 'x86_64', None), ('Linux', 'riscv64', None)]:
            uname = fake / 'uname'
            uname.write_text(f'#!/bin/sh\ncase "$1" in -s) echo {system};; -m) echo {arch};; esac\n')
            uname.chmod(0o755)
            output = self.run_install(['--version', VERSION, '--dry-run'], ok=expected is not None)
            if expected:
                self.assertIn(expected, output)

    def test_https_fetch_contract_and_failure(self):
        fake = self.root / 'tools'
        fake.mkdir()
        curl = fake / 'curl'
        # Mock transport only. Real curl TLS/redirect behavior is checked separately.
        curl.write_text('''#!/bin/sh
printf '%s\\n' "$@" >> "$HOME/curl-args"
for arg do
 case "$arg" in https://*) url=$arg;; esac
 last=$arg
done
cp "$FIXTURE/${url##*/}" "$last"
''')
        curl.chmod(0o755)
        self.env.update(PATH=str(fake) + ':' + self.env['PATH'], FIXTURE=str(self.bundle))
        self.run_install(['--version', VERSION, '--base-url', 'https://example.test/releases'])
        args = (self.home / 'curl-args').read_text()
        self.assertIn('--proto-redir\n=https', args)
        self.assertIn('https://example.test/releases/' + VERSION + '/' + ASSET, args)
        before = self.bin.read_bytes()
        curl.write_text('#!/bin/sh\nexit 22\n')
        self.run_install(['--version', VERSION], ok=False)
        self.assertEqual(self.bin.read_bytes(), before)

    @unittest.skipUnless(shutil.which('curl') and shutil.which('openssl'), 'real TLS test needs curl and openssl')
    def test_real_https_and_downgrade_rejection(self):
        cert, key = self.root / 'cert.pem', self.root / 'key.pem'
        config = self.root / 'openssl.cnf'
        config.write_text('[req]\ndistinguished_name=dn\nx509_extensions=ext\n[dn]\nCN=localhost\n[ext]\nsubjectAltName=DNS:localhost\nbasicConstraints=critical,CA:TRUE\n')
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
                        '-keyout', str(key), '-out', str(cert), '-days', '1',
                        '-subj', '/CN=localhost', '-config', str(config)],
                       check=True, capture_output=True)
        bundle = self.bundle
        mode = ['ok']
        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                if mode[0] == 'redirect':
                    self.send_response(302)
                    self.send_header('Location', 'http://localhost:9/insecure')
                    self.end_headers()
                    return
                data = (bundle / self.path.rsplit('/', 1)[-1]).read_bytes()
                self.send_response(200)
                self.end_headers()
                self.wfile.write(data)
            def log_message(self, *args):
                pass
        server = HTTPServer(('127.0.0.1', 0), Handler)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cert, key)
        server.socket = context.wrap_socket(server.socket, server_side=True)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            self.env['CURL_CA_BUNDLE'] = str(cert)
            args = ['--version', VERSION, '--base-url', f'https://localhost:{server.server_port}']
            self.run_install(args)
            before = self.bin.read_bytes()
            mode[0] = 'redirect'
            self.assertIn('http', self.run_install(args, ok=False))
            self.assertEqual(self.bin.read_bytes(), before)
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

if __name__ == '__main__':
    unittest.main(verbosity=2)
