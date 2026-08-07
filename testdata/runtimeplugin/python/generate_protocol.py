"""Regenerate Python bindings only when the canonical schema hashes match."""

from __future__ import annotations

import hashlib
import json
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
PROTO_ROOT = ROOT / "proto"
LOCK = HERE / "protocol-lock.json"


def main() -> int:
    expected = json.loads(LOCK.read_text(encoding="utf-8"))
    inputs = []
    for relative, digest in sorted(expected.items()):
        source = ROOT / relative
        observed = hashlib.sha256(source.read_bytes()).hexdigest()
        if observed != digest:
            raise SystemExit(f"schema hash mismatch: {relative}")
        inputs.append(str(source))
    command = [
        sys.executable,
        "-m",
        "grpc_tools.protoc",
        f"-I{PROTO_ROOT}",
        f"--python_out={HERE / 'src'}",
        f"--grpc_python_out={HERE / 'src'}",
        *inputs,
    ]
    return subprocess.run(command, check=False).returncode


if __name__ == "__main__":
    raise SystemExit(main())
