"""Frozen plugin/v1 constants and validation shared by the fixture service."""

from __future__ import annotations

import base64
import hashlib
import hmac
import json
from collections.abc import Iterable

from google.protobuf.descriptor import FieldDescriptor
from google.protobuf.message import Message
from spice.agent.common.v1 import common_pb2
from spice.agent.plugin.v1 import plugin_pb2

PROTOCOL_MAJOR = 1
PROTOCOL_MINOR = 0
PROTOCOL_PATCH = 0
CAPABILITY_RUNTIME_TOOLS = "runtime-tools-v1"
LAUNCH_ID_BYTES = 16
SESSION_ID_BYTES = 16
HANDSHAKE_CHALLENGE_BYTES = 32
HANDSHAKE_SECRET_BYTES = 32
MAXIMUM_MESSAGE_BYTES = 1 << 20
MAXIMUM_TOKEN_BYTES = 256
MAXIMUM_PROGRESS_BYTES = 4096
SUPPORTED_TOOL_CAPABILITIES = frozenset(
    {
        "environment.read",
        "environment.write",
        "filesystem.read",
        "filesystem.write",
        "network.access",
        "process.execute",
        "secrets.read",
    }
)
MUTATING_TOOL_CAPABILITIES = frozenset(
    {"environment.write", "filesystem.write", "process.execute"}
)
INITIALIZE_TRANSCRIPT_DOMAIN = "spice-agent/plugin/v1/initialize"
INITIALIZE_TRANSCRIPT_VERSION = 1


class RequestError(ValueError):
    """A safe invalid-request failure."""


def ok_status() -> common_pb2.Status:
    return common_pb2.Status(code=common_pb2.ERROR_CODE_OK)


def error_status(code: int, message: str) -> common_pb2.Status:
    return common_pb2.Status(code=code, message=message)


def fixture_limits() -> plugin_pb2.Limits:
    return plugin_pb2.Limits(
        max_message_bytes=MAXIMUM_MESSAGE_BYTES,
        max_tools=16,
        max_schema_bytes=64 << 10,
        max_call_argument_bytes=64 << 10,
        max_result_bytes=MAXIMUM_MESSAGE_BYTES,
        max_progress_bytes=MAXIMUM_PROGRESS_BYTES,
        max_concurrent_calls=8,
    )


def negotiate_limits(requested: plugin_pb2.Limits) -> plugin_pb2.Limits:
    validate_limits(requested)
    available = fixture_limits()
    return plugin_pb2.Limits(
        max_message_bytes=min(requested.max_message_bytes, available.max_message_bytes),
        max_tools=min(requested.max_tools, available.max_tools),
        max_schema_bytes=min(requested.max_schema_bytes, available.max_schema_bytes),
        max_call_argument_bytes=min(
            requested.max_call_argument_bytes, available.max_call_argument_bytes
        ),
        max_result_bytes=min(requested.max_result_bytes, available.max_result_bytes),
        max_progress_bytes=min(requested.max_progress_bytes, available.max_progress_bytes),
        max_concurrent_calls=min(
            requested.max_concurrent_calls, available.max_concurrent_calls
        ),
    )


def clone_limits(value: plugin_pb2.Limits) -> plugin_pb2.Limits:
    """Return a defensive copy of one already-validated negotiated limit set."""
    result = plugin_pb2.Limits()
    result.CopyFrom(value)
    return result


