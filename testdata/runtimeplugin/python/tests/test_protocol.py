from __future__ import annotations

import asyncio
import base64
import hashlib
import hmac
import json
import unittest

import grpc
from spice.agent.common.v1 import common_pb2
from spice.agent.plugin.v1 import plugin_pb2
from spice_agent_python_fixture import protocol
from spice_agent_python_fixture.main import decode_bootstrap, grpc_address
from spice_agent_python_fixture.service import ECHO_TOOL, WAIT_TOOL, PluginService

INITIALIZE_TRANSCRIPT_GOLDEN_SHA256 = (
    "c041c506079f6e7a4d8b60fe29c6f050e43007390db433f421508b8acbc59e20"
)


class FakeContext:
    def __init__(self) -> None:
        self.code = None
        self.details = None

    def set_code(self, code) -> None:
        self.code = code

    def set_details(self, details: str) -> None:
        self.details = details

    async def abort(self, code, details: str):
        self.code = code
        self.details = details
        raise FakeAbortError


class FakeAbortError(Exception):
    pass


def initialize_request() -> plugin_pb2.InitializeRequest:
    request = plugin_pb2.InitializeRequest(
        protocol=common_pb2.ProtocolRange(
            minimum=common_pb2.ProtocolVersion(major=1),
            maximum=common_pb2.ProtocolVersion(major=1),
        ),
        host=plugin_pb2.BuildIdentity(
            component="host", version="v1", commit="test", runtime="go1.26.5"
        ),
        supported_capabilities=common_pb2.CapabilitySet(names=["runtime-tools-v1"]),
        required_capabilities=common_pb2.CapabilitySet(names=["runtime-tools-v1"]),
        requested_limits=protocol.fixture_limits(),
        launch_id=b"l" * 16,
        handshake_challenge=b"c" * 32,
    )
    protocol.add_unknown_bytes(request, 127, b"future-compatible")
    return request


