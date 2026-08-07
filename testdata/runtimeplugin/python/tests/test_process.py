from __future__ import annotations

import base64
import hashlib
import hmac
import json
import os
import secrets
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

import grpc
from spice.agent.common.v1 import common_pb2
from spice.agent.plugin.v1 import plugin_pb2, plugin_pb2_grpc
from spice_agent_python_fixture import protocol
from spice_agent_python_fixture.main import grpc_address
from spice_agent_python_fixture.service import ECHO_TOOL, WAIT_TOOL


class ProcessTests(unittest.TestCase):
    def test_invalid_bootstrap_writes_only_safe_stderr(self) -> None:
        encoded_secret = base64.urlsafe_b64encode(b"s" * 32).rstrip(b"=")
        process = subprocess.run(
            [sys.executable, "-m", "spice_agent_python_fixture.main"],
            input=(
                b'{"address":"invalid","secret":"'
                + encoded_secret
                + b'","unexpected":true}\n'
            ),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
            timeout=5,
        )
        self.assertEqual(1, process.returncode)
        self.assertEqual(b"", process.stdout)
        self.assertNotEqual(b"", process.stderr)
        self.assertNotIn(encoded_secret, process.stderr)

    def test_real_local_grpc_process_lifecycle(self) -> None:
        secret = secrets.token_bytes(protocol.HANDSHAKE_SECRET_BYTES)
        address, cleanup = _endpoint()
        process = subprocess.Popen(
            [sys.executable, "-m", "spice_agent_python_fixture.main"],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        try:
            assert process.stdin is not None
            assert process.stdout is not None
            assert process.stderr is not None
            bootstrap = {
                "address": address,
                "secret": base64.urlsafe_b64encode(secret).rstrip(b"=").decode("ascii"),
            }
            process.stdin.write(json.dumps(bootstrap).encode("utf-8") + b"\n")
            process.stdin.close()
            self.assertEqual(b'{"ready":true}\n', process.stdout.readline())

            channel = grpc.insecure_channel(
                grpc_address(address),
                options=(
                    ("grpc.max_receive_message_length", protocol.MAXIMUM_MESSAGE_BYTES),
                    ("grpc.max_send_message_length", protocol.MAXIMUM_MESSAGE_BYTES),
                    ("grpc.enable_retries", 0),
                ),
            )
            try:
                grpc.channel_ready_future(channel).result(timeout=5)
                client = plugin_pb2_grpc.PluginServiceStub(channel)
                request = _initialize_request()
                response = client.Initialize(request, timeout=3)
                _verify_proof(request, response, secret)
                self.assertEqual(common_pb2.ERROR_CODE_OK, response.status.code)
                self.assertEqual(
                    ["conformance.echo", "conformance.fail", "conformance.wait"],
                    [definition.name for definition in response.manifest.tools],
                )

                echo = list(
                    client.Execute(
                        plugin_pb2.ExecuteRequest(
                            session_id=response.session_id,
                            call_id="process-echo",
                            tool_name=ECHO_TOOL,
                            arguments_json=b'{"value":"hello"}',
                        ),
                        timeout=3,
                    )
                )
                self.assertEqual([1, 2], [frame.sequence for frame in echo])
                self.assertEqual("progress", echo[0].WhichOneof("frame"))
                self.assertEqual("result", echo[1].WhichOneof("frame"))

                waiting = client.Execute(
                    plugin_pb2.ExecuteRequest(
                        session_id=response.session_id,
                        call_id="process-wait",
                        tool_name=WAIT_TOOL,
                        arguments_json=b"{}",
                    )
                )
                self.assertEqual("progress", next(waiting).WhichOneof("frame"))
                with self.assertRaises(grpc.RpcError) as drain_error:
                    client.Drain(
                        plugin_pb2.DrainRequest(session_id=response.session_id),
                        timeout=0.1,
                    )
                self.assertEqual(grpc.StatusCode.DEADLINE_EXCEEDED, drain_error.exception.code())
                waiting.cancel()
                deadline = time.monotonic() + 3
                while True:
                    try:
                        drained = client.Drain(
                            plugin_pb2.DrainRequest(session_id=response.session_id),
                            timeout=0.25,
                        )
                        break
                    except grpc.RpcError as error:
                        if error.code() != grpc.StatusCode.DEADLINE_EXCEEDED or time.monotonic() >= deadline:
                            raise
                self.assertEqual(common_pb2.ERROR_CODE_OK, drained.status.code)
                closed = client.Shutdown(
                    plugin_pb2.ShutdownRequest(session_id=response.session_id), timeout=3
                )
                self.assertEqual(common_pb2.ERROR_CODE_OK, closed.status.code)
            finally:
                channel.close()

            self.assertEqual(0, process.wait(timeout=5))
            self.assertEqual(b"", process.stdout.read())
            diagnostics = process.stderr.read()
            self.assertNotIn(bootstrap["secret"].encode("ascii"), diagnostics)
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=5)
            for stream in (process.stdin, process.stdout, process.stderr):
                if stream is not None and not stream.closed:
                    stream.close()
            cleanup()


def _initialize_request() -> plugin_pb2.InitializeRequest:
    request = plugin_pb2.InitializeRequest(
        protocol=common_pb2.ProtocolRange(
            minimum=common_pb2.ProtocolVersion(major=1),
            maximum=common_pb2.ProtocolVersion(major=1),
        ),
        host=plugin_pb2.BuildIdentity(
            component="python-self-test-host",
            version="v1",
            commit="fixture",
            runtime="python3.12",
        ),
        supported_capabilities=common_pb2.CapabilitySet(names=["runtime-tools-v1"]),
        required_capabilities=common_pb2.CapabilitySet(names=["runtime-tools-v1"]),
        requested_limits=protocol.fixture_limits(),
        launch_id=secrets.token_bytes(protocol.LAUNCH_ID_BYTES),
        handshake_challenge=secrets.token_bytes(protocol.HANDSHAKE_CHALLENGE_BYTES),
    )
    protocol.add_unknown_bytes(request, 127, b"future-compatible")
    return request


def _verify_proof(
    request: plugin_pb2.InitializeRequest,
    response: plugin_pb2.InitializeResponse,
    secret: bytes,
) -> None:
    unsigned = plugin_pb2.InitializeResponse()
    unsigned.CopyFrom(response)
    unsigned.ClearField("handshake_proof")
    transcript = protocol.canonical_initialize_transcript(request, unsigned)
    expected = hmac.new(secret, transcript, hashlib.sha256).digest()
    if not hmac.compare_digest(expected, response.handshake_proof):
        raise AssertionError("plugin initialize proof does not match")


def _endpoint():
    name = "spice-agent-python-fixture-" + secrets.token_hex(8)
    directory = tempfile.TemporaryDirectory(prefix="spice-python-fixture-")
    if os.name != "nt":
        os.chmod(directory.name, 0o700)
    return str(Path(directory.name) / "plugin.sock"), directory.cleanup


if __name__ == "__main__":
    unittest.main()
