package enginev1

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"google.golang.org/protobuf/proto"
)

const (
	// SnapshotFormat is the only safe kernel snapshot format accepted by v1.
	SnapshotFormat = "spice.agent.snapshot/v1alpha2"
	// MaximumSnapshotBytes matches the bounded kernel snapshot contract.
	MaximumSnapshotBytes  = 16 << 20
	maximumJSONBytes      = 1 << 20
	maximumTokenBytes     = 256
	maximumMessageParts   = 128
	minimumAuthTokenBytes = 32
	maximumAuthTokenBytes = 128
)

// NegotiateInitialize validates the first request and returns a complete
// response without retaining or reflecting the authentication token.
func NegotiateInitialize(
	request *InitializeRequest,
	serverRange *commonv1.ProtocolRange,
	serverBuild *commonv1.BuildIdentity,
	serverCapabilities *commonv1.CapabilitySet,
	serverLimits *commonv1.Limits,
	health *commonv1.Health,
	clientID string,
	ownershipEpoch uint64,
) *InitializeResponse {
	invalid := func(message string) *InitializeResponse {
		return &InitializeResponse{Status: protocolStatus(commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, message)}
	}
	if request == nil {
		return invalid("initialize request is required")
	}
	if size := len(request.GetAuthenticationToken()); size < minimumAuthTokenBytes || size > maximumAuthTokenBytes {
		return invalid("authentication token must be between 32 and 128 bytes")
	}
	if err := commonv1.ValidateBuildIdentity(request.GetClient()); err != nil {
		return invalid("client build identity: " + err.Error())
	}
	if err := commonv1.ValidateBuildIdentity(serverBuild); err != nil {
		return &InitializeResponse{Status: protocolStatus(commonv1.ErrorCode_ERROR_CODE_INTERNAL, "server build identity is invalid")}
	}
	if err := commonv1.ValidateHealth(health); err != nil {
		return &InitializeResponse{Status: protocolStatus(commonv1.ErrorCode_ERROR_CODE_INTERNAL, "server health is invalid")}
	}
	if err := token("client ID", clientID, 128); err != nil || ownershipEpoch == 0 {
		return &InitializeResponse{Status: protocolStatus(commonv1.ErrorCode_ERROR_CODE_INTERNAL, "server client ownership is invalid")}
	}
	version, negotiationStatus := commonv1.NegotiateProtocol(request.GetProtocol(), serverRange)
	if negotiationStatus.GetCode() != commonv1.ErrorCode_ERROR_CODE_OK {
		return &InitializeResponse{Status: negotiationStatus}
	}
	capabilities, negotiationStatus := commonv1.NegotiateCapabilities(
		request.GetSupportedCapabilities(),
		request.GetRequiredCapabilities(),
		serverCapabilities,
	)
	if negotiationStatus.GetCode() != commonv1.ErrorCode_ERROR_CODE_OK {
		return &InitializeResponse{Status: negotiationStatus}
	}
	limits, negotiationStatus := commonv1.NegotiateLimits(request.GetRequestedLimits(), serverLimits)
	if negotiationStatus.GetCode() != commonv1.ErrorCode_ERROR_CODE_OK {
		return &InitializeResponse{Status: negotiationStatus}
	}
	return &InitializeResponse{
		Status:         commonv1.OKStatus(),
		Protocol:       version,
		Server:         clone(serverBuild),
		Capabilities:   capabilities,
		Limits:         limits,
		Health:         clone(health),
		ClientId:       clientID,
		OwnershipEpoch: ownershipEpoch,
	}
}

// ValidateInitializeResponse lets a client fail closed on malformed negotiation.
func ValidateInitializeResponse(response *InitializeResponse) error {
	if response == nil {
		return errors.New("initialize response is required")
	}
	if err := commonv1.ValidateStatus(response.GetStatus()); err != nil {
		return err
	}
	if statusErr := commonv1.AsError(response.GetStatus()); statusErr != nil {
		return statusErr
	}
	if err := commonv1.ValidateProtocolRange(&commonv1.ProtocolRange{
		Minimum: response.GetProtocol(),
		Maximum: response.GetProtocol(),
	}); err != nil {
		return err
	}
	if err := commonv1.ValidateBuildIdentity(response.GetServer()); err != nil {
		return err
	}
	if err := commonv1.ValidateCapabilities(response.GetCapabilities()); err != nil {
		return err
	}
	if err := commonv1.ValidateLimits(response.GetLimits()); err != nil {
		return err
	}
	if err := commonv1.ValidateHealth(response.GetHealth()); err != nil {
		return err
	}
	if err := token("client ID", response.GetClientId(), 128); err != nil {
		return err
	}
	if response.GetOwnershipEpoch() == 0 {
		return errors.New("ownership epoch must be positive")
	}
	return nil
}