def validate_manifest(value: plugin_pb2.Manifest, limits: plugin_pb2.Limits) -> None:
    """Validate a runtime-tool manifest against the selected wire limits."""
    validate_limits(limits)
    validate_token(value.name)
    validate_token(value.version)
    if not value.tools or len(value.tools) > limits.max_tools:
        raise RequestError("plugin manifest tool count is outside negotiated limits")
    previous = ""
    for definition in value.tools:
        validate_token(definition.name, maximum=128)
        if previous and previous >= definition.name:
            raise RequestError("plugin tool definitions must be sorted and unique")
        previous = definition.name
        validate_token(definition.description, maximum=MAXIMUM_PROGRESS_BYTES)
        if not definition.input_schema_json:
            raise RequestError("plugin tool schema must be valid JSON")
        if len(definition.input_schema_json) > limits.max_schema_bytes:
            raise RequestError("plugin tool schema exceeds the negotiated limit")
        try:
            _load_strict_json(definition.input_schema_json)
        except (UnicodeDecodeError, ValueError) as error:
            raise RequestError("plugin tool schema must be valid JSON") from error
        if definition.effect not in (
            plugin_pb2.TOOL_EFFECT_READ_ONLY,
            plugin_pb2.TOOL_EFFECT_MUTATING,
        ):
            raise RequestError("plugin tool effect is unsupported")
        if definition.replay_safety not in (
            plugin_pb2.REPLAY_SAFETY_SAFE,
            plugin_pb2.REPLAY_SAFETY_IDEMPOTENT,
            plugin_pb2.REPLAY_SAFETY_UNSAFE,
        ):
            raise RequestError("plugin tool replay safety is unsupported")
        if (
            definition.effect == plugin_pb2.TOOL_EFFECT_READ_ONLY
            and definition.replay_safety != plugin_pb2.REPLAY_SAFETY_SAFE
        ) or (
            definition.effect == plugin_pb2.TOOL_EFFECT_MUTATING
            and definition.replay_safety == plugin_pb2.REPLAY_SAFETY_SAFE
        ):
            raise RequestError("plugin tool effect and replay safety are inconsistent")
        if not definition.HasField("capabilities"):
            raise RequestError("plugin tool capabilities are required")
        capabilities = validate_capabilities(definition.capabilities.names)
        if not set(capabilities).issubset(SUPPORTED_TOOL_CAPABILITIES):
            raise RequestError("plugin tool capability is unsupported")
        if definition.effect == plugin_pb2.TOOL_EFFECT_READ_ONLY and set(
            capabilities
        ).intersection(MUTATING_TOOL_CAPABILITIES):
            raise RequestError("read-only tool declares a mutation-capable capability")
    if value.ByteSize() > limits.max_message_bytes:
        raise RequestError("plugin manifest exceeds the negotiated message limit")


def validate_initialize(request: plugin_pb2.InitializeRequest) -> None:
    if request.ByteSize() > MAXIMUM_MESSAGE_BYTES:
        raise RequestError("plugin initialization request exceeds its byte limit")
    if not request.HasField("protocol"):
        raise RequestError("plugin protocol range is required")
    minimum = request.protocol.minimum
    maximum = request.protocol.maximum
    if minimum.major == 0 or maximum.major == 0:
        raise RequestError("plugin protocol versions require a positive major")
    if _version_tuple(minimum) > _version_tuple(maximum):
        raise RequestError("plugin protocol range is invalid")
    for value in (
        request.host.component,
        request.host.version,
        request.host.commit,
        request.host.runtime,
    ):
        validate_token(value)
    supported = validate_capabilities(request.supported_capabilities.names)
    required = validate_capabilities(request.required_capabilities.names)
    if CAPABILITY_RUNTIME_TOOLS not in supported or CAPABILITY_RUNTIME_TOOLS not in required:
        raise RequestError("runtime-tools-v1 must be supported and required")
    if not set(required).issubset(supported):
        raise RequestError("required capabilities must be host-supported")
    validate_limits(request.requested_limits)
    if len(request.launch_id) != LAUNCH_ID_BYTES:
        raise RequestError("plugin launch identity has an invalid size")
    if len(request.handshake_challenge) != HANDSHAKE_CHALLENGE_BYTES:
        raise RequestError("plugin handshake challenge has an invalid size")


