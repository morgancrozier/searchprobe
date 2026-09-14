# Command reference

```text
gsc setup [--agent auto|all|claude|codex|none] [--client-file <path>]
    [--credential-store auto|keychain|file]

gsc auth login [--client-file <oauth-client.json>] [--credential-store auto|keychain|file]
gsc auth status
gsc auth logout

gsc agent install|status|uninstall [--agent auto|all|claude|codex] [--json]

gsc sites [--match <text>] [--json]

gsc performance --site <property> [--json]
    --days <n> | --start YYYY-MM-DD --end YYYY-MM-DD
    --dimensions query,page,country,device,date,searchAppearance,hour
    --type web|image|video|news|discover|googleNews
    --data-state final|all|hourly_all
    --filter "<dimension> <operator> <expression>"   (repeatable, ANDed)
    --aggregation auto|byPage|byProperty|byNewsShowcasePanel
    --limit <n> --start-row <n> | --all

gsc compare --site <property> [--json]
    (same query flags as performance, minus --limit/--start-row/--all)
    --previous | --compare-start YYYY-MM-DD --compare-end YYYY-MM-DD
    --limit <n>                                      (joined rows to emit, 0 = all)
    --sort current-clicks|current-impressions|clicks-delta|impressions-delta|position-delta [--asc]

gsc inspect <url> --site <property> [--json]
gsc inspect --urls-file <path|-> --site <property> [--json]

gsc sitemaps --site <property> [--index <sitemap-index-url>] [--json]
gsc sitemap <sitemap-url> --site <property> [--json]

gsc mcp
```

Use `gsc <command> --help` for options and examples.

## MCP server

`gsc mcp` runs a local Model Context Protocol server over stdin and stdout for
the lifetime of one MCP client session. The client can discover six structured,
read-only tools corresponding to the commands above: `sites`, `performance`,
`compare`, `inspect`, `sitemaps`, and `sitemap`.

The server uses credentials previously created by `gsc setup` and connects
directly to Google's Search Console API. MCP mode never starts browser login. If
credentials are missing, revoked, or insufficient, the tool result instructs the
user to run `gsc setup` in a terminal. SearchProbe does not open a network port,
run a background service, or configure an MCP client automatically.

stdout is reserved for MCP protocol messages. Human diagnostics, if any, use
stderr. Tool results are structured as `data`, `meta`, and `warnings`; failures
carry SearchProbe's normalized error code and recovery action.