// CheckClientOwnership returns a typed stale-client status on identity or epoch
// mismatch. A nil return means the client still owns the connection.
func CheckClientOwnership(
	clientID string,
	observedEpoch uint64,
	expectedClientID string,
	expectedEpoch uint64,
) *commonv1.Status {
	if clientID == expectedClientID && observedEpoch == expectedEpoch && expectedEpoch != 0 {
		return nil
	}
	return &commonv1.Status{
		Code:    commonv1.ErrorCode_ERROR_CODE_STALE_CLIENT,
		Message: "client ownership is stale",
		Detail: &commonv1.Status_StaleClient{StaleClient: &commonv1.StaleClient{
			ExpectedEpoch: expectedEpoch,
			ObservedEpoch: observedEpoch,
		}},
	}
}

// ValidateStartRunRequest rejects unbounded or provider-specific run input.
func ValidateStartRunRequest(request *StartRunRequest, limits *commonv1.Limits) error {
	if request == nil {
		return errors.New("start run request is required")
	}
	if err := validateClientMutation(
		request.GetClientId(),
		request.GetOwnershipEpoch(),
		request.GetClientOperationId(),
	); err != nil {
		return err
	}
	definition := request.GetDefinition()
	if definition == nil {
		return errors.New("agent definition reference is required")
	}
	for _, field := range [][2]string{
		{"definition ID", definition.GetId()},
		{"definition revision", definition.GetRevision()},
		{"model", definition.GetModel()},
		{"expected static plan fingerprint", definition.GetExpectedStaticPlanFingerprint()},
	} {
		if err := token(field[0], field[1], maximumTokenBytes); err != nil {
			return err
		}
	}
	if definition.GetMaxTurns() == 0 {
		return errors.New("agent definition max turns must be positive")
	}
	if err := ValidateMessage(request.GetInput()); err != nil {
		return fmt.Errorf("initial message: %w", err)
	}
	if request.GetInput().GetRole() != MessageRole_MESSAGE_ROLE_USER {
		return errors.New("initial message must have the user role")
	}
	return commonv1.ValidateEncodedSize(request, limits.GetMaxMessageBytes())
}

// ValidateMessage enforces the provider-neutral bounded message union.
func ValidateMessage(value *Message) error {
	if value == nil {
		return errors.New("message is required")
	}
	if err := token("message ID", value.GetId(), 128); err != nil {
		return err
	}
	switch value.GetRole() {
	case MessageRole_MESSAGE_ROLE_SYSTEM,
		MessageRole_MESSAGE_ROLE_USER,
		MessageRole_MESSAGE_ROLE_ASSISTANT,
		MessageRole_MESSAGE_ROLE_TOOL:
	default:
		return fmt.Errorf("message role %d is unsupported", value.GetRole())
	}
	if len(value.GetParts()) == 0 || len(value.GetParts()) > maximumMessageParts {
		return fmt.Errorf("message part count must be between 1 and %d", maximumMessageParts)
	}
	for index, part := range value.GetParts() {
		if err := validateContentPart(part); err != nil {
			return fmt.Errorf("message part %d: %w", index, err)
		}
	}
	return nil
}

// ValidateStreamEventsRequest enforces replay and connection bounds.
func ValidateStreamEventsRequest(
	request *StreamEventsRequest,
	limits *commonv1.Limits,
) error {
	if request == nil {
		return errors.New("stream events request is required")
	}
	if err := validateClient(request.GetClientId(), request.GetOwnershipEpoch()); err != nil {
		return err
	}
	if err := token("run ID", request.GetRunId(), 128); err != nil {
		return err
	}
	if err := commonv1.ValidateLimits(limits); err != nil {
		return err
	}
	if request.GetReplayLimit() == 0 || request.GetReplayLimit() > limits.GetMaxReplayEvents() {
		return fmt.Errorf("replay limit must be between 1 and %d", limits.GetMaxReplayEvents())
	}
	return commonv1.ValidateEncodedSize(request, limits.GetMaxMessageBytes())
}

