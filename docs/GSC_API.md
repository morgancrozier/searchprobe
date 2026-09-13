# Google Search Console API notes

This document captures the API semantics that materially affect SearchProbe.
Treat Google's official documentation as authoritative when upstream behavior changes.

## Official references

- API overview/reference: https://developers.google.com/webmaster-tools/v1/api_reference_index
- Search Analytics query: https://developers.google.com/webmaster-tools/v1/searchanalytics/query
- Usage limits: https://developers.google.com/webmaster-tools/limits
- URL Inspection: https://developers.google.com/webmaster-tools/v1/urlInspection.index/inspect
- OAuth installed apps: https://developers.google.com/identity/protocols/oauth2/native-app

## API surfaces

The official Search Console API exposes the following major areas relevant to this project:

1. Search Analytics
2. Sites/properties
3. Sitemaps
4. URL Inspection

The web UI contains reports and actions that are not represented in this API. SearchProbe should never claim to be a complete headless clone of Search Console.

## Authorization

For read-only access, use:

```text
https://www.googleapis.com/auth/webmasters.readonly
```

The authenticated Google identity must already have access to the Search Console property being queried.

The OAuth application identifies the software; it does not grant access to properties by itself.

## Sites/properties

Canonical property identifiers include:

```text
sc-domain:example.com
https://www.example.com/
```

Domain properties and URL-prefix properties are distinct. The CLI should preserve Google's canonical property identifier internally.

SearchProbe lists and reads site/property metadata. It does not add or delete
property membership even though the API contains write-capable methods.

## Search Analytics

### Metrics

Typical result rows expose:

- clicks
- impressions
- CTR
- average position

### Dimensions

Important dimensions include:

- query
- page
- country
- device
- date
- hour (where supported with the requested data state)
- search appearance

Search types supported by the API may include web, image, video, news, Discover, and Google News according to the request semantics in Google's current documentation.

The request field is `type` (`web` default, `image`, `video`, `news`, `discover`, `googleNews`); `searchType` is deprecated. Allowed dimensions are `country`, `device`, `page`, `query`, `searchAppearance`, `date`, and `hour`; no dimension may repeat. `dataState` accepts `all`, `final` (default), and `hourly_all`; `hour` requires `hourly_all`. gsc exposes these as `--type`, `--dimensions`, and `--data-state`, defaulting to `web` and `final`, and rejects `hour` unless the data state is `hourly_all`. Google does not document a date-range limit for hourly data; upstream errors are passed through.

Do not hard-code assumptions about every combination being valid. Prefer explicit validation based on current documented API behavior and preserve upstream errors clearly.

### Date semantics

Search Analytics start/end dates use Pacific Time (PT), not the caller's local timezone.

The CLI must expose this in JSON metadata and avoid ambiguous behavior around terms such as `today` unless semantics are explicitly defined.

Recommended metadata:

```json
{
  "timezone": "America/Los_Angeles"
}
```

gsc defines `--days N` as the N Pacific-Time calendar days ending **yesterday** (PT). Today is never final, and because only finalized data is requested the last two or three days of the range are usually absent from results; the CLI emits `RECENT_DAYS_MAY_BE_EXCLUDED` (for windows of seven days or fewer ending within the last three days) rather than presenting the range as fully populated. With `--data-state all` or `hourly_all` it emits `PRELIMINARY_DATA` instead and propagates Google's `first_incomplete_date`/`first_incomplete_hour`.

### Ordering

Google returns rows ordered primarily by clicks descending unless request semantics dictate otherwise. If stable secondary ordering matters to the CLI, perform it explicitly after retrieval and document that behavior.

### Data completeness

Google explicitly states that the Search Analytics API does **not** guarantee every underlying row. It returns top rows and is subject to internal Search Console limits.

Therefore:

- summing returned query rows may not match property totals;
- anonymized/private queries can contribute to aggregates without appearing as rows;
- `--all` can only mean all rows made available by the API for that query;
- machine output should not mark a query as globally complete unless that can actually be established.

Never fabricate a boolean `complete: true` merely because pagination ended.

A property-level totals query (`--dimensions ""`) counts anonymized queries that a `query`-dimension query never lists, so the two differ by design. `gsc` does not estimate anonymized traffic.