def select_protocol(value: common_pb2.ProtocolRange) -> common_pb2.ProtocolVersion | None:
    selected = (PROTOCOL_MAJOR, PROTOCOL_MINOR, PROTOCOL_PATCH)
    if _version_tuple(value.minimum) <= selected <= _version_tuple(value.maximum):
        return common_pb2.ProtocolVersion(
            major=PROTOCOL_MAJOR,
            minor=PROTOCOL_MINOR,
            patch=PROTOCOL_PATCH,
        )
    return None


def select_capabilities(request: plugin_pb2.InitializeRequest) -> list[str] | None:
    supported = set(request.supported_capabilities.names)
    required = set(request.required_capabilities.names)
    available = {CAPABILITY_RUNTIME_TOOLS}
    if not required.issubset(available):
        return None
    return sorted(supported.intersection(available))


def validate_limits(value: plugin_pb2.Limits) -> None:
    fields = (
        value.max_message_bytes,
        value.max_tools,
        value.max_schema_bytes,
        value.max_call_argument_bytes,
        value.max_result_bytes,
        value.max_progress_bytes,
        value.max_concurrent_calls,
    )
    if any(field <= 0 for field in fields):
        raise RequestError("plugin limits must all be positive")
    if value.max_message_bytes > MAXIMUM_MESSAGE_BYTES:
        raise RequestError("plugin message limit is too large")
    if value.max_tools > 4096 or value.max_concurrent_calls > 4096:
        raise RequestError("plugin collection limit is too large")
    for bounded in (
        value.max_schema_bytes,
        value.max_call_argument_bytes,
        value.max_result_bytes,
        value.max_progress_bytes,
    ):
        if bounded > value.max_message_bytes:
            raise RequestError("plugin submessage limit exceeds the message limit")
    if value.max_progress_bytes > MAXIMUM_PROGRESS_BYTES:
        raise RequestError("plugin progress limit is too large")


def validate_execute(
    request: plugin_pb2.ExecuteRequest,
    session_id: bytes,
    limits: plugin_pb2.Limits,
) -> None:
    if not hmac.compare_digest(request.session_id, session_id):
        raise RequestError("plugin session identity does not match")
    validate_token(request.call_id, maximum=128)
    validate_token(request.tool_name, maximum=128)
    if not request.arguments_json:
        raise RequestError("plugin call arguments must be valid JSON")
    if len(request.arguments_json) > limits.max_call_argument_bytes:
        raise OverflowError("plugin call exceeds the negotiated limit")
    try:
        _load_strict_json(request.arguments_json)
    except (UnicodeDecodeError, ValueError) as error:
        raise RequestError("plugin call arguments must be valid JSON") from error
    if request.ByteSize() > limits.max_message_bytes:
        raise OverflowError("plugin call exceeds the negotiated message limit")


def validate_session(observed: bytes, expected: bytes) -> None:
    if len(observed) != SESSION_ID_BYTES or not hmac.compare_digest(observed, expected):
        raise RequestError("plugin session identity does not match")


def validate_token(value: str, maximum: int = MAXIMUM_TOKEN_BYTES) -> None:
    try:
        encoded = value.encode("utf-8")
    except UnicodeEncodeError as error:
        raise RequestError("plugin token is invalid") from error
    if not value or value != value.strip() or len(encoded) > maximum:
        raise RequestError("plugin token is invalid")
    if any(character in value for character in ("\x00", "\r", "\n", "\t")):
        raise RequestError("plugin token is invalid")


def validate_capabilities(values: Iterable[str]) -> list[str]:
    result = list(values)
    if len(result) > 1024 or result != sorted(set(result)):
        raise RequestError("plugin capabilities must be sorted and unique")
    for value in result:
        validate_token(value)
    return result


def sign_initialize(
    request: plugin_pb2.InitializeRequest,
    response: plugin_pb2.InitializeResponse,
    secret: bytearray,
) -> plugin_pb2.InitializeResponse:
    if len(secret) != HANDSHAKE_SECRET_BYTES:
        raise ValueError("handshake secret has an invalid size")
    signed = plugin_pb2.InitializeResponse()
    signed.CopyFrom(response)
    signed.ClearField("handshake_proof")
    transcript = canonical_initialize_transcript(request, signed)
    signed.handshake_proof = hmac.new(secret, transcript, hashlib.sha256).digest()
    return signed