class ProtocolTests(unittest.TestCase):
    def test_bootstrap_is_strict_and_secret_remains_binary(self) -> None:
        encoded = base64.urlsafe_b64encode(b"s" * 32).rstrip(b"=")
        bootstrap = decode_bootstrap(
            b'{"address":"\\\\\\\\.\\\\pipe\\\\fixture","secret":"' + encoded + b'"}'
        )
        self.assertEqual(32, len(bootstrap.secret))
        with self.assertRaises(ValueError):
            decode_bootstrap(b'{"address":"x","secret":"eA","extra":true}')

    def test_platform_address_is_local(self) -> None:
        import os

        if os.name == "nt":
            self.assertEqual("unix:D:\\Temp\\fixture.sock", grpc_address(r"D:\Temp\fixture.sock"))
            with self.assertRaises(ValueError):
                grpc_address(r"\\.\pipe\fixture")
        else:
            self.assertEqual("unix:/tmp/fixture", grpc_address("/tmp/fixture"))

    def test_hmac_is_over_complete_canonical_transcript(self) -> None:
        request = initialize_request()
        unsigned = plugin_pb2.InitializeResponse(
            status=protocol.ok_status(),
            protocol=common_pb2.ProtocolVersion(major=1),
            plugin=plugin_pb2.BuildIdentity(
                component="fixture", version="v1", commit="test", runtime="python3.12"
            ),
            capabilities=common_pb2.CapabilitySet(names=["runtime-tools-v1"]),
            limits=protocol.fixture_limits(),
            launch_id=request.launch_id,
            session_id=b"s" * 16,
            handshake_challenge=request.handshake_challenge,
        )
        secret = bytearray(b"k" * 32)
        signed = protocol.sign_initialize(request, unsigned, secret)
        expected = hmac.new(
            secret,
            protocol.canonical_initialize_transcript(request, unsigned),
            hashlib.sha256,
        ).digest()
        self.assertEqual(expected, signed.handshake_proof)

    def test_canonical_transcript_matches_cross_language_golden(self) -> None:
        request, response = golden_initialize_transcript()
        transcript = protocol.canonical_initialize_transcript(request, response)
        self.assertEqual(
            INITIALIZE_TRANSCRIPT_GOLDEN_SHA256,
            hashlib.sha256(transcript).hexdigest(),
        )
        response.handshake_proof = b"changed-proof-does-not-self-authenticate"
        self.assertEqual(
            transcript,
            protocol.canonical_initialize_transcript(request, response),
        )

    def test_canonical_transcript_preserves_unknown_occurrence_order(self) -> None:
        request, response = golden_initialize_transcript()
        canonical = protocol.canonical_initialize_transcript(request, response)
        normalized_request, normalized_response = golden_initialize_transcript(
            nonminimal_max_varint=True
        )
        self.assertEqual(
            canonical,
            protocol.canonical_initialize_transcript(
                normalized_request, normalized_response
            ),
        )
        reordered_request, reordered_response = golden_initialize_transcript(
            swap_repeated_unknowns=True
        )
        self.assertNotEqual(
            canonical,
            protocol.canonical_initialize_transcript(
                reordered_request, reordered_response
            ),
        )

    def test_service_initializes_and_streams_one_terminal_result(self) -> None:
        async def exercise() -> None:
            service = PluginService(bytearray(b"k" * 32), lambda: None)
            request = initialize_request()
            response = await service.Initialize(request, FakeContext())
            self.assertEqual(common_pb2.ERROR_CODE_OK, response.status.code)
            duplicate = await service.Initialize(request, FakeContext())
            self.assertEqual(common_pb2.ERROR_CODE_CONFLICT, duplicate.status.code)
            call = plugin_pb2.ExecuteRequest(
                session_id=response.session_id,
                call_id="call-1",
                tool_name=ECHO_TOOL,
                arguments_json=b'{"value":"hello"}',
            )
            frames = [frame async for frame in service.Execute(call, FakeContext())]
            self.assertEqual([1, 2], [frame.sequence for frame in frames])
            self.assertEqual("progress", frames[0].WhichOneof("frame"))
            self.assertEqual("result", frames[1].WhichOneof("frame"))
            self.assertEqual({"value": "hello"}, json.loads(frames[1].result.content_json))

        asyncio.run(exercise())

    def test_execute_rejects_non_finite_json_and_invalid_utf8(self) -> None:
        for arguments in (
            b'{"value":NaN}',
            b'{"value":Infinity}',
            b'{"value":-Infinity}',
            b'{"value":"\xff"}',
        ):
            with self.subTest(arguments=arguments):
                with self.assertRaises(protocol.RequestError):
                    protocol.validate_execute(
                        plugin_pb2.ExecuteRequest(
                            session_id=b"s" * protocol.SESSION_ID_BYTES,
                            call_id="call",
                            tool_name=ECHO_TOOL,
                            arguments_json=arguments,
                        ),
                        b"s" * protocol.SESSION_ID_BYTES,
                        protocol.fixture_limits(),
                    )

    def test_execute_rejects_internal_control_characters_in_tokens(self) -> None:
        for field in ("call_id", "tool_name"):
            for control in ("\x00", "\r", "\n", "\t"):
                with self.subTest(field=field, control=repr(control)):
                    values = {
                        "session_id": b"s" * protocol.SESSION_ID_BYTES,
                        "call_id": "call",
                        "tool_name": ECHO_TOOL,
                        "arguments_json": b"{}",
                    }
                    values[field] = "before" + control + "after"
                    with self.assertRaises(protocol.RequestError):
                        protocol.validate_execute(
                            plugin_pb2.ExecuteRequest(**values),
                            b"s" * protocol.SESSION_ID_BYTES,
                            protocol.fixture_limits(),
                        )


