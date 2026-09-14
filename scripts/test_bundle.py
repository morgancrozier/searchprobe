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
import time

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


def read_mcp_response(process, request_id, lines):
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        line = process.stdout.readline()
        if not line:
            raise AssertionError(f'MCP server closed before response {request_id}; stdout={lines!r}')
        lines.append(line)
        message = json.loads(line)
        assert message.get('jsonrpc') == '2.0', message
        if message.get('id') == request_id:
            return message
    raise AssertionError(f'timed out waiting for MCP response {request_id}')


def write_mcp_message(process, message):
    process.stdin.write(json.dumps(message, separators=(',', ':')) + '\n')
    process.stdin.flush()


def smoke_mcp(binary, cwd, env, version):
    process = subprocess.Popen(
        [str(binary), 'mcp'], cwd=cwd, env=env,
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        text=True, bufsize=1)
    stdout_lines = []
    try:
        write_mcp_message(process, {
            'jsonrpc': '2.0', 'id': 1, 'method': 'initialize',
            'params': {
                'protocolVersion': '2025-11-25',
                'capabilities': {},
                'clientInfo': {'name': 'searchprobe-bundle-test', 'version': '1'},
            },
        })
        initialized = read_mcp_response(process, 1, stdout_lines)
        assert 'error' not in initialized, initialized
        result = initialized['result']
        assert result['protocolVersion'] == '2025-11-25', result
        assert result['serverInfo']['name'] == 'searchprobe', result
        assert result['serverInfo']['title'] == 'SearchProbe', result
        assert result['serverInfo']['version'] == version, result

        write_mcp_message(process, {
            'jsonrpc': '2.0', 'method': 'notifications/initialized', 'params': {},
        })
        write_mcp_message(process, {
            'jsonrpc': '2.0', 'id': 2, 'method': 'tools/list', 'params': {},
        })
        listed = read_mcp_response(process, 2, stdout_lines)
        assert 'error' not in listed, listed
        names = {tool['name'] for tool in listed['result']['tools']}
        assert names == {'sites', 'performance', 'compare', 'inspect', 'sitemaps', 'sitemap'}, names

        write_mcp_message(process, {
            'jsonrpc': '2.0', 'id': 3, 'method': 'tools/call',
            'params': {'name': 'sites', 'arguments': {}},
        })
        called = read_mcp_response(process, 3, stdout_lines)
        assert 'error' not in called, called
        tool_result = called['result']
        assert tool_result['isError'] is True, tool_result
        structured = tool_result['structuredContent']
        assert structured['ok'] is False, structured
        assert structured['error']['code'] == 'AUTH_REQUIRED', structured
        assert 'gsc setup' in structured['error']['action'], structured

        process.stdin.close()
        process.stdin = None
        process.wait(timeout=5)
        remainder = process.stdout.read()
        if remainder:
            stdout_lines.extend(remainder.splitlines(keepends=True))
        stderr = process.stderr.read()
        assert process.returncode == 0, (process.returncode, stderr)
        assert stderr == '', stderr
        for line in stdout_lines:
            message = json.loads(line)
            assert message.get('jsonrpc') == '2.0', message
    finally:
        if process.poll() is None:
            process.kill()
            process.wait()


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
    for command in [[], ['setup'], ['auth'], ['auth', 'login'], ['performance'], ['compare'], ['inspect'], ['mcp']]:
        help_text = run(['gsc', *command, '--help']).stdout
        assert 'Usage:' in help_text
        print('help', ' '.join(command) or 'root', len(help_text.encode()), 'bytes')
    smoke_mcp(binary, home, env, version_text)
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
