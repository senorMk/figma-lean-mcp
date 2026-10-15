#!/usr/bin/env python3
"""Check release archives and run the native server through MCP initialization."""

import hashlib
import json
from pathlib import Path
import platform
import subprocess
import sys
import tarfile
import tempfile
import zipfile


def main():
    output = Path(sys.argv[1]).resolve()
    version = sys.argv[2]
    expected = {"figma-lean-mcp-plugin.zip"}
    expected.update(
        f"figma-lean-mcp_{system}_{arch}.{extension}"
        for system, extension in (("darwin", "tar.gz"), ("linux", "tar.gz"), ("windows", "zip"))
        for arch in ("amd64", "arm64")
    )
    checksums = {}
    for line in (output / "SHA256SUMS").read_text().splitlines():
        digest, name = line.split("  ", 1)
        checksums[name] = digest
    assert set(checksums) == expected, "checksum list does not match the release archives"
    assert {item.name for item in output.iterdir()} == expected | {"SHA256SUMS"}

    for name, digest in checksums.items():
        assert hashlib.sha256((output / name).read_bytes()).hexdigest() == digest, name
        if name.endswith(".zip"):
            with zipfile.ZipFile(output / name) as archive:
                assert archive.testzip() is None, name
                files = {item: archive.read(item) for item in archive.namelist()}
        else:
            with tarfile.open(output / name) as archive:
                files = {item.name: archive.extractfile(item).read() for item in archive.getmembers()}
                assert archive.getmember("figma-lean-mcp").mode & 0o111, name
        if name == "figma-lean-mcp-plugin.zip":
            prefix = "figma-lean-mcp-plugin/"
            manifest = json.loads(files[prefix + "manifest.json"])
            assert files[prefix + manifest["main"]], "plugin code is empty"
            assert files[prefix + manifest["ui"]], "plugin UI is empty"
            assert files[prefix + "VERSION"].decode().strip() == version
            assert files[prefix + "LICENSE"]
        else:
            executable = "figma-lean-mcp.exe" if "windows" in name else "figma-lean-mcp"
            assert set(files) == {executable, "LICENSE", "VERSION"}, name
            assert files[executable], name
            assert files["VERSION"].decode().strip() == version, name

    system = {"Darwin": "darwin", "Linux": "linux", "Windows": "windows"}[platform.system()]
    arch = {"x86_64": "amd64", "AMD64": "amd64", "arm64": "arm64", "aarch64": "arm64"}[platform.machine()]
    executable = "figma-lean-mcp.exe" if system == "windows" else "figma-lean-mcp"
    with tempfile.TemporaryDirectory(prefix="figma-lean-check-") as temporary:
        folder = Path(temporary)
        binary = folder / executable
        name = f"figma-lean-mcp_{system}_{arch}"
        if system == "windows":
            with zipfile.ZipFile(output / f"{name}.zip") as archive:
                binary.write_bytes(archive.read(executable))
        else:
            with tarfile.open(output / f"{name}.tar.gz") as archive:
                binary.write_bytes(archive.extractfile(executable).read())
        binary.chmod(0o755)
        result = subprocess.run([str(binary), "--version"], check=True, capture_output=True, text=True, timeout=10)
        assert result.stdout.strip() == version, "server version does not match the release"
        tools_file = folder / "tools.json"
        subprocess.run([
            sys.executable, str(Path(__file__).resolve().parents[1] / "bench" / "dump_tools.py"),
            str(tools_file), str(binary), "--port", "0",
        ], check=True, timeout=20)
        assert {tool["name"] for tool in json.loads(tools_file.read_text())} == {
            "run_code", "get_node", "get_document", "get_pages", "save_screenshots",
        }, "server tool list does not match"
    print("All seven archives passed. Native server version and MCP initialization passed.")


if __name__ == "__main__":
    main()
