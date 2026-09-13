# Architecture

## Overview

SearchProbe is a local-first CLI with a small reusable core. Google-specific
behavior is isolated from presentation so command handlers remain thin and the
core can be tested independently.

```text
                    +---------------------------+
                    | Google Search Console API |
                    +-------------+-------------+
                                  ^
                                  |
                           HTTPS + OAuth 2.0
                                  |
                    +-------------+-------------+
                    |      SearchProbe core     |
                    |                           |
                    | OAuth / token refresh     |
                    | Search Console client     |
                    | pagination / retries      |
                    | normalization             |
                    | warnings / metadata       |
                    +-------------+-------------+
                                  |
                                  v
                               CLI UX
                 |
          +------+------+
          |             |
        human       coding agent
```

## Technology direction

The CLI is implemented in Go because it provides:

- single-binary distribution;
- fast startup;
- straightforward cross-platform releases;
- mature HTTP/OAuth support;
- good CLI tooling;
- no Node/Python runtime requirement.

Do not over-abstract the Google API. The Search Console surface is small enough that the implementation uses direct REST integration.

## Package shape

```text
cmd/gsc/                     package main; `go install ./cmd/gsc` yields the gsc binary
  main.go                    entry point
  root.go                    cobra root, --json flag, error rendering, exit codes
  version.go                 version from -ldflags or Go build info
  auth.go                    auth login | status | logout
  sites.go
  performance.go             flag parsing, meta/warning policy for Search Analytics
  compare.go                 period comparison (reuses the performance flags)
  inspect.go                 single and batch (--urls-file) URL inspection
  sitemaps.go                sitemaps (list) and sitemap (detail), read-only

internal/
  gscerr/                    normalized error type, stable codes, exit-code mapping
  config/                    config directory resolution, 0600 secret-file writes
  auth/
    client.go                Desktop OAuth client JSON parsing
    login.go                 PKCE S256 + loopback callback flow, revoke
    store.go                 credentials.json persistence
    tokensource.go           auto-refreshing, self-persisting token source
    browser.go
  searchconsole/
    client.go                direct REST client, bounded retries
    errors.go                Google error -> gscerr mapping
    sites.go
    analytics.go             Search Analytics request/normalization, filters, auto-pagination
    compare.go               two-window comparison: join, deltas, totals
    inspection.go            URL Inspection request/normalization
    sitemaps.go              read-only sitemap list/detail normalization
  output/
    envelope.go              JSON envelope
```

Preserve the dependency direction: command code calls `auth` and `searchconsole`; those packages never import `cmd/gsc` or `output`.

## Authentication model

### BYO Desktop OAuth

Every user imports their own Google Cloud Desktop OAuth client with
`gsc setup --client-file <path>`. `gsc auth login` reuses the persisted client.
There is no bundled shared identity or hosted configuration fetch. The former
client ID is retained only to block old grants and guide migration; logout can
still remove them. See [Google setup](GOOGLE_SETUP.md).

The user authenticates directly with Google and authorizes only the read-only Search Console scope.

```text
CLI
 |
 | generate PKCE verifier/challenge + random state
 | bind random loopback port
 v
system browser -> accounts.google.com
                     |
                     | user signs in / grants scope
                     v
              http://127.0.0.1:<random-port>/?code=...
                     |
                     v
CLI exchanges authorization code + PKCE verifier
                     |
                     v
access token + refresh token
```

For macOS/Linux/Windows desktop apps, Google's recommended installed-app mechanism is a loopback IP redirect on a random local port. Use PKCE with S256.

Official reference:
https://developers.google.com/identity/protocols/oauth2/native-app

### OAuth scope

V1 must request only:

```text
https://www.googleapis.com/auth/webmasters.readonly
```

Do not silently broaden the scope. Write-capable Search Console access is outside
the current architecture.

### Token storage

The storage facade supports macOS Keychain through native Security framework calls,
Linux Secret Service through D-Bus, and explicitly selected plaintext file storage.
The complete client, scopes and tokens reside in the selected backend. `auto`
reuses an established selection, preferring OS storage for new installations.
Interactive fallback requires explicit consent; scripts must select file storage.

Config resolution is `GSC_CONFIG_DIR`, then `$XDG_CONFIG_HOME/gsc`, then
`~/.config/gsc`. OS storage keeps backend and namespaced record metadata in
`storage.json`; file storage keeps `credentials.json`. Config directories are
`0700` and atomic files `0600`; permission-hardening failures stop writes.
OS writes use a fresh record, verify readback, atomically commit the metadata,
then delete the prior managed record. A failed pending write preserves the active
record. No secret is passed in command-line arguments. OS records are namespaced
by the absolute resolved config directory. macOS builds require CGO and an Apple SDK.