// CheckReplayCursor returns typed recovery bounds when retained events cannot
// satisfy the requested cursor.
func CheckReplayCursor(
	requestedAfter,
	earliest,
	latest uint64,
) *commonv1.Status {
	validWindow := earliest > 0 && latest >= earliest
	if validWindow && requestedAfter != math.MaxUint64 &&
		requestedAfter+1 >= earliest && requestedAfter <= latest {
		return nil
	}
	recovery := uint64(0)
	if validWindow {
		recovery = earliest - 1
	}
	return &commonv1.Status{
		Code:    commonv1.ErrorCode_ERROR_CODE_OUT_OF_RANGE,
		Message: "requested event cursor is outside the retained replay window",
		Detail: &commonv1.Status_ReplayBounds{ReplayBounds: &commonv1.ReplayBounds{
			RequestedAfterSequence: requestedAfter,
			EarliestSequence:       earliest,
			LatestSequence:         latest,
			RecoverySequence:       recovery,
		}},
	}
}

// ValidateEventBatch enforces a contiguous ordered replay after one cursor.
func ValidateEventBatch(
	runID string,
	afterSequence uint64,
	events []*RunEvent,
	limits *commonv1.Limits,
) error {
	if err := token("run ID", runID, 128); err != nil {
		return err
	}
	if err := commonv1.ValidateLimits(limits); err != nil {
		return err
	}
	if len(events) > int(limits.GetMaxReplayEvents()) {
		return fmt.Errorf("event count %d exceeds %d", len(events), limits.GetMaxReplayEvents())
	}
	expected := afterSequence
	var total uint64
	for index, current := range events {
		if expected == math.MaxUint64 {
			return errors.New("event sequence overflow")
		}
		expected++
		if err := ValidateRunEvent(current); err != nil {
			return fmt.Errorf("event %d: %w", index, err)
		}
		if current.GetRunId() != runID || current.GetSequence() != expected {
			return fmt.Errorf("event %d is not the next ordered event", index)
		}
		size := proto.Size(current)
		if size < 0 {
			return fmt.Errorf("event %d has an invalid encoded size", index)
		}
		// #nosec G115 -- the explicit non-negative guard makes every int value safe in uint64.
		total += uint64(size)
		if total > limits.GetMaxReplayBytes() {
			return fmt.Errorf("event replay size %d exceeds %d", total, limits.GetMaxReplayBytes())
		}
	}
	return nil
}

// ValidateRunEvent rejects unknown lifecycle kinds and malformed envelopes.
func ValidateRunEvent(value *RunEvent) error {
	if value == nil {
		return errors.New("run event is required")
	}
	if err := token("run ID", value.GetRunId(), 128); err != nil {
		return err
	}
	if value.GetSequence() == 0 || value.GetUnixNano() <= 0 {
		return errors.New("run event requires positive sequence and timestamp")
	}
	if !knownEventKind(value.GetKind()) {
		return fmt.Errorf("run event kind %d is unsupported", value.GetKind())
	}
	if value.GetTerminal() != terminalEventKind(value.GetKind()) {
		return errors.New("run event terminal flag does not match its kind")
	}
	if len(value.GetPayloadJson()) > maximumJSONBytes ||
		(len(value.GetPayloadJson()) != 0 && !json.Valid(value.GetPayloadJson())) {
		return errors.New("run event payload must be empty or bounded valid JSON")
	}
	if value.GetOperationId() != "" {
		return token("operation ID", value.GetOperationId(), 128)
	}
	return nil
}

// OverloadStatus creates a typed non-retryable overload result.
func OverloadStatus(resource string, limit, observed uint64) *commonv1.Status {
	return &commonv1.Status{
		Code:    commonv1.ErrorCode_ERROR_CODE_RESOURCE_EXHAUSTED,
		Message: "protocol resource limit exceeded",
		Detail: &commonv1.Status_Overload{Overload: &commonv1.Overload{
			Resource: resource,
			Limit:    limit,
			Observed: observed,
		}},
	}
}

// ValidateCancelRunRequest validates an idempotent cancellation mutation.
func ValidateCancelRunRequest(request *CancelRunRequest) error {
	if request == nil {
		return errors.New("cancel run request is required")
	}
	if err := validateClientMutation(
		request.GetClientId(),
		request.GetOwnershipEpoch(),
		request.GetClientOperationId(),
	); err != nil {
		return err
	}
	if err := token("run ID", request.GetRunId(), 128); err != nil {
		return err
	}
	if request.GetReason() != "" {
		return token("cancellation reason", request.GetReason(), 1024)
	}
	return nil
}

