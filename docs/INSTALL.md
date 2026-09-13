# Install SearchProbe

SearchProbe is a native read-only Google Search Console CLI. The command is `gsc`.
The public beta supports macOS and Linux on arm64 and amd64. Prebuilt native
release bundles are the supported installation path; Go is contributor tooling.

## Prepare Google access

First follow [Google setup](GOOGLE_SETUP.md) to create your Desktop OAuth client.
Have the downloaded JSON ready before running setup.

## Native installation

Download the pinned installer from the `v0.1.0-beta.3` GitHub prerelease. The
installer then downloads the checksum manifest and the one matching archive:

```sh
curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location \
  https://github.com/morgancrozier/searchprobe/releases/download/v0.1.0-beta.3/install.sh \
  -o install-searchprobe.sh
sh ./install-searchprobe.sh --version v0.1.0-beta.3
"$HOME/.local/share/searchprobe/bin/gsc" setup --client-file "$HOME/Downloads/client.json"
```

Replace the JSON path with your download. Release archives are named
`searchprobe_0.1.0-beta.3_<darwin|linux>_<arm64|amd64>.tar.gz`. The installer
selects the correct archive and refuses missing, duplicate, malformed, or
mismatched checksums.

The installer verifies SHA256 and installs in a user-owned directory without
sudo or shell-profile edits. It needs standard shell utilities, tar, and shasum
(macOS) or sha256sum (Linux); HTTPS downloads also need curl. Binaries are not
Developer ID signed/notarized. Use OS per-app approval if required; do not disable
system-wide protections. Windows is unsupported.
Release archives also carry the project license and third-party software notices.

Package installation installs only the binary. Setup is an explicit next step
that configures Google access and the selected agent skills.

## Google and agent setup

Follow [Google setup](GOOGLE_SETUP.md) to create your Cloud project, enable the
Search Console API, configure the audience and download a Desktop OAuth client.
Run `gsc setup` interactively to enter the download path, or provide it directly:

```sh
"$HOME/.local/share/searchprobe/bin/gsc" setup --client-file "$HOME/Downloads/client.json"
```

Setup shows Claude Code/Codex skill destinations, opens Google consent, verifies
credential persistence, checks properties and installs the selected bundled skills.
After credentials are saved and verified, delete the downloaded JSON if desired.
Future sign-ins reuse the managed client; no copy in Downloads is required.

OS storage uses macOS Keychain or Linux Secret Service. If unavailable, setup asks
before selecting plaintext file storage protected by filesystem permissions.
`--credential-store file` explicitly selects it; `--credential-store keychain`
selects the OS store. `auto` reuses a configured choice. See the Google guide for
storage locations, migration, token lifetimes and headless Linux requirements.

Use `--agent all|claude|codex|none` to select explicitly. Existing modified skills
are preserved, unchanged skills are not rewritten and managed old skills update.
Rerun setup after interruptions. For scripting, `gsc setup --agent all --json`
requires existing authentication and never starts Google consent. `--no-browser`
prints the consent URL but still requires a callback on the same machine.

## PATH and other gsc programs

Ghostscript also provides a `gsc` executable. The installer never overwrites or
deletes another package's binary. Its ownership receipt guards updates/removal.
Use the absolute SearchProbe path above to preserve Ghostscript's PATH priority.
Otherwise add SearchProbe to the current shell:

```sh
export PATH="$HOME/.local/share/searchprobe/bin:$PATH"
hash -r
command -v gsc
gsc --version
```

Add the export to your shell startup file yourself if desired. Restart Claude
Code/Codex after PATH changes so its shell can find SearchProbe. Setup verifies
Google access and skill files, not activation inside the agent. Start a fresh
agent session in your website repository and use the suggested first prompt.
See [agent integration](AGENT_INTEGRATION.md).

## Update and removal

Update by downloading the installer from a newer release and rerunning it with
that pinned version.
Credentials are retained. Run `gsc setup` afterward to update managed skills.
There are no automatic updates.

Before removing SearchProbe:

```sh
"$HOME/.local/share/searchprobe/bin/gsc" agent uninstall
"$HOME/.local/share/searchprobe/bin/gsc" auth logout
sh ./install.sh --uninstall
```

Logout attempts Google revocation and removes the saved client and tokens from
the selected store. Read any warnings/errors; uninstall alone preserves credentials.
Remove any PATH line you added yourself. Unrelated agent skills and other packages'
binaries are preserved. A later fresh login needs another client JSON import.