def canonical_initialize_transcript(
    request: plugin_pb2.InitializeRequest,
    response: plugin_pb2.InitializeResponse,
) -> bytes:
    """Encode the normative plugin/v1 initialize HMAC transcript."""
    if request is None or response is None:
        raise ValueError("plugin handshake transcript is required")
    value = [
        INITIALIZE_TRANSCRIPT_DOMAIN,
        INITIALIZE_TRANSCRIPT_VERSION,
        _initialize_request(request),
        _initialize_response(response),
    ]
    encoded = json.dumps(
        value,
        ensure_ascii=False,
        separators=(",", ":"),
        allow_nan=False,
    )
    # Go's encoding/json always escapes these two valid JSON characters. The
    # transcript contract fixes that spelling so all runtimes emit the same
    # UTF-8 bytes while leaving other non-ASCII text unescaped.
    encoded = encoded.replace("\u2028", "\\u2028").replace("\u2029", "\\u2029")
    return encoded.encode("utf-8")


def _initialize_request(value: plugin_pb2.InitializeRequest) -> list:
    return _message(
        value,
        "InitializeRequest",
        _protocol_range(value.protocol) if value.HasField("protocol") else None,
        _build_identity(value.host) if value.HasField("host") else None,
        _capability_set(value.supported_capabilities)
        if value.HasField("supported_capabilities")
        else None,
        _capability_set(value.required_capabilities)
        if value.HasField("required_capabilities")
        else None,
        _limits(value.requested_limits) if value.HasField("requested_limits") else None,
        _bytes(value.launch_id),
        _bytes(value.handshake_challenge),
    )


def _initialize_response(value: plugin_pb2.InitializeResponse) -> list:
    return _message(
        value,
        "InitializeResponse",
        _status(value.status) if value.HasField("status") else None,
        _protocol_version(value.protocol) if value.HasField("protocol") else None,
        _build_identity(value.plugin) if value.HasField("plugin") else None,
        _capability_set(value.capabilities) if value.HasField("capabilities") else None,
        _limits(value.limits) if value.HasField("limits") else None,
        _manifest(value.manifest) if value.HasField("manifest") else None,
        _bytes(value.launch_id),
        _bytes(value.session_id),
        _bytes(value.handshake_challenge),
        _bytes(b""),
    )


def _protocol_version(value: common_pb2.ProtocolVersion) -> list:
    return _message(value, "ProtocolVersion", value.major, value.minor, value.patch)


def _protocol_range(value: common_pb2.ProtocolRange) -> list:
    return _message(
        value,
        "ProtocolRange",
        _protocol_version(value.minimum) if value.HasField("minimum") else None,
        _protocol_version(value.maximum) if value.HasField("maximum") else None,
    )


def _build_identity(value: plugin_pb2.BuildIdentity) -> list:
    return _message(
        value,
        "BuildIdentity",
        value.component,
        value.version,
        value.commit,
        value.runtime,
    )


def _capability_set(value: common_pb2.CapabilitySet) -> list:
    return _message(value, "CapabilitySet", list(value.names))


def _limits(value: plugin_pb2.Limits) -> list:
    return _message(
        value,
        "Limits",
        value.max_message_bytes,
        value.max_tools,
        value.max_schema_bytes,
        value.max_call_argument_bytes,
        value.max_result_bytes,
        value.max_progress_bytes,
        value.max_concurrent_calls,
    )


def _manifest(value: plugin_pb2.Manifest) -> list:
    return _message(
        value,
        "Manifest",
        value.name,
        value.version,
        [_tool_definition(tool) for tool in value.tools],
    )