// ValidateInteractionResponse rejects stale ownership, wrong correlation, and
// malformed structured input before it can reach the kernel broker.
func ValidateInteractionResponse(
	request *RespondInteractionRequest,
	expectedClientID string,
	expectedEpoch uint64,
	expectedRunID string,
	expectedInteractionID string,
) *commonv1.Status {
	if request == nil {
		return protocolStatus(commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "interaction response is required")
	}
	if stale := CheckClientOwnership(
		request.GetClientId(),
		request.GetOwnershipEpoch(),
		expectedClientID,
		expectedEpoch,
	); stale != nil {
		return stale
	}
	for _, field := range [][2]string{
		{"client operation ID", request.GetClientOperationId()},
		{"run ID", request.GetRunId()},
		{"interaction ID", request.GetInteractionId()},
		{"response ID", request.GetResponseId()},
	} {
		if err := token(field[0], field[1], 128); err != nil {
			return protocolStatus(commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error())
		}
	}
	if request.GetRunId() != expectedRunID || request.GetInteractionId() != expectedInteractionID {
		return protocolStatus(commonv1.ErrorCode_ERROR_CODE_CONFLICT, "interaction correlation does not match the pending request")
	}
	if len(request.GetValueJson()) == 0 || len(request.GetValueJson()) > maximumJSONBytes ||
		!json.Valid(request.GetValueJson()) {
		return protocolStatus(commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "interaction response must be bounded valid JSON")
	}
	return commonv1.OKStatus()
}

// ValidateSnapshotEnvelope checks format, safe lifecycle, bound, and digest.
func ValidateSnapshotEnvelope(value *SnapshotEnvelope) error {
	if value == nil {
		return errors.New("snapshot envelope is required")
	}
	if value.GetFormat() != SnapshotFormat {
		return fmt.Errorf("snapshot format %q is unsupported", value.GetFormat())
	}
	if err := token("snapshot run ID", value.GetRunId(), 128); err != nil {
		return err
	}
	if value.GetLastSequence() == 0 || value.GetLastSequence() == math.MaxUint64 {
		return errors.New("snapshot sequence must be positive and resumable")
	}
	switch value.GetLifecycle() {
	case SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED,
		SnapshotLifecycle_SNAPSHOT_LIFECYCLE_COMPLETED,
		SnapshotLifecycle_SNAPSHOT_LIFECYCLE_FAILED,
		SnapshotLifecycle_SNAPSHOT_LIFECYCLE_CANCELLED:
	default:
		return fmt.Errorf("snapshot lifecycle %d is unsafe", value.GetLifecycle())
	}
	if len(value.GetPayload()) == 0 || len(value.GetPayload()) > MaximumSnapshotBytes {
		return fmt.Errorf("snapshot payload must be between 1 and %d bytes", MaximumSnapshotBytes)
	}
	digest := sha256.Sum256(value.GetPayload())
	if !slices.Equal(value.GetSha256(), digest[:]) {
		return errors.New("snapshot SHA-256 digest does not match its payload")
	}
	return nil
}

// NewSnapshotEnvelope constructs a checksummed immutable-by-convention wire value.
func NewSnapshotEnvelope(
	runID string,
	lastSequence uint64,
	lifecycle SnapshotLifecycle,
	payload []byte,
) (*SnapshotEnvelope, error) {
	digest := sha256.Sum256(payload)
	result := &SnapshotEnvelope{
		Format:       SnapshotFormat,
		RunId:        runID,
		LastSequence: lastSequence,
		Lifecycle:    lifecycle,
		Payload:      slices.Clone(payload),
		Sha256:       slices.Clone(digest[:]),
	}
	if err := ValidateSnapshotEnvelope(result); err != nil {
		return nil, err
	}
	return result, nil
}

// ValidateImportSnapshotRequest rejects unsafe or mismatched resume requests.
func ValidateImportSnapshotRequest(request *ImportSnapshotRequest) error {
	if request == nil {
		return errors.New("import snapshot request is required")
	}
	if err := validateClientMutation(
		request.GetClientId(),
		request.GetOwnershipEpoch(),
		request.GetClientOperationId(),
	); err != nil {
		return err
	}
	for _, field := range [][2]string{
		{"new run ID", request.GetNewRunId()},
		{"static plan fingerprint", request.GetExpectedStaticPlanFingerprint()},
		{"expected plan ID", request.GetExpectedPlanId()},
	} {
		if err := token(field[0], field[1], maximumTokenBytes); err != nil {
			return err
		}
	}
	if err := ValidateSnapshotEnvelope(request.GetSnapshot()); err != nil {
		return err
	}
	if request.GetSnapshot().GetLifecycle() != SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED {
		return errors.New("only a suspended snapshot may be imported")
	}
	return nil
}

