"""Validate exported evidence, including the contents of Playwright ZIP traces."""
import io
import json
from pathlib import Path, PurePosixPath
import re
import sys
import zipfile

PRIVATE_KEY = re.compile(rb"-----BEGIN (?:[A-Z0-9 ]* )?PRIVATE KEY-----")


def check(name, data, depth=0):
    if PRIVATE_KEY.search(data) or name.lower().endswith((".key", ".p12", ".pfx")):
        raise ValueError("private key rejected: " + name)
    if depth > 2:
        raise ValueError("nested evidence archive rejected")
    if name.lower().endswith(".zip"):
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            for item in archive.infolist():
                path = PurePosixPath(item.filename)
                if path.is_absolute() or ".." in path.parts or (item.external_attr >> 16) & 0o170000 == 0o120000:
                    raise ValueError("unsafe archive entry")
                if not item.is_dir():
                    check(item.filename, archive.read(item), depth + 1)
    if name.endswith((".trace", ".network", ".ndjson")):
        for line in data.splitlines():
            json.loads(line)
    if name.endswith("playwright.json"):
        report = json.loads(data)
        assert isinstance(report["suites"], list) and isinstance(report["stats"], dict)
    if name.endswith(".png"):
        assert data.startswith(b"\x89PNG\r\n\x1a\n"), "invalid PNG"


if __name__ == "__main__":
    for path in Path(sys.argv[1]).rglob("*"):
        if path.is_symlink():
            raise ValueError("evidence link rejected")
        if path.is_file():
            check(path.name, path.read_bytes())
    print("Evidence audit passed: formats and no private keys (including ZIP entries)")
