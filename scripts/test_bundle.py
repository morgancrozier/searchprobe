#!/usr/bin/env python3
"""Smoke-test built release assets in an empty HOME with only OS utilities."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile

bundle = Path(sys.argv[1]).resolve()
root = Path(__file__).resolve().parent.parent
version_text = (bundle / 'VERSION').read_text().strip()
expected_assets = {
    *(f'searchprobe_{version_text[1:]}_{system}_{arch}.tar.gz'
      for system in ('darwin', 'linux') for arch in ('arm64', 'amd64')),
    'VERSION', 'THIRD_PARTY_NOTICES.txt', 'install.sh', 'SKILL.md',
    'GOOGLE_SETUP.md', 'START_HERE.md',
}
actual_assets = {path.name for path in bundle.iterdir()}
assert actual_assets == expected_assets | {'checksums.txt'}, actual_assets
assert all(path.is_file() and not path.is_symlink() for path in bundle.iterdir())
checksums = {}
for line in (bundle / 'checksums.txt').read_text().splitlines():
    digest, name = line.split('  ', 1)
    assert name not in checksums and len(digest) == 64
    checksums[name] = digest
assert set(checksums) == expected_assets, checksums.keys()
for name, digest in checksums.items():
    assert hashlib.sha256((bundle / name).read_bytes()).hexdigest() == digest, name
for archive in bundle.glob('*.tar.gz'):
    with tarfile.open(archive, 'r:gz') as tar:
        assert tar.getnames() == ['gsc', 'LICENSE', 'THIRD_PARTY_NOTICES.txt']
        assert all(member.isfile() for member in tar.getmembers()), archive.name
        assert tar.getmember('gsc').mode == 0o755, archive.name
        assert tar.extractfile('LICENSE').read() == (root / 'LICENSE').read_bytes(), archive.name
        assert tar.extractfile('THIRD_PARTY_NOTICES.txt').read() == (bundle / 'THIRD_PARTY_NOTICES.txt').read_bytes(), archive.name
        binary_data = tar.extractfile('gsc').read()
        assert version_text.encode('ascii') + b'\0' in binary_data, archive.name
        # Validate target headers even when this host cannot execute the binary.
        if '_darwin_' in archive.name:
            assert binary_data[:4] == b'\xcf\xfa\xed\xfe', archive.name
            cpu = int.from_bytes(binary_data[4:8], 'little')
            assert cpu == (0x100000c if '_arm64.' in archive.name else 0x1000007), archive.name
        else:
            assert binary_data[:6] == b'\x7fELF\x02\x01', archive.name
            machine = int.from_bytes(binary_data[18:20], 'little')
            assert machine == (183 if '_arm64.' in archive.name else 62), archive.name
# Release assets include the Google prerequisites guide referenced by onboarding.
assert (bundle / 'GOOGLE_SETUP.md').is_file()
assert '](GOOGLE_SETUP.md)' in (bundle / 'START_HERE.md').read_text()
assert '](AGENT_INTEGRATION.md)' not in (bundle / 'START_HERE.md').read_text()
skill_bytes = (bundle / 'SKILL.md').read_bytes()
assert skill_bytes == (root / 'skills/searchprobe/SKILL.md').read_bytes()
skill_text = skill_bytes.decode('utf-8')
skill_lines = skill_text.splitlines()
assert skill_lines[0] == '---'
frontmatter_end = skill_lines.index('---', 1)
frontmatter = {}
for line in skill_lines[1:frontmatter_end]:
    key, separator, value = line.partition(':')
    assert separator and key and value.strip(), line
    assert key not in frontmatter, key
    frontmatter[key] = value.strip()
assert frontmatter.get('name') == 'searchprobe'
assert frontmatter.get('description', '').strip('"\'')
with tempfile.TemporaryDirectory(prefix='searchprobe-fresh-') as scratch:
    home = Path(scratch) / 'new user'
    home.mkdir()
    env = {'HOME': str(home), 'PATH': '/usr/bin:/bin:/usr/sbin:/sbin'}
    def run(args, expected=0):
        p = subprocess.run(args, cwd=home, env=env, capture_output=True, text=True, timeout=20)
        if p.returncode != expected:
            raise AssertionError(f'{args}: exit {p.returncode}, expected {expected}; {p.stdout} {p.stderr}')
        return p
    # Neither the source checkout nor Go/Git is involved in installation.
    run(['/bin/sh', str(bundle / 'install.sh'), '--from-dir', str(bundle)])
    binary = home / '.local/share/searchprobe/bin/gsc'
    env['PATH'] = str(binary.parent) + ':' + env['PATH']
    version = run(['gsc', '--version']).stdout.strip()
    assert version == 'SearchProbe gsc version ' + version_text, version
    for command in [[], ['setup'], ['auth'], ['auth', 'login'], ['performance'], ['compare'], ['inspect']]:
        help_text = run(['gsc', *command, '--help']).stdout
        assert 'Usage:' in help_text
        print('help', ' '.join(command) or 'root', len(help_text.encode()), 'bytes')
    setup = json.loads(run(['gsc', 'setup', '--agent', 'all', '--json'], 3).stdout)
    assert setup['error']['code'] == 'AUTH_REQUIRED'
    assert not (home / '.claude').exists()
    assert not (home / '.agents').exists()
    run(['gsc', 'sites'], 3)
    data = json.loads(run(['gsc', 'sites', '--json'], 3).stdout)
    assert data['error']['code'] == 'AUTH_REQUIRED'
    # Exercise BYO loopback startup without browser launch or a real grant.
    client = home / "client.json"
    client.write_text(json.dumps({"installed": {"client_id": "fixture.apps.googleusercontent.com"}}))
    login = run(['gsc', 'auth', 'login', '--client-file', str(client), '--credential-store', 'file', '--no-browser', '--timeout', '1ms'], 1)
    assert 'accounts.google.com' in login.stderr
    assert not list(home.glob('.config/gsc/credentials*'))
    # Install from the binary outside the checkout into both personal locations.
    status = json.loads(run(['gsc', 'agent', 'status', '--json']).stdout)
    assert all(item['state'] == 'missing' for item in status['data']['agents'])
    run(['gsc', 'agent', 'install'], 1)  # neither agent detected; explicit works
    run(['gsc', 'agent', 'install', '--agent', 'all'])
    run(['gsc', 'agent', 'install', '--agent', 'all'])  # idempotent
    status = json.loads(run(['gsc', 'agent', 'status', '--json']).stdout)
    assert all(item['state'] == 'installed' for item in status['data']['agents'])
    for relative in ['.claude/skills/searchprobe/SKILL.md', '.agents/skills/searchprobe/SKILL.md']:
        assert (home / relative).read_bytes() == (bundle / 'SKILL.md').read_bytes()
    # Keep CLI smoke coverage independent of Markdown layout: these representative
    # workflows mirror the canonical skill without counting or parsing prose examples.
    # With no credentials, each must parse fully and stop before Google traffic.
    (home / 'urls.txt').write_text('https://example.com/page\n')
    workflows = [
        ['gsc', 'sites', '--json'],
        ['gsc', 'performance', '--site', 'sc-domain:example.com', '--days', '28', '--dimensions', '', '--json'],
        ['gsc', 'compare', '--site', 'sc-domain:example.com', '--days', '28', '--dimensions', 'page',
         '--previous', '--sort', 'impressions-delta', '--asc', '--limit', '20', '--json'],
        ['gsc', 'compare', '--site', 'sc-domain:example.com', '--days', '28', '--dimensions', 'query',
         '--previous', '--sort', 'clicks-delta', '--asc', '--limit', '20', '--json'],
        ['gsc', 'inspect', 'https://example.com/page', '--site', 'sc-domain:example.com', '--json'],
        ['gsc', 'inspect', '--urls-file', 'urls.txt', '--site', 'sc-domain:example.com', '--json'],
        ['gsc', 'sitemaps', '--site', 'sc-domain:example.com', '--json'],
        ['gsc', 'sitemap', 'https://example.com/sitemap.xml', '--site', 'sc-domain:example.com', '--json'],
    ]
    for workflow in workflows:
        result = json.loads(run(workflow, 3).stdout)
        assert result['error']['code'] == 'AUTH_REQUIRED', workflow
    run(['gsc', 'agent', 'uninstall'])
    run(['gsc', 'agent', 'uninstall'])
    assert not (home / '.claude/skills/searchprobe').exists()
    assert not (home / '.agents/skills/searchprobe').exists()
    run(['gsc', 'auth', 'logout'])
    run(['/bin/sh', str(bundle / 'install.sh'), '--uninstall'])
    assert not binary.exists()
    print(version + ': install, help, login startup, AUTH_REQUIRED, agent skill lifecycle and representative workflows, logout, uninstall passed; no new grant.')