Downloaded OAuth JSON can be deleted after confirmed persistence. Later login,
refresh and setup reuse its saved client configuration. Explicit `--client-file`
requires fresh consent before replacing a grant. Logout removes client and tokens.

Never print access or refresh tokens in normal output, debug logs, `auth status`, or errors.

### Refresh behavior

Access tokens are short-lived. The core client should transparently refresh them using the stored refresh token. Commands should not require re-login during normal operation.

The client wraps `golang.org/x/oauth2`'s refreshing token source so that any refreshed token is written back to the selected credential store before the API call proceeds. A refresh rejected with `invalid_grant` maps to `AUTH_REVOKED`.

Login always sends `access_type=offline` and `prompt=consent` so Google issues a refresh token even when the account previously authorized the client.

### Logout

`gsc auth logout` should:

1. attempt to revoke the Google token when appropriate;
2. remove local credentials;
3. preserve unrelated configuration unless the user asks to remove it.

## OAuth project and quota attribution

Requests use each user's Google Cloud project. Google also enforces per-user,
per-site and load quotas. No hosted proxy is involved.

## Data path and privacy

Default:

```text
local process <------HTTPS------> Google
```

There is no SearchProbe server in the data path.

Consequences:

- the maintainer does not receive users' Search Console data;
- refresh tokens remain local;
- no central user database is required;
- no service availability dependency is introduced;
- the privacy model is straightforward to explain and audit.

## Core client

`internal/searchconsole.Client` provides domain methods for sites, performance, comparisons, URL inspection, and sitemaps independently of Cobra and output rendering. Commands reuse these methods rather than duplicating API logic.

## JSON contract

Machine output should be normalized instead of dumping arbitrary Google responses.

Example success:

```json
{
  "ok": true,
  "data": {
    "rows": [
      {
        "query": "example search",
        "clicks": 42,
        "impressions": 910,
        "ctr": 0.0461538462,
        "position": 6.3
      }
    ]
  },
  "meta": {
    "site": "sc-domain:example.com",
    "startDate": "2026-08-15",
    "endDate": "2026-09-11",
    "timezone": "America/Los_Angeles",
    "dataState": "final"
  },
  "warnings": []
}
```

Example error:

```json
{
  "ok": false,
  "error": {
    "code": "PROPERTY_ACCESS_DENIED",
    "message": "The authenticated Google account cannot access sc-domain:example.com.",
    "action": "Run `gsc sites --json` to list accessible properties."
  }
}
```

### Requirements

- stable field names;
- typed values, not formatted strings;
- explicit date/time-zone semantics;
- warnings for known incompleteness/partial data;
- actionable error codes;
- no ANSI/color in JSON mode;
- stdout for successful data; stderr for human diagnostics;
- non-zero exit code on failed commands.

### Contract details

Success envelopes always contain `ok`, `data`, `warnings` (an array, possibly empty) and usually `meta`. Failure envelopes contain only `ok` and `error`; `error.action` is present whenever there is a concrete next step. Failure envelopes are written to stdout in `--json` mode so agents can parse them.

Exit codes:

| Code | Meaning |
| --- | --- |
| 0 | success |
| 1 | failure (API, network, config, sitemap/property not found, or internal error) |
| 2 | invalid arguments (`INVALID_ARGUMENT`, `INVALID_DATE_RANGE`, `INVALID_DIMENSION_COMBINATION`, `URL_OUTSIDE_PROPERTY`) |
| 3 | authentication required (`AUTH_REQUIRED`, `AUTH_REVOKED`, `AUTH_SCOPE_INSUFFICIENT`) |

`gsc performance` rows are flat objects: dimension values keyed by dimension name (in request order) followed by `clicks`, `impressions`, `ctr`, `position`. `data.returnedTotals` sums only the returned rows.

`meta` for `gsc performance`:

| Key | Meaning |
| --- | --- |
| `site` | canonical property identifier |
| `dateMode` | `days` (range computed from `--days`, ending yesterday PT) or `explicit` (`--start`/`--end`) |
| `startDate`, `endDate`, `timezone` | the exact PT dates sent to Google; timezone is always `America/Los_Angeles` |
| `days` | only in `days` mode |
| `searchType` | `web`, `image`, `video`, `news`, `discover`, or `googleNews` |
| `dataState` | `final`, `all`, or `hourly_all` |
| `dimensions` | canonical dimension names in request order |
| `filters`, `filterLogic` | the `dimensionFilterGroups` filters sent (always an array) and `and` (Google's only group type) |
| `requestedAggregationType`, `aggregationType` | the requested value (when given) and Google's `responseAggregationType` |
| `all`, `rowLimit`, `startRow`, `pagesFetched`, `rowCount` | pagination mode and request accounting; under `--all`, `rowLimit` is the 25,000 page size |
| `paginationExhausted` | this API query has no further pages |
| `paginationStopReason` | `repeated_page` or `safety_cap` when `--all` stopped before the API query was exhausted; `paginationExhausted` is then always `false` |
| `sourceMayBePartial` | always `true`: Google returns top rows only, so an exhausted query still does not prove the source data is complete |
| `firstIncompleteDate`, `firstIncompleteHour` | Google's `first_incomplete_*` metadata when present |
| `firstObservedDate`, `lastObservedDate` | only when the `date` dimension was requested: the earliest and latest date values among the returned rows. They describe returned rows, not data availability: a missing date row may mean no activity or top-rows truncation. `startDate`/`endDate` remain the requested window; gsc never issues extra requests to discover this |

`--all`, `--limit`, and `--start-row`: `--all` pages with the maximum row limit from row 0 until a short page (the API query is exhausted), a repeated page, context cancellation, or an upstream error. A defensive guard of 1,000 pages (25,000,000 rows) exists only to stop a runaway loop; reaching it is reported as `paginationStopReason: "safety_cap"` with `paginationExhausted: false` and a `PAGINATION_STOPPED` warning, never as exhaustion. Combining `--all` with `--limit` or `--start-row` is rejected. Without `--all`, `--limit` (default 1000, max 25,000) and `--start-row` control exactly one request. Google's row ordering is preserved across pages. A failure on a later page fails the whole command with the normalized error and the page number in the message; no partial result is emitted.

`gsc compare` runs the base query over a current and a previous window with identical site, dimensions, filters, type, data state, and aggregation, fully paginating both (a window that cannot be exhausted fails the command with `PAGINATION_INCOMPLETE`). Rows are joined on their dimension values and emitted as `{<dimension values>, current, previous, delta}`; `current`/`previous` are `null` for rows present in one period only. `delta` is current minus previous: `clicks`/`impressions` treat a missing period as 0; `ctr`/`position` are `null` unless both periods have the row; `clicksPct`/`impressionsPct` are `null` when the previous value is missing or zero (never `Inf`/`NaN`), otherwise rounded to two decimals. Position is lower-is-better, so a negative `delta.position` is an improvement. Rows default to current clicks, current impressions, previous clicks (all descending), then dimension values. `--sort` selects `current-clicks`, `current-impressions`, `clicks-delta`, `impressions-delta`, or `position-delta` as the primary key (descending unless `--asc`); ties always fall through to the default chain, which is total, so ordering is deterministic. Rows without a position delta sort last under `position-delta`. `meta.sort` records the key and direction and `meta.sortedBy` the full chain. `--limit` truncates after sorting while `data.returnedTotals` always covers every joined row. `meta` carries `current`/`previous` window blocks (`startDate`, `endDate`, `days`, `rowCount`, `pagesFetched`, observed range and incomplete markers when present), `previousMode` (`previous` or `explicit`), `joinedRows`, `limit`, `sortedBy`, `deltaSemantics`, and the same query context as `performance`. `--previous` is the immediately preceding window of equal length; explicit windows may not overlap the current one.

`gsc sitemaps` returns `data.sitemaps[]` and `gsc sitemap` returns one object, each with `path`, `type`, `isSitemapsIndex`, `isPending`, `lastSubmitted`, `lastDownloaded`, `warnings`, `errors`, `submittedUrls` (sum of `contents[].submitted`), and `contents[]{type, submitted}`. Google's deprecated `contents[].indexed` counter is omitted.

### Warning policy

Warnings are reserved for conditions that materially change how a specific result should be read. Caveats that apply to every call are metadata, not warnings: `sourceMayBePartial` (Search Analytics returns top rows only), `dataState`, `dateMode`, `inspectionType`/`liveTest` (URL Inspection describes the indexed version only), and `readOnly`/`submittedCountsOnly` (sitemaps). The rules are deterministic and covered by tests:

| Code | Command | Emitted when |
| --- | --- | --- |
| `TOP_ROWS_ONLY` | performance `--all`, compare | The result was fully paginated, where an exhausted query is most likely to be mistaken for complete data. |
| `RECENT_DAYS_MAY_BE_EXCLUDED` | performance, compare | Data state `final` on a window of 7 days or fewer that ends within 3 days of today (PT). A conservative statement about Google's finalization lag; never inferred from which date rows were returned. Under `compare` the message notes that deltas can understate that period. |
| `PRELIMINARY_DATA` | performance, compare | Data state `all` or `hourly_all`. |
| `INCOMPLETE_DATA` | performance, compare | Google itself reported `first_incomplete_date`/`first_incomplete_hour`; this explicit metadata is the only signal used for incompleteness. |
| `ROW_LIMIT_REACHED` | performance | A single request returned exactly `rowLimit` rows. |
| `PAGINATION_STOPPED` | performance `--all` | Pagination stopped on a repeated page or the safety cap. |
| `UNEQUAL_WINDOWS` | compare | Explicit windows differ in length, so absolute deltas are not like-for-like. |
| `REVOKE_FAILED` | auth logout | Local credentials were removed but Google revocation failed. |

## Human output

Human-readable tables should be derived from the same normalized result objects used for JSON. Do not maintain parallel semantics.

Example:

```text
QUERY                    CLICKS  IMPRESSIONS    CTR  POSITION
example search               42          910   4.6%       6.3
```

## Property handling

Search Console property identifiers can be awkward:

```text
sc-domain:example.com
https://www.example.com/
```

The CLI accepts and retains canonical API property identifiers.

Never silently guess between multiple matching URL-prefix/domain properties.

## Pagination

Search Analytics allows `rowLimit` up to 25,000 and uses `startRow` for pagination.

The core should implement pagination once. Commands should expose a safe distinction between:

- returning a requested limit;
- fetching all rows the API makes available.

`--all` must never imply that Google exposed every underlying search record. It means "paginate through all rows available from this API query." Warnings/documentation should preserve that distinction.

## Retries and quota handling

Implement bounded exponential backoff for retryable API failures and quota conditions where retrying is appropriate.

Do not endlessly retry:

- invalid arguments;
- authentication failures requiring user action;
- property permission failures;
- daily quota exhaustion.

Errors should distinguish retryable from terminal failures.

The client retries a request up to three times with short backoff for HTTP 429, HTTP 5xx, and transport errors; everything else fails immediately.

## Caching

The CLI has no cache. Each command queries Google and reports the resulting data
state and completeness metadata.

## Agent skill distribution

`skills/searchprobe/SKILL.md` is the canonical portable source, embedded by its
adjacent Go package. `internal/agent` detects local environments and installs
identical copies with a SHA256 ownership receipt. `cmd/gsc/agent.go` exposes
install/status/uninstall through the existing envelope and error handling.
Copies remain usable when a checkout or versioned package directory disappears;
there is no runtime download or checkout dependency. Updates require rerunning
install with the new binary. Modified/unmanaged skills, extra files and symlink
paths are conflicts; no force option or recursive uninstall is provided.

The agent installer changes only selected personal skill directories and a transient
sibling lock/staging directory. It does not edit agent configuration, authorize
shell execution, authenticate to Google, or send data. Agent providers can receive
CLI results when the user asks their agent to analyze them. See [setup](AGENT_INTEGRATION.md).

## Security requirements

- read-only OAuth scope in V1;
- PKCE S256;
- random OAuth `state` value validated on callback;
- bind loopback callback only to loopback interfaces;
- random available callback port;
- no token logging;
- restrict local token-file permissions when keychain unavailable;
- sanitize Google API errors before printing sensitive context;
- never execute arbitrary commands based on API data;
- no telemetry by default.

## OAuth identity lifecycle

The user configures their own Desktop OAuth application. Switching login identities replaces the single stored grant only after successful login. Refresh uses that stored client configuration, never a bundled identity. Identity rotation may require re-login for existing grants.

Production publishing does not guarantee every account can authorize. Diagnose the exact Google error before changing authentication; organization policy and property access remain controlled by Google and the user's account.

## Guided setup

`gsc setup` composes the existing auth, sites and agent packages in the command
layer; it introduces no setup-state file, default property, or server dependency.
State comes from credential loading, a live sites request, and managed skill
receipts. Skill conflicts are preflighted before auth. Google failures do not
trigger skill installation; successful auth survives a later failure for safe retry.
Explicit `--agent` selection authorizes the named destinations, while interactive
setup displays choices and paths. Piped/JSON runs require explicit selection and
never initiate OAuth. Browser recovery retries once for recognized auth errors,
retaining the stored client identity. JSON uses the existing envelope; `ready:false`
with `NO_ACCESSIBLE_PROPERTIES` distinguishes successful verification from useful
query readiness. Agent activation is always reported as unverified.
