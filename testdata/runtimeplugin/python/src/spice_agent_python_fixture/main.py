"""Process boundary for the independent Python plugin fixture."""

from __future__ import annotations

import asyncio
import base64
import json
import os
import sys
from dataclasses import dataclass

import grpc
from spice.agent.plugin.v1 import plugin_pb2_grpc

from . import protocol
from .service import PluginService

MAXIMUM_BOOTSTRAP_BYTES = 4096


@dataclass(frozen=True)
class Bootstrap:
    address: str
    secret: bytearray


def decode_bootstrap(content: bytes) -> Bootstrap:
    if not content or len(content) > MAXIMUM_BOOTSTRAP_BYTES:
        raise ValueError("fixture bootstrap is empty or exceeds its byte limit")
    try:
        value = json.loads(content)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ValueError("fixture bootstrap is invalid") from error
    if not isinstance(value, dict) or set(value) != {"address", "secret"}:
        raise ValueError("fixture bootstrap fields are invalid")
    if not isinstance(value["address"], str) or not value["address"]:
        raise ValueError("fixture bootstrap address is invalid")
    if not isinstance(value["secret"], str):
        raise ValueError("fixture bootstrap secret is invalid")
    try:
        padding = "=" * (-len(value["secret"]) % 4)
        secret = bytearray(base64.b64decode(value["secret"] + padding, altchars=b"-_", validate=True))
    except (ValueError, base64.binascii.Error) as error:
        raise ValueError("fixture bootstrap secret is invalid") from error
    if len(secret) != protocol.HANDSHAKE_SECRET_BYTES:
        for index in range(len(secret)):
            secret[index] = 0
        raise ValueError("fixture bootstrap secret is invalid")
    return Bootstrap(address=value["address"], secret=secret)


def grpc_address(address: str) -> str:
    if os.name == "nt" and address.startswith("\\\\.\\pipe\\"):
        raise ValueError(
            "Python grpcio cannot serve Windows named pipes; use an absolute AF_UNIX path"
        )
    if not os.path.isabs(address):
        raise ValueError("fixture bootstrap address is not an absolute Unix socket")
    return "unix:" + address


async def serve(bootstrap: Bootstrap) -> None:
    stopped = asyncio.Event()
    server = grpc.aio.server(
        options=(
            ("grpc.max_receive_message_length", protocol.MAXIMUM_MESSAGE_BYTES),
            ("grpc.max_send_message_length", protocol.MAXIMUM_MESSAGE_BYTES),
        )
    )

    def request_shutdown() -> None:
        asyncio.create_task(stop_server())

    async def stop_server() -> None:
        await server.stop(grace=1.0)
        stopped.set()

    plugin_pb2_grpc.add_PluginServiceServicer_to_server(
        PluginService(bootstrap.secret, request_shutdown), server
    )
    endpoint = grpc_address(bootstrap.address)
    if server.add_insecure_port(endpoint) != 1:
        raise RuntimeError("fixture could not bind its local IPC endpoint")
    await server.start()
    if os.name != "nt":
        os.chmod(bootstrap.address, 0o600)
    sys.stdout.buffer.write(b'{"ready":true}\n')
    sys.stdout.buffer.flush()
    await stopped.wait()
    await server.wait_for_termination()


def main() -> int:
    bootstrap = None
    try:
        bootstrap = decode_bootstrap(sys.stdin.buffer.read(MAXIMUM_BOOTSTRAP_BYTES + 1))
        asyncio.run(serve(bootstrap))
    except (OSError, RuntimeError, ValueError) as error:
        print(f"spice-agent-python-plugin-fixture: {error}", file=sys.stderr)
        return 1
    finally:
        if bootstrap is not None:
            for index in range(len(bootstrap.secret)):
                bootstrap.secret[index] = 0
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
