package grpcserver

import (
	"context"
	"errors"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type engineService struct {
	enginev1.UnimplementedEngineServiceServer

	root         context.Context //nolint:containedctx // adapter service lifetime, never an RPC lifetime.
	host         runHostBoundary
	sessions     sessionStoreBoundary
	registry     *negotiatedSessionRegistry
	build        *commonv1.BuildIdentity
	capabilities *commonv1.CapabilitySet
	limits       *commonv1.Limits
}

func (service *engineService) Initialize(
	ctx context.Context,
	request *enginev1.InitializeRequest,
) (*enginev1.InitializeResponse, error) {
	if err := service.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	description, err := service.host.Describe(ctx)
	if err != nil {
		return initializeContextOrFailure(ctx, err)
	}
	health, err := healthToWire(description.Health())
	if err != nil {
		//nolint:nilerr // application failures are protocol statuses; gRPC errors are transport-only.
		return initializeInternalFailure("daemon health is invalid"), nil
	}
	definitions, err := definitionsToWire(description.Definitions(), service.limits)
	if err != nil {
		//nolint:nilerr // application failures are protocol statuses; gRPC errors are transport-only.
		return initializeInternalFailure("daemon definitions are invalid"), nil
	}
	negotiation, failure := enginev1.PreflightInitialize(
		request,
		commonv1.SupportedProtocolRange(),
		proto.CloneOf(service.build),
		proto.CloneOf(service.capabilities),
		proto.CloneOf(service.limits),
		health,
		definitions,
	)
	if failure != nil {
		return failure, nil
	}

	claim := negotiation.ReconnectClaim()
	var session daemon.Session
	if claim == nil {
		session, err = service.sessions.Fresh()
	} else {
		session, err = service.sessions.ReconnectContext(
			ctx, claim.GetClientId(), claim.GetExpectedOwnershipEpoch(),
		)
	}
	if err != nil {
		return initializeContextOrFailure(ctx, err)
	}
	response := enginev1.CompleteInitialize(negotiation, session.ClientID(), session.Epoch())
	if response.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_OK ||
		enginev1.ValidateInitializeResponseForRequest(request, response) != nil {
		//nolint:nilerr // invalid server completion is returned as a bounded application status.
		return initializeInternalFailure("initialize completion is invalid"), nil
	}
	if claim == nil {
		err = service.registry.installFresh(session, response)
	} else {
		err = service.registry.replaceReconnect(
			claim.GetClientId(), claim.GetExpectedOwnershipEpoch(), session, response,
		)
	}
	if err != nil {
		//nolint:nilerr // registry failures are returned as bounded application statuses.
		return initializeInternalFailure("initialize ownership is unavailable"), nil
	}
	return proto.CloneOf(response), nil
}

func (service *engineService) Health(
	ctx context.Context,
	request *enginev1.HealthRequest,
) (*enginev1.HealthResponse, error) {
	if err := service.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if err := enginev1.ValidateHealthRequest(request, service.limits); err != nil {
		//nolint:nilerr // request validation failures are application statuses, not transport failures.
		return healthFailure(commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "health request is invalid"), nil
	}
	negotiated, err := service.registry.lookup(request.GetClientId(), request.GetOwnershipEpoch())
	if err != nil {
		if checkErr := service.sessions.Check(request.GetClientId(), request.GetOwnershipEpoch()); checkErr != nil {
			return healthFailureForSession(checkErr), nil
		}
		return healthFailure(commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE, "client session is unavailable"), nil
	}
	if err = service.sessions.Check(negotiated.session.ClientID(), negotiated.session.Epoch()); err != nil {
		return healthFailureForSession(err), nil
	}
	health, err := service.host.Health(ctx, negotiated.session)
	if err != nil {
		if transportErr := contextTransportError(ctx, err); transportErr != nil {
			return nil, transportErr
		}
		return healthFailureForSession(err), nil
	}
	wireHealth, err := healthToWire(health)
	if err != nil {
		//nolint:nilerr // invalid host output is a bounded application status.
		return healthFailure(commonv1.ErrorCode_ERROR_CODE_INTERNAL, "daemon health is invalid"), nil
	}
	response := &enginev1.HealthResponse{
		Status: commonv1.OKStatus(), Server: proto.CloneOf(negotiated.response.GetServer()),
		Protocol: proto.CloneOf(negotiated.response.GetProtocol()), Health: wireHealth,
	}
	if err = enginev1.ValidateHealthResponse(response, negotiated.response.GetLimits()); err != nil {
		//nolint:nilerr // invalid adapter output is a bounded application status.
		return healthFailure(commonv1.ErrorCode_ERROR_CODE_INTERNAL, "health response is invalid"), nil
	}
	return response, nil
}

func (service *engineService) requireAuthenticated(ctx context.Context) error {
	if service == nil || service.root == nil || service.host == nil || service.sessions == nil ||
		service.registry == nil || service.build == nil || service.capabilities == nil || service.limits == nil ||
		!transportAuthenticated(ctx) {
		return unauthenticatedTransport()
	}
	if service.root.Err() != nil {
		return status.Error(codes.Unavailable, "local daemon is stopping")
	}
	return nil
}

func initializeContextOrFailure(
	ctx context.Context,
	err error,
) (*enginev1.InitializeResponse, error) {
	if transportErr := contextTransportError(ctx, err); transportErr != nil {
		return nil, transportErr
	}
	return initializeFailureForSession(err), nil
}

func contextTransportError(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) || ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		return status.Error(codes.Canceled, context.Canceled.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) || ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
	}
	return nil
}