class ServiceNegotiationTests(unittest.IsolatedAsyncioTestCase):
    async def test_incompatible_manifest_does_not_commit_session(self) -> None:
        service = PluginService(bytearray(b"k" * 32), lambda: None)
        incompatible = initialize_request()
        incompatible.requested_limits.max_tools = 2
        failed = await service.Initialize(incompatible, FakeContext())
        self.assertEqual(common_pb2.ERROR_CODE_INVALID_ARGUMENT, failed.status.code)

        compatible = initialize_request()
        initialized = await service.Initialize(compatible, FakeContext())
        self.assertEqual(common_pb2.ERROR_CODE_OK, initialized.status.code)

    async def test_selected_argument_limit_is_defensively_enforced(self) -> None:
        service = PluginService(bytearray(b"k" * 32), lambda: None)
        request = initialize_request()
        request.requested_limits.max_call_argument_bytes = 1
        response = await service.Initialize(request, FakeContext())
        self.assertEqual(common_pb2.ERROR_CODE_OK, response.status.code)
        request.requested_limits.max_call_argument_bytes = protocol.MAXIMUM_MESSAGE_BYTES
        response.limits.max_call_argument_bytes = protocol.MAXIMUM_MESSAGE_BYTES

        context = FakeContext()
        stream = service.Execute(
            plugin_pb2.ExecuteRequest(
                session_id=response.session_id,
                call_id="too-large",
                tool_name=ECHO_TOOL,
                arguments_json=b"{}",
            ),
            context,
        )
        with self.assertRaises(FakeAbortError):
            await anext(stream)
        self.assertEqual(grpc.StatusCode.RESOURCE_EXHAUSTED, context.code)

    async def test_concurrency_limit_and_drain_admission_fence_are_atomic(self) -> None:
        service = PluginService(bytearray(b"k" * 32), lambda: None)
        request = initialize_request()
        request.requested_limits.max_concurrent_calls = 1
        response = await service.Initialize(request, FakeContext())
        self.assertEqual(common_pb2.ERROR_CODE_OK, response.status.code)

        waiting = service.Execute(
            plugin_pb2.ExecuteRequest(
                session_id=response.session_id,
                call_id="active",
                tool_name=WAIT_TOOL,
                arguments_json=b"{}",
            ),
            FakeContext(),
        )
        progress = await anext(waiting)
        self.assertEqual("progress", progress.WhichOneof("frame"))

        overloaded_context = FakeContext()
        overloaded = service.Execute(
            plugin_pb2.ExecuteRequest(
                session_id=response.session_id,
                call_id="overloaded",
                tool_name=ECHO_TOOL,
                arguments_json=b'{"value":"ignored"}',
            ),
            overloaded_context,
        )
        with self.assertRaises(FakeAbortError):
            await anext(overloaded)
        self.assertEqual(grpc.StatusCode.RESOURCE_EXHAUSTED, overloaded_context.code)

        drain = asyncio.create_task(
            service.Drain(
                plugin_pb2.DrainRequest(session_id=response.session_id), FakeContext()
            )
        )
        await asyncio.sleep(0)
        fenced_context = FakeContext()
        fenced = service.Execute(
            plugin_pb2.ExecuteRequest(
                session_id=response.session_id,
                call_id="fenced",
                tool_name=ECHO_TOOL,
                arguments_json=b'{"value":"ignored"}',
            ),
            fenced_context,
        )
        with self.assertRaises(FakeAbortError):
            await anext(fenced)
        self.assertEqual(grpc.StatusCode.UNAVAILABLE, fenced_context.code)

        await waiting.aclose()
        drained = await asyncio.wait_for(drain, timeout=1)
        self.assertEqual(common_pb2.ERROR_CODE_OK, drained.status.code)


