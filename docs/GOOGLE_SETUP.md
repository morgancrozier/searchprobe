# Connect your Google Cloud project

SearchProbe requires your own **Google Search Console API** Desktop OAuth client.
An API key is not sufficient. The Google account you sign in with must already
have access to your properties in [Search Console](https://search.google.com/search-console).
Creating a Cloud project does not create or verify a Search Console property.

1. Open [Google Cloud Console](https://console.cloud.google.com/) and create or select your project.
2. Enable the [Google Search Console API](https://console.cloud.google.com/apis/library/searchconsole.googleapis.com) in that project.
3. Open Google Auth Platform. Configure the app name, support email and developer contact for your personal application.
4. Choose an audience. For a personal Google account, use **External** and set publishing status to **In production** before connecting. This is a personal-use app, not a listing in an app store. Publishing does not restrict authorization to your email address or grant anyone access to your Search Console properties.
5. For an eligible Google Workspace or Cloud Identity organization project, **Internal** restricts the audience to that organization, not one address. Administrator policy can still block authorization. Use External if your GSC account is outside that organization.
6. In Data Access, configure only `https://www.googleapis.com/auth/webmasters.readonly`.
7. In Clients, create an OAuth client of type **Desktop app** and download its JSON. Do not choose Web application, API key, or service account.
8. Import and connect in a terminal on the machine where SearchProbe runs:

```sh
"$HOME/.local/share/searchprobe/bin/gsc" setup --client-file "$HOME/Downloads/client_secret_example.json"
```

The command opens Google consent, saves your client and tokens, checks property
access, and prepares your selected Claude Code/Codex skills. After SearchProbe
reports credentials saved and verified, you can delete the downloaded JSON.
It is not needed for later commands, token refresh, or reauthentication:

```sh
gsc auth login
gsc setup
```

SearchProbe never deletes your download. Do not commit it or paste its contents
into a chat. Import a different client with `--client-file`; failed login leaves
the previous saved grant intact. Logout removes both the saved client and tokens,
so a later fresh setup requires a new download/import.

## Publishing and token lifetime

External apps left in **Testing** generally receive refresh tokens that expire
after seven days for this scope; add your account as a test user if temporarily
using Testing. Set In production before the initial connection to avoid that
Testing limit. If you already connected in Testing, sign in again after publishing.
Production tokens can still expire or be revoked. Personal-use verification
exceptions and organization rules depend on Google's policies; follow the exact
Google error rather than assuming publishing resolves every restriction.

See Google's [audience rules](https://developers.google.com/identity/protocols/oauth2/production-readiness/overview),
[verification exceptions](https://support.google.com/cloud/answer/13464323?hl=en), and
[token expiration documentation](https://developers.google.com/identity/protocols/oauth2#expiration).

## Local credential storage

SearchProbe prefers macOS Keychain or Linux Secret Service. The `keychain` option
means the platform OS credential store on either system. Linux needs a running,
unlocked Secret Service provider such as GNOME Keyring in the current D-Bus session.
Secrets are stored locally, not in SearchProbe servers or agent skill files.

If OS storage is unavailable, setup asks before using a plaintext file protected
by user-only filesystem permissions. For an explicit choice:

```sh
gsc setup --credential-store keychain
gsc setup --credential-store file
```

File storage uses `~/.config/gsc/credentials.json` with mode `0600` in a `0700`
directory. `GSC_CONFIG_DIR`, then `XDG_CONFIG_HOME`, override the config location.
File permissions do not encrypt the data. Protect local accounts and backups.
OS-backed storage keeps only backend metadata and a record identifier in
`storage.json`; the downloaded client and tokens remain in the OS store.
Alternate config directories use separate credential records.

`auto` reuses a saved choice and never silently downgrades an OS-backed record.
Noninteractive runs must explicitly select `--credential-store file` when OS
storage is unavailable. `gsc setup --agent all --json` requires prior working
authentication and never starts browser consent. A locked store must be unlocked
to access/migrate an existing record.

`gsc auth status --json` reports `credentialStore` and non-secret metadata;
`credentialsPath` is null for OS storage. It checks local state, not live access.
`gsc auth logout` attempts revocation and removes the managed client and tokens.
Read errors and warnings before uninstalling.

## Agent sandbox access

An agent needs permission to execute SearchProbe, contact Google and access the
selected credential backend. Codex read-only sandbox mode may block macOS
Keychain even when normal terminal commands work. Authorize the specific CLI
operation outside that sandbox through your agent's permission flow, or explicitly
choose file storage if it fits your environment. Never ask the agent to extract
Keychain items or read credentials directly. File fallback does not grant network
access through an agent sandbox.
