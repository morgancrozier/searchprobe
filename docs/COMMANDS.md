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
```

Use `gsc <command> --help` for options and examples.