func initializeFailureForSession(err error) *enginev1.InitializeResponse {
	statusValue := sessionFailureStatus(err)
	return &enginev1.InitializeResponse{Status: statusValue}
}

func initializeInternalFailure(message string) *enginev1.InitializeResponse {
	return &enginev1.InitializeResponse{Status: &commonv1.Status{
		Code: commonv1.ErrorCode_ERROR_CODE_INTERNAL, Message: message,
	}}
}

func healthFailureForSession(err error) *enginev1.HealthResponse {
	return &enginev1.HealthResponse{Status: sessionFailureStatus(err)}
}

func healthFailure(code commonv1.ErrorCode, message string) *enginev1.HealthResponse {
	return &enginev1.HealthResponse{Status: &commonv1.Status{Code: code, Message: message}}
}

func sessionFailureStatus(err error) *commonv1.Status {
	var stale *daemon.StaleSessionError
	if errors.As(err, &stale) && stale.ExpectedEpoch() != 0 {
		return &commonv1.Status{
			Code: commonv1.ErrorCode_ERROR_CODE_STALE_CLIENT, Message: "client ownership epoch is stale",
			Detail: &commonv1.Status_StaleClient{StaleClient: &commonv1.StaleClient{
				ExpectedEpoch: stale.ExpectedEpoch(), ObservedEpoch: stale.ObservedEpoch(),
			}},
		}
	}
	var capacity *daemon.SessionGateCapacityError
	if errors.As(err, &capacity) && capacity.Maximum() > 0 {
		return &commonv1.Status{
			Code: commonv1.ErrorCode_ERROR_CODE_RESOURCE_EXHAUSTED, Message: "client session capacity is exhausted",
			Detail: &commonv1.Status_Overload{Overload: &commonv1.Overload{
				Resource: capacity.Resource(), Limit: uint64(capacity.Maximum()), // #nosec G115 -- maximum is validated positive and bounded.
				Observed: uint64(capacity.Maximum()) + 1, // #nosec G115 -- maximum is validated positive and bounded.
			}},
		}
	}
	return &commonv1.Status{
		Code:    commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE,
		Message: "client session is unavailable", Retryable: true,
	}
}