`gsc inspect --urls-file` inspects sequentially, de-duplicates the list, and caps one batch at 2,000 URLs to match the per-site daily URL Inspection quota; quota, rate-limit, auth, and network failures stop the batch, while per-URL validation and 4xx errors are recorded per URL.

A better representation is to distinguish pagination completion from source completeness, for example:

```json
{
  "meta": {
    "paginationExhausted": true,
    "sourceMayBePartial": true
  }
}
```

### Row limits and pagination

The Search Analytics request supports:

```text
rowLimit: 1..25000
startRow: offset
```

Default row limit is 1,000.

The core should own pagination behavior so commands do not reimplement it.

### Data state / freshness

Google can expose finalized and fresher/incomplete data depending on request parameters supported by the API.

If the response identifies an incomplete date/hour, propagate that information rather than presenting it as final.

Human example:

```text
Warning: results include preliminary Search Console data that may change.
```

Machine example:

```json
{
  "warnings": [
    {
      "code": "INCOMPLETE_DATA",
      "message": "The requested range includes preliminary Search Console data."
    }
  ]
}
```

### Filtering

Search Analytics supports dimension filters. The CLI exposes filtering without inventing a different mental model from Google's API.

Prefer explicit syntax that can be represented deterministically in machine output. Avoid clever natural-language filters in the core CLI.

Filters accept dimension `country`, `device`, `page`, `query`, or `searchAppearance` (not `date`/`hour`) and operator `contains`, `equals`, `notContains`, `notEquals`, `includingRegex`, or `excludingRegex` (RE2 syntax). `groupType` supports only `and`, and multiple groups are also ANDed, so OR is not expressible. gsc's `--filter "<dimension> <operator> <expression>"` maps each filter into one `and` group; the exact request body is covered by tests.

### Aggregation

Aggregation can materially change result interpretation. Preserve the effective aggregation type in metadata when available or explicitly selected.

Do not silently compare differently aggregated results.

`aggregationType` is a documented request option with values `auto` (default), `byPage`, `byProperty`, and `byNewsShowcasePanel`. `byProperty` is not supported for `type=discover` or `googleNews`, and `byNewsShowcasePanel` requires a `NEWS_SHOWCASE` `searchAppearance` filter with `discover` or `googleNews`. gsc exposes `--aggregation` with exactly these values, omits the field when unset so Google chooses, and reports both the requested value and Google's `responseAggregationType`. Combination rules are left to Google's validation.

## Search Analytics quotas

Google currently documents these request quotas:

### Per-site

- 1,200 queries per minute

### Per-user

- 1,200 queries per minute

### Per-project

- 40,000 queries per minute
- 30,000,000 queries per day

Google also enforces short-term and long-term **load quotas** that are not expressed as simple public counters.

Query load increases with expensive dimensions/filters and long date ranges. Google specifically notes that page/query grouping or filtering can be expensive and that longer ranges cost more load.

SearchProbe behavior:

- retry quota failures with bounded backoff only when sensible;
- avoid automatically firing large matrices of queries;
- preserve a quota-specific error code;
- do not promise that project-level request quotas are the only limiting factor.

## URL Inspection

Endpoint family:

```text
POST https://searchconsole.googleapis.com/v1/urlInspection/index:inspect
```

Required inputs include:

- inspection URL
- Search Console property/site URL

The inspection URL must be under the specified property.

### Important limitation

The API currently returns information about the version in Google's index. It **cannot perform the live URL inspection/indexability test** available in the Search Console browser UI.

The CLI should explicitly describe this as indexed-state inspection.

The response is `inspectionResult` containing `inspectionResultLink`, `indexStatusResult`, `ampResult`, `mobileUsabilityResult`, and `richResultsResult`. `indexStatusResult` carries `verdict` (`PASS`, `PARTIAL`, `FAIL`, `NEUTRAL`), `coverageState`, `robotsTxtState`, `indexingState`, `lastCrawlTime`, `pageFetchState`, `googleCanonical`, `userCanonical`, `crawledAs`, `sitemap[]`, and `referringUrls[]`. Google may omit any sub-result; gsc omits absent ones rather than inventing empty values.

Potential result fields include:

- verdict
- coverage state
- robots.txt state
- indexing state
- last crawl time
- page fetch state
- Google canonical
- user-declared canonical
- crawler/user agent
- associated sitemaps where known
- referring URLs where known
- rich-results information where returned