func validateClientMutation(clientID string, epoch uint64, operationID string) error {
	if err := validateClient(clientID, epoch); err != nil {
		return err
	}
	return token("client operation ID", operationID, 128)
}

func validateClient(clientID string, epoch uint64) error {
	if err := token("client ID", clientID, 128); err != nil {
		return err
	}
	if epoch == 0 {
		return errors.New("ownership epoch must be positive")
	}
	return nil
}

func validateContentPart(part *ContentPart) error {
	if part == nil {
		return errors.New("content part is required")
	}
	switch value := part.GetValue().(type) {
	case *ContentPart_Text:
		return validateTextPart(value)
	case *ContentPart_ToolCall:
		return validateToolCallPart(value)
	case *ContentPart_ToolResult:
		return validateToolResultPart(value)
	case *ContentPart_Extension:
		return validateExtensionPart(value)
	default:
		return errors.New("content part union is unspecified")
	}
}

func validateTextPart(value *ContentPart_Text) error {
	if value == nil {
		return errors.New("text part is required")
	}
	if value.Text == "" || len(value.Text) > maximumJSONBytes {
		return errors.New("text part must be non-empty and bounded")
	}
	return nil
}

func validateToolCallPart(value *ContentPart_ToolCall) error {
	if value == nil || value.ToolCall == nil {
		return errors.New("tool call part is required")
	}
	if err := token("tool call ID", value.ToolCall.GetCallId(), 128); err != nil {
		return err
	}
	if err := token("tool name", value.ToolCall.GetName(), 128); err != nil {
		return err
	}
	return validateJSON("tool arguments", value.ToolCall.GetArgumentsJson())
}

func validateToolResultPart(value *ContentPart_ToolResult) error {
	if value == nil || value.ToolResult == nil {
		return errors.New("tool result part is required")
	}
	if err := token("tool call ID", value.ToolResult.GetCallId(), 128); err != nil {
		return err
	}
	if err := token("tool name", value.ToolResult.GetName(), 128); err != nil {
		return err
	}
	return validateJSON("tool result", value.ToolResult.GetResultJson())
}

func validateExtensionPart(value *ContentPart_Extension) error {
	if value == nil || value.Extension == nil {
		return errors.New("extension part is required")
	}
	if err := token("extension namespace", value.Extension.GetNamespace(), maximumTokenBytes); err != nil {
		return err
	}
	return validateJSON("extension value", value.Extension.GetValueJson())
}

func validateJSON(label string, value []byte) error {
	if len(value) == 0 || len(value) > maximumJSONBytes || !json.Valid(value) {
		return fmt.Errorf("%s must be bounded valid JSON", label)
	}
	return nil
}

func knownEventKind(kind EventKind) bool {
	return kind >= EventKind_EVENT_KIND_RUN_STARTED &&
		kind <= EventKind_EVENT_KIND_INTERACTION_CANCELLED
}

func terminalEventKind(kind EventKind) bool {
	switch kind {
	case EventKind_EVENT_KIND_RUN_COMPLETED,
		EventKind_EVENT_KIND_RUN_FAILED,
		EventKind_EVENT_KIND_RUN_CANCELLED,
		EventKind_EVENT_KIND_TURN_COMPLETED,
		EventKind_EVENT_KIND_TURN_FAILED,
		EventKind_EVENT_KIND_MODEL_COMPLETED,
		EventKind_EVENT_KIND_MODEL_FAILED,
		EventKind_EVENT_KIND_TOOL_COMPLETED,
		EventKind_EVENT_KIND_TOOL_FAILED,
		EventKind_EVENT_KIND_INTERACTION_COMPLETED,
		EventKind_EVENT_KIND_INTERACTION_FAILED,
		EventKind_EVENT_KIND_INTERACTION_CANCELLED:
		return true
	default:
		return false
	}
}

func token(label, value string, maximum int) error {
	if value == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must be non-empty without surrounding whitespace", label)
	}
	if len(value) > maximum {
		return fmt.Errorf("%s exceeds %d bytes", label, maximum)
	}
	return nil
}

func protocolStatus(code commonv1.ErrorCode, message string) *commonv1.Status {
	return &commonv1.Status{Code: code, Message: message}
}

func clone[T proto.Message](value T) T {
	result, ok := proto.Clone(value).(T)
	if !ok {
		panic("protobuf clone changed concrete message type")
	}
	return result
}
