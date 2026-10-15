#!/usr/bin/env python3
"""Build server archives and a Figma plugin package from the same source."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile
import zipfile


ROOT = Path(__file__).resolve().parents[1]
TARGETS = [(system, arch) for system in ("darwin", "linux", "windows")
           for arch in ("amd64", "arm64")]


def run(command, **kwargs):
    subprocess.run(command, cwd=ROOT, check=True, **kwargs)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--output", type=Path, default=ROOT / "dist" / "release")
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", args.version):
        parser.error("version must contain only letters, digits, dots, underscores, or hyphens")
    output = args.output.resolve()
    # An empty directory prevents old packages from entering a new release.
    if output.exists() and any(output.iterdir()):
        parser.error(f"output directory is not empty: {output}")

    run(["npm", "ci", "--prefix", "plugin"])
    run(["npm", "run", "build", "--prefix", "plugin"])

    manifest = json.loads((ROOT / "plugin" / "manifest.json").read_text())
    for key in ("main", "ui"):
        if not (ROOT / "plugin" / manifest[key]).is_file():
            raise RuntimeError(f"plugin file is missing: {manifest[key]}")

    with tempfile.TemporaryDirectory(prefix="figma-lean-release-") as temporary:
        staging = Path(temporary)
        packages = staging / "packages"
        packages.mkdir()
        for system, arch in TARGETS:
            name = f"figma-lean-mcp_{system}_{arch}"
            folder = staging / name
            folder.mkdir()
            executable = "figma-lean-mcp.exe" if system == "windows" else "figma-lean-mcp"
            run([
                "go", "build", "-trimpath", "-buildvcs=false",
                "-ldflags", f"-s -w -X main.version={args.version}",
                "-o", str(folder / executable), "./cmd/figma-lean-mcp",
            ], env={**os.environ, "CGO_ENABLED": "0", "GOOS": system, "GOARCH": arch})
            shutil.copy2(ROOT / "LICENSE", folder / "LICENSE")
            (folder / "VERSION").write_text(args.version + "\n")
            if system == "windows":
                with zipfile.ZipFile(packages / f"{name}.zip", "w", zipfile.ZIP_DEFLATED) as archive:
                    for item in sorted(folder.iterdir()):
                        archive.write(item, item.name)
            else:
                with tarfile.open(packages / f"{name}.tar.gz", "w:gz") as archive:
                    for item in sorted(folder.iterdir()):
                        archive.add(item, arcname=item.name)

        with zipfile.ZipFile(packages / "figma-lean-mcp-plugin.zip", "w", zipfile.ZIP_DEFLATED) as archive:
            for name in ("manifest.json", manifest["main"], manifest["ui"]):
                archive.write(ROOT / "plugin" / name, f"figma-lean-mcp-plugin/{name}")
            archive.write(ROOT / "LICENSE", "figma-lean-mcp-plugin/LICENSE")
            archive.writestr("figma-lean-mcp-plugin/VERSION", args.version + "\n")

        checksums = []
        for item in sorted(packages.iterdir()):
            digest = hashlib.sha256(item.read_bytes()).hexdigest()
            checksums.append(f"{digest}  {item.name}\n")
        (packages / "SHA256SUMS").write_text("".join(checksums))
        output.mkdir(parents=True, exist_ok=True)
        for item in packages.iterdir():
            shutil.copy2(item, output / item.name)
    print(f"Release packages: {output}")


if __name__ == "__main__":
    main()
