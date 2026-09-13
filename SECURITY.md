# Security policy

Report suspected vulnerabilities privately to [morgan@morgancrozier.com](mailto:morgan@morgancrozier.com). Do not open a public issue containing vulnerability details, OAuth client JSON, tokens, or Search Console data.

Include the affected version and operating system, expected and actual behavior, and a minimal reproduction using redacted or synthetic data. Do not send live credentials. The maintainer will coordinate investigation and disclosure by email; no response-time guarantee is currently offered.

SearchProbe is in public beta. Security fixes target the current development
version and latest published beta; older beta releases are not maintained
separately. macOS and Linux are the supported platforms.

SearchProbe uses read-only OAuth and direct Google API requests. OS credential storage is preferred; explicitly selected file storage is plaintext protected by filesystem permissions. See [credential storage](docs/GOOGLE_SETUP.md#local-credential-storage).