def _tool_definition(value: plugin_pb2.ToolDefinition) -> list:
    return _message(
        value,
        "ToolDefinition",
        value.name,
        value.description,
        _bytes(value.input_schema_json),
        value.effect,
        value.replay_safety,
        _capability_set(value.capabilities) if value.HasField("capabilities") else None,
    )


def _status(value: common_pb2.Status) -> list:
    return _message(
        value,
        "Status",
        value.code,
        value.message,
        value.retryable,
        value.operation_id,
        _status_detail(value),
    )


def _status_detail(value: common_pb2.Status) -> list | None:
    detail = value.WhichOneof("detail")
    if detail is None:
        return None
    encoders = {
        "version_mismatch": _version_mismatch,
        "capability_mismatch": _capability_mismatch,
        "replay_bounds": _replay_bounds,
        "overload": _overload,
        "stale_client": _stale_client,
        "snapshot_version_mismatch": _snapshot_version_mismatch,
        "uncertain_operation": _uncertain_operation,
    }
    encoder = encoders.get(detail)
    if encoder is None:
        raise ValueError("plugin initialize status contains an unsupported known detail")
    return [detail, encoder(getattr(value, detail))]


def _version_mismatch(value: common_pb2.VersionMismatch) -> list:
    return _message(
        value,
        "VersionMismatch",
        _protocol_range(value.client) if value.HasField("client") else None,
        _protocol_range(value.server) if value.HasField("server") else None,
    )


def _capability_mismatch(value: common_pb2.CapabilityMismatch) -> list:
    return _message(
        value,
        "CapabilityMismatch",
        list(value.required),
        list(value.available),
        list(value.missing),
    )


def _replay_bounds(value: common_pb2.ReplayBounds) -> list:
    return _message(
        value,
        "ReplayBounds",
        value.requested_after_sequence,
        value.earliest_sequence,
        value.latest_sequence,
        value.recovery_sequence,
    )


def _overload(value: common_pb2.Overload) -> list:
    return _message(value, "Overload", value.resource, value.limit, value.observed)


def _stale_client(value: common_pb2.StaleClient) -> list:
    return _message(value, "StaleClient", value.expected_epoch, value.observed_epoch)


def _snapshot_version_mismatch(value: common_pb2.SnapshotVersionMismatch) -> list:
    return _message(value, "SnapshotVersionMismatch", value.expected, value.observed)


def _uncertain_operation(value: common_pb2.UncertainOperation) -> list:
    return _message(value, "UncertainOperation", value.operation_id, value.operation_kind)


def _message(value: Message, name: str, *fields) -> list:
    return [name, *fields, _unknown_atoms(value)]


def _unknown_atoms(message: Message) -> list[list]:
    known = {
        field.number: _known_wire_types(field) for field in message.DESCRIPTOR.fields
    }
    atoms: list[tuple[int, int, int | bytes]] = []
    raw = message.SerializeToString()
    offset = 0
    while offset < len(raw):
        tag, offset = _decode_varint(raw, offset)
        field = tag >> 3
        wire_type = tag & 7
        if field <= 0:
            raise ValueError("plugin handshake contains malformed unknown wire data")
        if wire_type == 0:
            wire_value, offset = _decode_varint(raw, offset)
        elif wire_type == 1:
            offset, wire_value = _consume_fixed(raw, offset, 8)
        elif wire_type == 2:
            size, offset = _decode_varint(raw, offset)
            end = offset + size
            if end > len(raw):
                raise ValueError("plugin handshake contains malformed unknown wire data")
            wire_value = raw[offset:end]
            offset = end
        elif wire_type in (3, 4):
            raise ValueError("plugin handshake unknown protobuf groups are unsupported")
        elif wire_type == 5:
            offset, wire_value = _consume_fixed(raw, offset, 4)
        else:
            raise ValueError("plugin handshake contains an unsupported unknown wire type")
        if field not in known or wire_type not in known[field]:
            atoms.append((field, wire_type, wire_value))
    return [
        [field, wire_type, _bytes(value) if isinstance(value, bytes) else value]
        for field, wire_type, value in atoms
    ]


