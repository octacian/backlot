"""Application evidence assertions; no Backlot runtime or archive parser code."""
import io
import json
from pathlib import Path
import sys
import zipfile

PNG = b"\x89PNG\r\n\x1a\n"


def reject_constant(value):
    raise ValueError(f"non-JSON constant: {value}")


def verify_file(name, data):
    """Check original media and ZIP entries using the formats Playwright writes."""
    if name.endswith(".png") and not data.startswith(PNG):
        raise ValueError(f"invalid PNG: {name}")
    if name.endswith((".trace", ".network", ".ndjson")):
        for number, line in enumerate(data.splitlines(), 1):
            try:
                json.loads(line.decode("utf-8"), parse_constant=reject_constant)
            except (ValueError, UnicodeError) as error:
                raise ValueError(f"invalid NDJSON: {name}:{number}") from error
    if name.lower().endswith(".zip"):
        # Read through zipfile so compression, entry boundaries and CRCs are
        # checked. Never extract or reinterpret ZIP bytes as a text stream.
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            for entry in archive.infolist():
                if not entry.is_dir():
                    verify_file(f"{name}/{entry.filename}", archive.read(entry))


def verify_tree(root):
    for path in sorted(Path(root).rglob("*")):
        if path.is_file():
            verify_file(path.name, path.read_bytes())


if __name__ == "__main__":
    verify_tree(sys.argv[1])