def golden_initialize_transcript(
    *, swap_repeated_unknowns: bool = False, nonminimal_max_varint: bool = False
):
    minimum = common_pb2.ProtocolVersion(major=1, minor=2, patch=3)
    protocol.add_unknown_bytes(minimum, 90, b"nested")
    protocol_range = common_pb2.ProtocolRange(
        minimum=minimum,
        maximum=common_pb2.ProtocolVersion(major=4, minor=5, patch=6),
    )
    protocol.add_unknown_varint(protocol_range, 91, 17)
    host = plugin_pb2.BuildIdentity(
        component="host-\u2028-雪", version="v1", commit="commit", runtime="go1.26.5"
    )
    protocol.add_unknown_fixed32(host, 92, 0x11223344)
    supported = common_pb2.CapabilitySet(names=["alpha", "runtime-tools-v1"])
    protocol.add_unknown_bytes(supported, 93, b"supported")
    request = plugin_pb2.InitializeRequest(
        protocol=protocol_range,
        host=host,
        supported_capabilities=supported,
        required_capabilities=common_pb2.CapabilitySet(names=["runtime-tools-v1"]),
        requested_limits=protocol.fixture_limits(),
        launch_id=b"\x11" * protocol.LAUNCH_ID_BYTES,
        handshake_challenge=b"\x22" * protocol.HANDSHAKE_CHALLENGE_BYTES,
    )
    # Field 6 is known as bytes; a varint with the same number is still an
    # unknown atom and must be authenticated rather than discarded.
    protocol.add_unknown_varint(request, 6, 42)
    repeated_unknowns = [b"future-compatible", b"future-compatible-2"]
    if swap_repeated_unknowns:
        repeated_unknowns.reverse()
    for value in repeated_unknowns:
        protocol.add_unknown_bytes(request, 127, value)
    if nonminimal_max_varint:
        request.MergeFromString(
            _encode_test_varint((126 << 3) | 0) + (b"\xff" * 9) + b"\x01"
        )
    else:
        protocol.add_unknown_varint(request, 126, (1 << 64) - 1)
    protocol.add_unknown_fixed64(request, 125, 0x0102030405060708)
    protocol.add_unknown_fixed32(request, 124, 0x01020304)

    overload = common_pb2.Overload(
        resource="calls", limit=(1 << 64) - 1, observed=9007199254740993
    )
    protocol.add_unknown_varint(overload, 94, 19)
    status = common_pb2.Status(
        code=common_pb2.ERROR_CODE_RESOURCE_EXHAUSTED,
        message="busy-\u2029-λ",
        retryable=True,
        operation_id="operation-1",
        overload=overload,
    )
    tool_capabilities = common_pb2.CapabilitySet(names=["filesystem-read"])
    protocol.add_unknown_fixed64(tool_capabilities, 95, 23)
    definition = plugin_pb2.ToolDefinition(
        name="echo",
        description="Echo 雪.",
        input_schema_json=b'{"type":"object"}',
        effect=plugin_pb2.TOOL_EFFECT_READ_ONLY,
        replay_safety=plugin_pb2.REPLAY_SAFETY_SAFE,
        capabilities=tool_capabilities,
    )
    response = plugin_pb2.InitializeResponse(
        status=status,
        protocol=common_pb2.ProtocolVersion(major=1),
        plugin=plugin_pb2.BuildIdentity(
            component="fixture", version="v1", commit="python", runtime="python3.12"
        ),
        capabilities=common_pb2.CapabilitySet(names=["runtime-tools-v1"]),
        limits=protocol.fixture_limits(),
        manifest=plugin_pb2.Manifest(name="fixture", version="v1", tools=[definition]),
        launch_id=b"\x11" * protocol.LAUNCH_ID_BYTES,
        session_id=b"\x33" * protocol.SESSION_ID_BYTES,
        handshake_challenge=b"\x22" * protocol.HANDSHAKE_CHALLENGE_BYTES,
        handshake_proof=b"ignored-self-proof",
    )
    protocol.add_unknown_bytes(response, 127, b"response-future")
    return request, response


def _encode_test_varint(value: int) -> bytes:
    result = bytearray()
    while value > 0x7F:
        result.append((value & 0x7F) | 0x80)
        value >>= 7
    result.append(value)
    return bytes(result)


if __name__ == "__main__":
    unittest.main()