Do not assume sitemap/referring URL lists are exhaustive.

### URL Inspection quotas

As documented by Google at the time this spec was written:

Per site:

- 600 queries per minute
- 2,000 queries per day

Per project:

- 15,000 queries per minute
- 10,000,000 queries per day

The per-site daily cap is likely to be more relevant to real users than the shared project cap.

## Sitemaps

SearchProbe lists sitemap state and details exposed by the API. It does not expose
sitemap submission or deletion because the OAuth and command surface are
intentionally read-only.

`GET /sites/{siteUrl}/sitemaps` (optional `sitemapIndex` query parameter) returns `{"sitemap": [...]}` and `GET /sites/{siteUrl}/sitemaps/{feedpath}` returns one resource with `path`, `lastSubmitted`, `lastDownloaded`, `isPending`, `isSitemapsIndex`, `type` (`sitemap`, `urlList`, `rssFeed`, `atomFeed`, `patternSitemap`, `notSitemap`), `warnings`, `errors`, and `contents[]{type, submitted, indexed}`. The `long` counters are JSON strings. `contents[].indexed` is documented as deprecated and gsc omits it. gsc implements only `list` and `get`; `submit` and `delete` are never called.

## What is not exposed by this API

Do not imply support for UI-only features. Notable examples include capabilities/reports not represented in the official API surface, such as:

- live URL test
- ordinary-page manual "Request indexing"
- full Crawl Stats report
- full Page Indexing UI report
- Manual Actions UI report
- Security Issues UI report
- Links UI report
- Removals UI
- the complete Search Console web interface

Check this list against current official documentation when Google changes the API.

## Indexing API is separate

Google's Indexing API is not a generic way to request indexing for arbitrary pages. It is intended for specific supported content types such as qualifying job posting and livestream/broadcast pages under Google's documented rules.

SearchProbe must not expose a misleading general-purpose `gsc index <url>` command backed by the Indexing API.

## Large-site / complete-data considerations

For very large properties where a user needs a bulk, durable dataset rather than interactive API queries, Search Console's BigQuery bulk export may be the appropriate Google-supported mechanism.

SearchProbe does not integrate with BigQuery.

## Error taxonomy to normalize

Stable internal codes include:

```text
AUTH_REQUIRED
AUTH_REVOKED
AUTH_SCOPE_INSUFFICIENT
AUTH_FAILED
PROPERTY_ACCESS_DENIED
PROPERTY_NOT_FOUND
SITEMAP_NOT_FOUND
PAGINATION_INCOMPLETE
INVALID_ARGUMENT
INVALID_DATE_RANGE
INVALID_DIMENSION_COMBINATION
URL_OUTSIDE_PROPERTY
QUOTA_EXCEEDED
RATE_LIMITED
GOOGLE_API_ERROR
NETWORK_ERROR
CONFIG_ERROR
INTERNAL_ERROR
```

Current mapping from Google responses (see `internal/searchconsole/errors.go`): HTTP 401 → `AUTH_REVOKED`; 403 with `insufficientPermissions` → `AUTH_SCOPE_INSUFFICIENT`; `accessNotConfigured`/`SERVICE_DISABLED` → `CONFIG_ERROR` (enable the API and retry); other 403 → `PROPERTY_ACCESS_DENIED` (Google returns 403, not 404, for properties the account cannot see); 404 → `PROPERTY_NOT_FOUND` (or `SITEMAP_NOT_FOUND` from `gsc sitemap`); 429 or `rateLimitExceeded` → `RATE_LIMITED`; `quotaExceeded`/`RESOURCE_EXHAUSTED` → `QUOTA_EXCEEDED`; 400 → one of the `INVALID_*`/`URL_OUTSIDE_PROPERTY` codes by message; 5xx → `GOOGLE_API_ERROR` (retryable). Google's message text is preserved inside `error.message`.

Do not simply surface opaque Google error strings as the only machine-readable failure contract.

## Normalization rule

When behavior is ambiguous, prefer:

1. preserving Google's raw semantics internally;
2. adding explicit normalized metadata;
3. warning rather than guessing;
4. linking/documenting the relevant upstream limitation.

The job of SearchProbe is to make the API easier to use, not to pretend the upstream data is more complete or precise than it is.
