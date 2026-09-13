# SearchProbe

**Google Search Console for coding agents.**

[![CI](https://github.com/morgancrozier/searchprobe/actions/workflows/ci.yml/badge.svg)](https://github.com/morgancrozier/searchprobe/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/morgancrozier/searchprobe?include_prereleases&sort=semver)](https://github.com/morgancrozier/searchprobe/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[Website](https://searchprobe.com) · [Documentation](#documentation) · [Contributing](CONTRIBUTING.md)

SearchProbe gives Claude Code, Codex, scripts, and terminal workflows direct,
read-only access to Google Search Console. It returns stable JSON without browser
automation and without routing Search Console data through a SearchProbe backend.

```bash
gsc performance \
  --site sc-domain:example.com \
  --days 28 \
  --dimensions page \
  --json
```

Without `--json`, the same query returns a compact table like this (synthetic data):

```text
PAGE                         CLICKS  IMPRESSIONS  CTR  POSITION
https://example.com/guide        42          910  4.6%       6.3
```

Then ask your coding agent a question that combines Search Console evidence with
the repository it is working in:

> Which pages lost the most Google visibility in the last 28 days, and are there code changes in this repository that might explain it?

SearchProbe fetches the Search Console evidence. Your agent can inspect the code,
compare the two sources, and keep observations separate from possible causes.

**Status: Public beta.** SearchProbe supports macOS and Linux on arm64 and amd64.
Windows is not currently supported, and release binaries are unsigned.

## Why SearchProbe

- **Agent-native:** stable JSON plus skills for Claude Code and Codex.
- **Local-first:** the local `gsc` binary talks directly to Google.
- **Read-only:** OAuth requests only `webmasters.readonly`.
- **No SearchProbe account:** there is no required SearchProbe backend or hosted data plane.
- **A normal CLI:** use it from a terminal or script without an agent.

## Quick start

You need a Google account with access to a Search Console property.

1. Install the current beta release:

   ```sh
   curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location \
     https://github.com/morgancrozier/searchprobe/releases/download/v0.1.0-beta.3/install.sh \
     -o install-searchprobe.sh
   sh ./install-searchprobe.sh --version v0.1.0-beta.3
   ```

2. Follow [Google OAuth setup](docs/GOOGLE_SETUP.md) to enable the Search Console
   API and download a Desktop OAuth client JSON.

3. Connect SearchProbe to Google:

   ```sh
   "$HOME/.local/share/searchprobe/bin/gsc" setup \
     --client-file "$HOME/Downloads/client.json"
   ```

   Setup opens Google consent, verifies property access, and can prepare the
   personal SearchProbe skill for Claude Code or Codex. Choose terminal-only use
   with `--agent none`.

4. Add SearchProbe to this shell and verify access:

   ```sh
   export PATH="$HOME/.local/share/searchprobe/bin:$PATH"
   hash -r
   gsc --version
   gsc sites --json
   ```

5. Copy an exact property identifier from `gsc sites` and try a useful query:

   ```sh
   gsc performance \
     --site sc-domain:example.com \
     --days 28 \
     --dimensions page \
     --json
   ```

6. Start a fresh Claude Code or Codex session in your website repository and ask
   the example question above. See [agent integration](docs/AGENT_INTEGRATION.md)
   for setup choices and troubleshooting.

For platform requirements, PATH setup, credential-storage options, updates, and
removal, see the [installation guide](docs/INSTALL.md).

## Capabilities

| Capability | Command |
| --- | --- |
| List Search Console properties | `gsc sites` |
| Query clicks, impressions, CTR, and position | `gsc performance` |
| Compare two periods with deterministic deltas | `gsc compare` |
| Inspect Google's indexed version of a URL | `gsc inspect` |
| List and inspect submitted sitemaps | `gsc sitemaps`, `gsc sitemap` |
| Return structured machine output | `--json` |
| Prepare Claude Code and Codex skills | `gsc setup`, `gsc agent` |

Use `gsc <command> --help` for command-specific options. The
[command reference](docs/COMMANDS.md) provides a compact overview.

## Privacy and safety

```text
user / coding agent
        |
        v
local gsc binary
        |
        v
Google Search Console API
```

SearchProbe has no hosted data plane and no telemetry. OAuth and API requests go
directly from the local binary to Google, using only the Search Console read-only
scope. Credentials are stored locally, preferably in macOS Keychain or Linux
Secret Service; an explicitly selected protected-file fallback is also available.

Search Console data passed to a coding agent is still subject to that agent
provider's data-handling settings. See [architecture](docs/ARCHITECTURE.md),
[Google API semantics](docs/GSC_API.md), and [credential storage](docs/GOOGLE_SETUP.md#local-credential-storage)
for the detailed model and limitations.

## Documentation

- [Installation](docs/INSTALL.md): supported platforms, installation, PATH, updates, and removal.
- [Google OAuth setup](docs/GOOGLE_SETUP.md): create a Desktop OAuth client and understand credential storage.
- [Command reference](docs/COMMANDS.md): available commands and flags at a glance.
- [Agent integration](docs/AGENT_INTEGRATION.md): set up and use SearchProbe with Claude Code or Codex.
- [Architecture](docs/ARCHITECTURE.md): packages, authentication, data flow, and JSON contract.
- [Search Console API semantics](docs/GSC_API.md): date, completeness, inspection, quota, and error behavior.
- [Roadmap](docs/ROADMAP.md): current public priorities and possible future directions.
- [Security policy](SECURITY.md): report vulnerabilities privately.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, validation commands,
and contribution expectations.

## License

[MIT](LICENSE). Copyright (c) 2026 Morgan Crozier.
