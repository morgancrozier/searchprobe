# AGENTS.md

## What this project is

SearchProbe is a local-first, read-only Google Search Console CLI for developers,
scripts, and coding agents. The product name is SearchProbe, the command is `gsc`,
and the Go module is `github.com/morgancrozier/searchprobe`.

## Product invariants

Preserve these product boundaries unless a requested change explicitly alters
them:

- users provide a Google Desktop OAuth client;
- OAuth and API traffic go directly from the local binary to Google;
- the OAuth scope is read-only;
- the CLI works independently of Claude Code, Codex, or any other agent;
- package installation does not modify agent configuration;
- JSON output is a stable interface, not debug output.

## Security and OAuth invariants

- Request only `https://www.googleapis.com/auth/webmasters.readonly`.
- Generate and validate random OAuth state.
- Use PKCE S256.
- Bind the OAuth callback only to a loopback address.
- Never print or log access tokens, refresh tokens, or OAuth client secrets.
- Never commit credentials or live Search Console data.
- Prefer OS credential storage; require an explicit choice before protected-file fallback.
- Do not add telemetry or route API data through a SearchProbe server.
- Keep installation separate from agent-skill setup.

## Google API correctness

- Search Analytics date ranges use Pacific Time.
- Search Analytics does not guarantee every underlying row.
- Completing pagination does not prove source-data completeness.
- Preserve incomplete, preliminary, truncated, quota-limited, or otherwise
  uncertain states in metadata or structured warnings.
- URL Inspection describes Google's indexed version. It cannot perform the live
  URL test or request indexing.
- Preserve canonical Search Console property identifiers internally.
- Verify uncertain upstream behavior against current official Google documentation.

## Architecture

Keep command handlers thin. Authentication and Google API behavior belong in
reusable internal packages, not directly in Cobra commands.

```text
cmd/gsc/                 thin Cobra commands
internal/gscerr/         normalized errors and exit codes
internal/config/         config paths and protected file writes
internal/auth/           Desktop OAuth, credential storage, refresh
internal/searchconsole/  API client, pagination, normalization, error mapping
internal/output/         JSON envelope
internal/agent/          Claude Code and Codex skill management
```

Prefer a small dependency graph and explicit HTTP/domain code. Preserve the
dependency direction described in `docs/ARCHITECTURE.md`.

## JSON contract

JSON mode must provide:

- a predictable top-level envelope;
- stable field names and typed numeric values;
- explicit site, date, time-zone, and data-state metadata;
- structured warnings only when a condition changes interpretation;
- no ANSI formatting;
- successful machine data on stdout and diagnostics on stderr;
- nonzero exit codes with normalized error codes on failure.

Do not expose Google's raw response as the only public contract.

## Distribution constraints

Normal users install prebuilt macOS/Linux bundles; Go is contributor tooling.
The canonical binary name remains `gsc`. Installation must coexist with other
programs named `gsc` and must never overwrite or delete another package's binary.
`scripts/install.sh` installs the binary only. Agent setup remains an explicit
`gsc setup` or `gsc agent` action.

## Validation

- Run `make check` for code changes.
- Run `go test ./internal/agent ./cmd/gsc` for setup or skill-management changes.
- For installer changes, also run `sh -n scripts/install.sh`,
  `shellcheck scripts/install.sh`, and `python3 scripts/test_install.py`.
- For release-bundle changes, build with `python3 scripts/release.py <version>`
  and run `python3 scripts/test_bundle.py dist/<version>`.
- Do not use live credentials, publish a release, or change repository visibility
  for validation.
