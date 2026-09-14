# Contributing

Contributions that make SearchProbe safer, clearer, more reliable, or easier to
use from terminals and coding agents are welcome. For substantial changes, open
an issue first so the intended scope and public roadmap can be discussed.

Read [AGENTS.md](AGENTS.md) for the repository's durable engineering constraints,
package structure, Google API semantics, and guidance for coding agents.

## Development setup

Use a maintained Go toolchain supporting Go 1.25 or later.

```sh
make build
make check
sh -n scripts/install.sh
shellcheck scripts/install.sh
python3 scripts/test_install.py
```

`make build` produces `./gsc`. Use that path during development so an installed
program with the same command name is unaffected. macOS Keychain builds require
Xcode command-line tools and CGO. Linux Secret Service integration requires an
unlocked provider in the current D-Bus session.

Native credential-store integration tests are opt-in:

```sh
GSC_TEST_KEYCHAIN=1 go test ./internal/auth -run TestKeychainIntegration -count=1
```

Live Google tests require your own Desktop OAuth client and an isolated config.
Never commit credentials, tokens, Search Console data, or private property
identifiers.

## Pull requests

Keep changes focused and preserve existing behavior unless the change requires
otherwise. In a pull request:

- explain the user-visible problem and resulting behavior;
- add focused tests for behavioral changes;
- update affected documentation;
- preserve the read-only OAuth scope and direct-to-Google architecture;
- keep secrets out of output, logs, fixtures, and error messages;
- treat JSON output and Google API limitations as public contracts.

Run the relevant checks and report anything that could not be verified. See the
[architecture](docs/ARCHITECTURE.md), [API semantics](docs/GSC_API.md), and
[roadmap](docs/ROADMAP.md) for additional context.

Report vulnerabilities privately using [SECURITY.md](SECURITY.md).
