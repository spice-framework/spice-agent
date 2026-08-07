"""Async gRPC implementation of the frozen runtime-tool conformance profile."""

from __future__ import annotations

import asyncio
import json
import secrets
import sys
from collections.abc import Callable

import grpc
from spice.agent.common.v1 import common_pb2
from spice.agent.plugin.v1 import plugin_pb2, plugin_pb2_grpc

from . import protocol

ECHO_TOOL = "conformance.echo"
FAIL_TOOL = "conformance.fail"
WAIT_TOOL = "conformance.wait"


class PluginService(plugin_pb2_grpc.PluginServiceServicer):
    """One connection-owned plugin generation used by the public conformance kit."""

    def __init__(self, secret: bytearray, shutdown: Callable[[], None]) -> None:
        if len(secret) != protocol.HANDSHAKE_SECRET_BYTES:
            raise ValueError("fixture secret has an invalid size")
        self._secret = secret
        self._shutdown = shutdown
        self._limits = protocol.fixture_limits()
        self._manifest = _fixture_manifest()
        self._lock = asyncio.Lock()
        self._zero_active = asyncio.Event()
        self._zero_active.set()
        self._initialized = False
        self._draining = False
        self._closed = False
        self._session_id = b""
        self._active = 0

    async def Initialize(self, request, context):  # noqa: N802 - generated RPC API
        try:
            protocol.validate_initialize(request)
        except protocol.RequestError:
            return self._failed_initialize(
                request,
                common_pb2.ERROR_CODE_INVALID_ARGUMENT,
                "plugin initialization request is invalid",
                context,
            )

        async with self._lock:
            if self._initialized:
                return self._failed_initialize(
                    request,
                    common_pb2.ERROR_CODE_CONFLICT,
                    "plugin session is already initialized",
                    context,
                )
            selected_protocol = protocol.select_protocol(request.protocol)
            if selected_protocol is None:
                return self._failed_initialize(
                    request,
                    common_pb2.ERROR_CODE_INCOMPATIBLE_VERSION,
                    "client and plugin protocol ranges do not overlap",
                    context,
                )
            selected_capabilities = protocol.select_capabilities(request)
            if selected_capabilities is None:
                return self._failed_initialize(
                    request,
                    common_pb2.ERROR_CODE_MISSING_CAPABILITY,
                    "required client capabilities are unavailable",
                    context,
                )
            try:
                selected_limits = protocol.negotiate_limits(request.requested_limits)
                protocol.validate_manifest(self._manifest, selected_limits)
            except protocol.RequestError:
                return self._failed_initialize(
                    request,
                    common_pb2.ERROR_CODE_INVALID_ARGUMENT,
                    "plugin limit or manifest negotiation failed",
                    context,
                )

            session_id = secrets.token_bytes(protocol.SESSION_ID_BYTES)
            response = plugin_pb2.InitializeResponse(
                status=protocol.ok_status(),
                protocol=selected_protocol,
                plugin=_fixture_build(),
                capabilities=common_pb2.CapabilitySet(names=selected_capabilities),
                limits=selected_limits,
                manifest=self._manifest,
                launch_id=request.launch_id,
                session_id=session_id,
                handshake_challenge=request.handshake_challenge,
            )
            signed = protocol.sign_initialize(request, response, self._secret)
            self._initialized = True
            self._limits = protocol.clone_limits(selected_limits)
            self._session_id = session_id
            return signed

    async def Execute(self, request, context):  # noqa: N802 - generated RPC API
        async with self._lock:
            if not self._initialized or self._closed:
                await context.abort(grpc.StatusCode.FAILED_PRECONDITION, "plugin session is unavailable")
            if self._draining:
                await context.abort(grpc.StatusCode.UNAVAILABLE, "plugin is draining")
            try:
                protocol.validate_execute(request, self._session_id, self._limits)
            except OverflowError:
                await context.abort(
                    grpc.StatusCode.RESOURCE_EXHAUSTED,
                    "plugin call exceeds the negotiated limit",
                )
            except protocol.RequestError:
                await context.abort(grpc.StatusCode.INVALID_ARGUMENT, "plugin call is invalid")
            if request.tool_name not in {ECHO_TOOL, FAIL_TOOL, WAIT_TOOL}:
                await context.abort(grpc.StatusCode.NOT_FOUND, "plugin tool is unavailable")
            if self._active >= self._limits.max_concurrent_calls:
                await context.abort(
                    grpc.StatusCode.RESOURCE_EXHAUSTED,
                    "plugin concurrent-call limit is exhausted",
                )
            self._active += 1
            self._zero_active.clear()

        try:
            if request.tool_name == ECHO_TOOL:
                async for response in self._execute_echo(request, context):
                    yield response
            elif request.tool_name == FAIL_TOOL:
                yield await self._execute_failure(request, context)
            else:
                async for response in self._execute_wait(request, context):
                    yield response
        finally:
            async with self._lock:
                self._active -= 1
                if self._active == 0:
                    self._zero_active.set()

    async def Drain(self, request, context):  # noqa: N802 - generated RPC API
        async with self._lock:
            await self._require_session(request.session_id, context)
            self._draining = True
            zero_active = self._zero_active
        await zero_active.wait()
        return plugin_pb2.DrainResponse(status=protocol.ok_status(), active_calls=0)

    async def Shutdown(self, request, context):  # noqa: N802 - generated RPC API
        async with self._lock:
            await self._require_session(request.session_id, context)
            if not self._draining or self._active != 0:
                await context.abort(
                    grpc.StatusCode.FAILED_PRECONDITION,
                    "plugin must drain before shutdown",
                )
            self._closed = True
            for index in range(len(self._secret)):
                self._secret[index] = 0
        asyncio.get_running_loop().call_later(0.05, self._shutdown)
        return plugin_pb2.ShutdownResponse(status=protocol.ok_status())

    def _failed_initialize(self, request, code: int, message: str, context):
        if (
            len(request.launch_id) != protocol.LAUNCH_ID_BYTES
            or len(request.handshake_challenge) != protocol.HANDSHAKE_CHALLENGE_BYTES
        ):
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            context.set_details("plugin initialization request is invalid")
            return plugin_pb2.InitializeResponse()
        response = plugin_pb2.InitializeResponse(
            status=protocol.error_status(code, message),
            plugin=_fixture_build(),
            launch_id=request.launch_id,
            handshake_challenge=request.handshake_challenge,
        )
        return protocol.sign_initialize(request, response, self._secret)

    async def _require_session(self, observed: bytes, context) -> None:
        if not self._initialized or self._closed:
            await context.abort(
                grpc.StatusCode.FAILED_PRECONDITION, "plugin session is unavailable"
            )
        try:
            protocol.validate_session(observed, self._session_id)
        except protocol.RequestError:
            await context.abort(grpc.StatusCode.INVALID_ARGUMENT, "plugin session is invalid")

    async def _execute_echo(self, request, context):
        try:
            value = json.loads(request.arguments_json)
        except (UnicodeDecodeError, json.JSONDecodeError):
            await context.abort(grpc.StatusCode.INVALID_ARGUMENT, "plugin echo arguments are invalid")
        if not isinstance(value, dict) or not isinstance(value.get("value"), str) or not value["value"]:
            await context.abort(grpc.StatusCode.INVALID_ARGUMENT, "plugin echo arguments are invalid")
        progress = plugin_pb2.ExecuteResponse(
            call_id=request.call_id,
            sequence=1,
            progress=plugin_pb2.Progress(message="echo accepted"),
        )
        protocol.add_unknown_bytes(progress, 127, b"fixture-compatible")
        yield progress
        content = json.dumps(
            {"value": value["value"]}, separators=(",", ":"), ensure_ascii=False
        ).encode("utf-8")
        yield plugin_pb2.ExecuteResponse(
            call_id=request.call_id,
            sequence=2,
            result=plugin_pb2.Result(content_json=content),
        )

    async def _execute_failure(self, request, context):
        if request.arguments_json != b"{}":
            await context.abort(
                grpc.StatusCode.INVALID_ARGUMENT, "plugin failure arguments are invalid"
            )
        return plugin_pb2.ExecuteResponse(
            call_id=request.call_id,
            sequence=1,
            failure=plugin_pb2.ExecutionFailure(
                state=plugin_pb2.EXECUTION_STATE_DEFINITIVE,
                retry=plugin_pb2.RETRY_DISPOSITION_NEVER,
                safe_message="fixture failure",
            ),
        )

    async def _execute_wait(self, request, context):
        if request.arguments_json != b"{}":
            await context.abort(grpc.StatusCode.INVALID_ARGUMENT, "plugin wait arguments are invalid")
        yield plugin_pb2.ExecuteResponse(
            call_id=request.call_id,
            sequence=1,
            progress=plugin_pb2.Progress(message="waiting"),
        )
        await asyncio.Future()


def _fixture_manifest() -> plugin_pb2.Manifest:
    definitions = []
    for name, description, schema in (
        (
            ECHO_TOOL,
            "Echo one string value.",
            '{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}',
        ),
        (FAIL_TOOL, "Return one definitive fixture failure.", '{"type":"object"}'),
        (WAIT_TOOL, "Wait until the call is cancelled.", '{"type":"object"}'),
    ):
        definitions.append(
            plugin_pb2.ToolDefinition(
                name=name,
                description=description,
                input_schema_json=schema.encode("utf-8"),
                effect=plugin_pb2.TOOL_EFFECT_READ_ONLY,
                replay_safety=plugin_pb2.REPLAY_SAFETY_SAFE,
                capabilities=common_pb2.CapabilitySet(),
            )
        )
    return plugin_pb2.Manifest(
        name="spice-agent-python-conformance",
        version="v1",
        tools=definitions,
    )


def _fixture_build() -> plugin_pb2.BuildIdentity:
    return plugin_pb2.BuildIdentity(
        component="spice-agent-python-conformance",
        version="v1",
        commit="fixture",
        runtime=f"python{sys.version_info.major}.{sys.version_info.minor}.{sys.version_info.micro}",
    )
