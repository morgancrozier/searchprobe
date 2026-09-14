#!/usr/bin/env python3
"""Build allowlisted release assets locally; never tag or publish. Needs Go/Python."""
import gzip
import hashlib
import io
import os
import platform
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile

RUNTIME_LICENSES = [
    ("github.com/google/jsonschema-go@v0.4.3", "LICENSE"),
    ("github.com/keybase/go-keychain@v0.0.1", "LICENSE"),
    ("github.com/modelcontextprotocol/go-sdk@v1.7.0", "LICENSE"),
    ("github.com/segmentio/asm@v1.1.3", "LICENSE"),
    ("github.com/segmentio/encoding@v0.5.4", "LICENSE"),
    ("github.com/spf13/cobra@v1.8.1", "LICENSE.txt"),
    ("github.com/spf13/pflag@v1.0.5", "LICENSE"),
    ("github.com/yosida95/uritemplate/v3@v3.0.2", "LICENSE"),
    ("github.com/zalando/go-keyring@v0.2.6", "LICENSE"),
    ("github.com/godbus/dbus/v5@v5.1.0", "LICENSE"),
    ("golang.org/x/oauth2@v0.35.0", "LICENSE"),
    ("golang.org/x/sync@v0.20.0", "LICENSE"),
    ("golang.org/x/sys@v0.41.0", "LICENSE"),
    ("golang.org/x/time@v0.15.0", "LICENSE"),
]


def build_notices():
    template = '{{with .Module}}{{if ne .Path "github.com/morgancrozier/searchprobe"}}{{.Path}}@{{.Version}}{{end}}{{end}}'
    runtime_modules = set()
    for system, arch, cgo in (("darwin", "arm64", "1"), ("linux", "amd64", "0")):
        env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED=cgo, GOFLAGS="")
        output = subprocess.check_output(
            ["go", "list", "-mod=readonly", "-deps", "-f", template, "./cmd/gsc"],
            env=env, text=True)
        runtime_modules.update(line for line in output.splitlines() if line)
    declared_modules = {module for module, _ in RUNTIME_LICENSES}
    if runtime_modules != declared_modules:
        missing = sorted(runtime_modules - declared_modules)
        stale = sorted(declared_modules - runtime_modules)
        sys.exit(f"Runtime dependency license list is stale; missing={missing}, stale={stale}")

    go_env = subprocess.check_output(
        ["go", "env", "GOROOT", "GOMODCACHE"], text=True).splitlines()
    go_root, module_cache = map(Path, go_env)
    go_license = next((path for path in
                       (go_root / "LICENSE", go_root.parent / "LICENSE")
                       if path.is_file()), None)
    if go_license is None:
        sys.exit("Missing Go standard library license")
    go_version = subprocess.check_output(["go", "version"], text=True).strip()
    parts = [
        b"SearchProbe third-party software notices\n\n",
        f"===== Go standard library ({go_version}) =====\n\n".encode("utf-8"),
        go_license.read_bytes().rstrip() + b"\n\n",
    ]
    for module, filename in RUNTIME_LICENSES:
        license_path = module_cache / module / filename
        if not license_path.is_file():
            sys.exit(f"Missing license for runtime dependency: {module}")
        parts.extend([
            f"===== {module} =====\n\n".encode("utf-8"),
            license_path.read_bytes().rstrip() + b"\n\n",
        ])
    return b"".join(parts)


def main():
    if len(sys.argv) != 2 or not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9]+(?:[.-][a-zA-Z0-9]+)*)?", sys.argv[1]):
        sys.exit("Usage: python3 scripts/release.py vMAJOR.MINOR.PATCH[-prerelease]")
    tag = sys.argv[1]
    root = Path(__file__).resolve().parent.parent
    # Refuse to mix a build with stale artifacts or overwrite previous output.
    out = root / "dist" / tag
    out.mkdir(parents=True, exist_ok=False)
    if platform.system() != "Darwin":
        sys.exit("Release bundles require a macOS host with Xcode command-line tools for native Keychain support.")
    notices_path = out / "THIRD_PARTY_NOTICES.txt"
    notices_path.write_bytes(build_notices())
    checksums = []
    for system, arch in [("darwin", "arm64"), ("darwin", "amd64"),
                         ("linux", "amd64"), ("linux", "arm64")]:
        env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED="1" if system == "darwin" else "0", GOFLAGS="")
        with tempfile.TemporaryDirectory(prefix="searchprobe-build-") as tmp:
            binary = Path(tmp) / "gsc"
            subprocess.run([
                "go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false",
                "-ldflags", f"-s -w -X main.Version={tag}",
                "-o", str(binary), "./cmd/gsc",
            ], cwd=root, env=env, check=True)
            name = f"searchprobe_{tag[1:]}_{system}_{arch}.tar.gz"
            path = out / name
            # Fixed archive metadata and gzip timestamp allow byte-identical rebuilds
            # with the same source, Go toolchain and Python/zlib version.
            with path.open("wb") as raw:
                with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as gz:
                    with tarfile.open(fileobj=gz, mode="w", format=tarfile.USTAR_FORMAT) as tar:
                        for member, source, mode in [("gsc", binary, 0o755),
                                                     ("LICENSE", root / "LICENSE", 0o644),
                                                     ("THIRD_PARTY_NOTICES.txt", notices_path, 0o644)]:
                            data = source.read_bytes()
                            info = tarfile.TarInfo(member)
                            info.size, info.mode, info.mtime = len(data), mode, 0
                            tar.addfile(info, io.BytesIO(data))
            checksums.append(f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {name}\n")
            print(name)
    version_path = out / "VERSION"
    version_path.write_text(tag + "\n", encoding="ascii")
    checksums.append(f"{hashlib.sha256(notices_path.read_bytes()).hexdigest()}  THIRD_PARTY_NOTICES.txt\n")
    checksums.append(f"{hashlib.sha256(version_path.read_bytes()).hexdigest()}  VERSION\n")
    # Supporting files are release assets too. This script performs no network or
    # publishing operations.
    for name, source in [("install.sh", root / "scripts/install.sh"),
                         ("SKILL.md", root / "skills/searchprobe/SKILL.md"),
                         ("GOOGLE_SETUP.md", root / "docs/GOOGLE_SETUP.md"),
                         ("START_HERE.md", root / "docs/INSTALL.md")]:
        data = source.read_bytes()
        if name == "START_HERE.md":
            text = data.decode("utf-8")
            for doc in ["AGENT_INTEGRATION.md"]:
                text = text.replace(f"]({doc})", f"](https://github.com/morgancrozier/searchprobe/blob/main/docs/{doc})")
            data = text.encode("utf-8")
        (out / name).write_bytes(data)
        checksums.append(f"{hashlib.sha256(data).hexdigest()}  {name}\n")
    (out / "checksums.txt").write_text("".join(checksums), encoding="ascii")


if __name__ == "__main__":
    main()
