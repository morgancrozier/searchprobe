# Use SearchProbe with coding agents

SearchProbe gives coding agents deterministic, read-only access to Google Search
Console. The skill teaches discovery and interpretation; `gsc` owns auth, API
requests, pagination and comparison. SearchProbe also provides an optional local
stdio MCP server. Neither path adds a SearchProbe backend, an embedded LLM, or
per-project instruction edits.

## Setup

Install the current pinned SearchProbe GitHub release using
[the installation guide](INSTALL.md), then:

```sh
gsc --version                 # must identify SearchProbe, not Ghostscript
gsc setup                     # Google access + personal agent skills + first prompt
```

Review the detected state and paths, then accept the shown agent choice or select
`claude`, `codex`, `all`, or `none` for terminal-only use. When none are detected,
the prompt offers both explicitly, so desktop-only users do not need to understand
detection. EOF/cancellation or an invalid choice stops before changes.

For an explicit choice (no selection prompt):

```sh
gsc setup --agent claude
gsc setup --agent codex
gsc setup --agent all
```

Setup preflights skill conflicts, reuses credentials, verifies access through
Google's sites API (refreshing tokens normally), and installs/updates selected
skills. Missing credentials or a revoked/insufficient grant triggers browser login
in a terminal; existing BYO identity is retained. Network and permission failures
stop without relogging in or installing skills. Authentication completed before a
later failure is retained; rerunning is safe. Corrupt credentials require the
reported recovery steps rather than silent replacement.

No setup marker or default property is saved: actual credentials, Google access,
and skill contents are checked each time. Identical skills are not rewritten.
Zero accessible verified properties yields `ready:false` and recovery instructions;
it does not claim the account is ready to query. A single property appears in the
first prompt; multiple properties require the agent to ask which matches the repo.
The sites check does not prove Search Analytics data exists or that an agent has
loaded the skill. Start a fresh local agent session to try the printed prompt.

For scripts, authenticate separately and select agents explicitly:

```sh
gsc auth login
gsc setup --agent all --json
```

Piped/JSON setup never opens a browser or reads a selection. It requires `--agent`
and working credentials. `--agent auto` includes detected agents and existing skill
destinations; it fails when neither exists. This keeps explicitly prepared desktop
skills up to date. JSON emits one normal envelope with `data.ready`,
`authenticated`, `agents`, `sites`, `firstPrompt`, and `nextStep`; metadata separates
Google access verification from unverified agent activation. No accessible
properties is a successful check (exit 0) with `ready:false` and a structured
warning. Operational failures use the existing nonzero error envelope.

Advanced users can keep the independent commands:

```sh
gsc auth login                 # browser auth only
gsc sites --json               # Google access only
gsc agent install              # skill files only
gsc agent status
gsc agent uninstall
```

## Optional MCP integration

After `gsc setup` has stored working credentials, configure an MCP-capable client
to launch:

```sh
gsc mcp
```

The process exposes `sites`, `performance`, `compare`, `inspect`, `sitemaps`, and
`sitemap` as structured read-only tools over stdio. It uses the existing local
credentials and connects directly to Google's API. It does not open a browser;
missing or unusable credentials produce a tool error directing the user back to
`gsc setup` in a terminal.

SearchProbe does not currently add or change Claude Code or Codex MCP settings.
Configure the client separately and ensure the same environment can resolve the
intended `gsc` binary. The existing SearchProbe skill and CLI remain supported
independently of MCP.

For the low-level `gsc agent install` command, the default detects a `claude`/`codex`
executable on PATH or an existing agent configuration directory. These are hints, not proof that an agent is runnable.
If neither is detected, auto installation fails with instructions and writes no
skills. When one is detected it installs that one and reports the other as skipped.
Status/uninstall inspect both destinations even if the agent was removed.

| Agent | Personal skill | Configuration detection |
| --- | --- | --- |
| Claude Code | `~/.claude/skills/searchprobe/SKILL.md` | `~/.claude` or `CLAUDE_CONFIG_DIR` |
| Codex | `~/.agents/skills/searchprobe/SKILL.md` | `~/.codex` or `CODEX_HOME` |

`CLAUDE_CONFIG_DIR` moves Claude's skill destination too. `CODEX_HOME` is a detection
hint; it does not move Codex's documented agent-neutral personal skill location.
Overrides must be absolute paths. Symlinked destination paths are refused; inspect
and manually manage those installations if you deliberately use linked dotfiles.

Restart the agent after changing PATH. If a newly installed skill doesn't appear,
start a fresh session. Check `/skills` or `$searchprobe` in Codex, `/searchprobe` in
Claude Code. Explicit invocation is a discovery diagnostic, not required for normal
use. Disabled skills, project/plugin duplicates, managed policy and shell/network
permissions can affect activation; `status` does not inspect or override these.
A remote/cloud agent needs its own accessible CLI and authorized environment.

## Try it in a website repository

Start a fresh Claude Code or Codex session in the relevant repository and ask:

- “Which pages on my site lost the most Google search visibility recently?”
- “What queries are close to the first page?”
- “Check whether https://example.com/page is indexed and what canonical Google chose.”
- “See whether anything in this repository could plausibly explain the biggest organic traffic declines.”

Supply the real site/URL if ambiguous. The agent should discover the skill without
being told to use `gsc`, select the correct property, prefer JSON and use deterministic
comparisons. Expect explicit dates, completeness/freshness caveats and a separation
of Search Console observations, repository evidence and causal hypotheses. Average
position is not a universal live rank. A commit is not proof of deployment.

## Ownership and updates

The binary embeds `skills/searchprobe/SKILL.md`; native binaries use the same source without needing a checkout.
Each agent receives an identical regular file and `.searchprobe-sha256` receipt.
Copies avoid dangling links after package upgrades/removal. They do not introduce
separately authored variants. Rerun `gsc setup` (or `gsc agent install` for skills only) after updating the binary:
unchanged managed copies update, identical copies are a no-op. There are no downloads.

Modified files, missing/invalid receipts, extra files and symlinks produce conflicts.
Back up and move the conflicting `searchprobe` directory aside, then reinstall.
There is no `--force`. Status reports `missing`, `installed`, `outdated`, or `conflict`;
`installed` means bytes match this binary, not that an agent has loaded the skill.
Concurrent mutations use a sibling `.lock`; an interrupted install may require
manual inspection of that lock or a conflicted receipt before retrying.

```sh
gsc agent status --json
gsc agent uninstall             # remove verified managed copies only
# Or remove one:
gsc agent uninstall --agent claude
```

Uninstall is idempotent and preserves modified skills, unrelated files, credentials,
and agent configuration. Empty parent directories remain. Remove skills before
removing the binary; binary/package uninstall does not remove personal skills.
Failures use the existing `ok:false,error` contract and report per-agent outcomes,
including successful operations when another destination failed. Rerunning is safe.

## Privacy

Google OAuth and API traffic remains directly between the local process and Google.
SearchProbe has no telemetry or server receiving results. An agent analyzing output
can send it to its model provider under that provider's settings; local-first CLI
transport does not mean local model inference.

## Upstream references

Verified against first-party documentation on 2026-09-12:

- [Claude Code skills](https://code.claude.com/docs/en/skills): personal discovery and implicit/explicit invocation.
- [Claude configuration directory](https://code.claude.com/docs/en/claude-directory): `CLAUDE_CONFIG_DIR` behavior.
- [Codex skills](https://learn.chatgpt.com/docs/build-skills): personal `.agents/skills`, discovery and disabling skills.
- [Agent Skills specification](https://agentskills.io/specification): portable `name`/`description` frontmatter.