def _known_wire_types(field: FieldDescriptor) -> frozenset[int]:
    wire_by_type = {
        FieldDescriptor.TYPE_DOUBLE: 1,
        FieldDescriptor.TYPE_FLOAT: 5,
        FieldDescriptor.TYPE_INT64: 0,
        FieldDescriptor.TYPE_UINT64: 0,
        FieldDescriptor.TYPE_INT32: 0,
        FieldDescriptor.TYPE_FIXED64: 1,
        FieldDescriptor.TYPE_FIXED32: 5,
        FieldDescriptor.TYPE_BOOL: 0,
        FieldDescriptor.TYPE_STRING: 2,
        FieldDescriptor.TYPE_GROUP: 3,
        FieldDescriptor.TYPE_MESSAGE: 2,
        FieldDescriptor.TYPE_BYTES: 2,
        FieldDescriptor.TYPE_UINT32: 0,
        FieldDescriptor.TYPE_ENUM: 0,
        FieldDescriptor.TYPE_SFIXED32: 5,
        FieldDescriptor.TYPE_SFIXED64: 1,
        FieldDescriptor.TYPE_SINT32: 0,
        FieldDescriptor.TYPE_SINT64: 0,
    }
    wire_type = wire_by_type.get(field.type)
    if wire_type is None:
        raise ValueError("plugin handshake descriptor contains an unsupported field type")
    result = {wire_type}
    if field.is_repeated and wire_type in (0, 1, 5):
        result.add(2)
    return frozenset(result)


def _consume_fixed(raw: bytes, offset: int, size: int) -> tuple[int, int]:
    end = offset + size
    if end > len(raw):
        raise ValueError("plugin handshake contains malformed unknown wire data")
    return end, int.from_bytes(raw[offset:end], "little", signed=False)


def _decode_varint(raw: bytes, offset: int) -> tuple[int, int]:
    value = 0
    for shift in range(0, 70, 7):
        if offset >= len(raw):
            raise ValueError("plugin handshake contains malformed unknown wire data")
        current = raw[offset]
        offset += 1
        if shift == 63 and current > 1:
            raise ValueError("plugin handshake contains malformed unknown wire data")
        value |= (current & 0x7F) << shift
        if current < 0x80:
            return value, offset
    raise ValueError("plugin handshake contains malformed unknown wire data")


def _bytes(value: bytes) -> str:
    return base64.urlsafe_b64encode(value).rstrip(b"=").decode("ascii")


def add_unknown_bytes(message: Message, field: int, value: bytes) -> None:
    encoded = _encode_varint((field << 3) | 2) + _encode_varint(len(value)) + value
    message.MergeFromString(encoded)


def add_unknown_varint(message: Message, field: int, value: int) -> None:
    message.MergeFromString(_encode_varint((field << 3) | 0) + _encode_varint(value))


def add_unknown_fixed64(message: Message, field: int, value: int) -> None:
    message.MergeFromString(
        _encode_varint((field << 3) | 1) + value.to_bytes(8, "little", signed=False)
    )


def add_unknown_fixed32(message: Message, field: int, value: int) -> None:
    message.MergeFromString(
        _encode_varint((field << 3) | 5) + value.to_bytes(4, "little", signed=False)
    )


def _encode_varint(value: int) -> bytes:
    result = bytearray()
    while value > 0x7F:
        result.append((value & 0x7F) | 0x80)
        value >>= 7
    result.append(value)
    return bytes(result)


def _version_tuple(value: common_pb2.ProtocolVersion) -> tuple[int, int, int]:
    return value.major, value.minor, value.patch


def _load_strict_json(value: bytes) -> object:
    return json.loads(value, parse_constant=_reject_json_constant)


def _reject_json_constant(_value: str) -> None:
    raise ValueError("non-finite JSON number")
