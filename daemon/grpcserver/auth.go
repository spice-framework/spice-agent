// Package grpcserver implements the authenticated local gRPC process boundary
// without adding transport dependencies to the daemon lifecycle core.
package grpcserver

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	// AuthenticationMetadataKey is the standard gRPC metadata key carrying the
	// user-local daemon bearer credential.
	AuthenticationMetadataKey = "authorization"

	endpointTokenBytes            = 32
	endpointTokenAttempts         = 4
	endpointBearerPrefix          = "Bearer "
	endpointAuthenticationFailure = "local daemon authentication failed"
)

// EndpointToken is one opaque 256-bit user-local daemon credential. It is not
// a snapshot authority key and must never enter Protobuf payloads, logs,
// generated files, events, or errors. Indirection prevents fmt's special %p
// fallback from recursively printing credential bytes.
type EndpointToken struct{ state *endpointTokenState }

type endpointTokenState struct{ value [endpointTokenBytes]byte }

// GenerateEndpointToken creates a credential from the operating-system CSPRNG.
func GenerateEndpointToken() (EndpointToken, error) { return generateEndpointToken(rand.Reader) }

func generateEndpointToken(random io.Reader) (EndpointToken, error) {
	if random == nil {
		return EndpointToken{}, errors.New("endpoint token randomness is nil")
	}
	for range endpointTokenAttempts {
		token := EndpointToken{state: &endpointTokenState{}}
		if _, err := io.ReadFull(random, token.state.value[:]); err != nil {
			return EndpointToken{}, fmt.Errorf("generate endpoint token: %w", err)
		}
		if token.valid() {
			return token, nil
		}
	}
	return EndpointToken{}, errors.New("generate nonzero endpoint token")
}

// ParseEndpointToken decodes the canonical unpadded base64url credential form.
func ParseEndpointToken(encoded string) (EndpointToken, error) {
	if len(encoded) != base64.RawURLEncoding.EncodedLen(endpointTokenBytes) ||
		strings.TrimSpace(encoded) != encoded {
		return EndpointToken{}, errors.New("endpoint token encoding is invalid")
	}
	token := EndpointToken{state: &endpointTokenState{}}
	written, err := base64.RawURLEncoding.Decode(token.state.value[:], []byte(encoded))
	if err != nil || written != endpointTokenBytes || !token.valid() ||
		base64.RawURLEncoding.EncodeToString(token.state.value[:]) != encoded {
		return EndpointToken{}, errors.New("endpoint token encoding is invalid")
	}
	return token, nil
}

// AuthorizationValue returns the explicit Bearer value for endpoint metadata.
// Callers must handle it as a secret and must not log or persist it outside the
// user-only endpoint metadata file.
func (token EndpointToken) AuthorizationValue() (string, error) {
	if !token.valid() {
		return "", errors.New("endpoint token is invalid")
	}
	return endpointBearerPrefix + base64.RawURLEncoding.EncodeToString(token.state.value[:]), nil
}

// String prevents accidental formatting from exposing credential bytes.
func (EndpointToken) String() string { return "[REDACTED endpoint token]" }

// GoString prevents %#v formatting from exposing credential bytes.
func (EndpointToken) GoString() string { return "grpcserver.EndpointToken([REDACTED])" }

// MarshalJSON prevents structured encoders from exposing token representation
// details while still making accidental serialization visibly redacted.
func (EndpointToken) MarshalJSON() ([]byte, error) {
	return []byte(`"[REDACTED endpoint token]"`), nil
}

// Format prevents every fmt verb, flag, width, and precision from reflecting
// the private byte array. It deliberately ignores the caller's presentation.
func (EndpointToken) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[REDACTED endpoint token]")
}

func (token EndpointToken) valid() bool {
	if token.state == nil {
		return false
	}
	var zero [endpointTokenBytes]byte
	return subtle.ConstantTimeCompare(token.state.value[:], zero[:]) == 0
}

type transportAuthenticationKey struct{}

func transportAuthenticated(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	authenticated, _ := ctx.Value(transportAuthenticationKey{}).(bool)
	return authenticated
}

// newAuthenticationInterceptors constructs matching unary and streaming
// middleware. It remains private so the eventual server constructor can make
// installing both paths mandatory. Authentication happens after gRPC framing
// and decode but before an application handler may inspect daemon state.
func newAuthenticationInterceptors(
	token EndpointToken,
) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor, error) {
	if !token.valid() {
		return nil, nil, errors.New("endpoint authentication token is invalid")
	}
	unary := func(
		ctx context.Context,
		request any,
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		authenticated, err := authenticateTransportContext(ctx, token)
		if err != nil {
			return nil, err
		}
		return handler(authenticated, request)
	}
	stream := func(
		service any,
		server grpc.ServerStream,
		_ *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if server == nil {
			return unauthenticatedTransport()
		}
		authenticated, err := authenticateTransportContext(server.Context(), token)
		if err != nil {
			return err
		}
		return handler(service, authenticatedServerStream{ServerStream: server, ctx: authenticated})
	}
	return unary, stream, nil
}

func authenticateTransportContext(ctx context.Context, expected EndpointToken) (context.Context, error) {
	if ctx == nil {
		return nil, unauthenticatedTransport()
	}
	values, present := metadata.FromIncomingContext(ctx)
	if !present {
		return nil, unauthenticatedTransport()
	}
	authorization := values.Get(AuthenticationMetadataKey)
	if len(authorization) != 1 {
		return nil, unauthenticatedTransport()
	}
	presented, err := parseBearerToken(authorization[0])
	if err != nil || subtle.ConstantTimeCompare(presented.state.value[:], expected.state.value[:]) != 1 {
		return nil, unauthenticatedTransport()
	}
	return context.WithValue(ctx, transportAuthenticationKey{}, true), nil
}

func parseBearerToken(value string) (EndpointToken, error) {
	if !strings.HasPrefix(value, endpointBearerPrefix) || len(value) !=
		len(endpointBearerPrefix)+base64.RawURLEncoding.EncodedLen(endpointTokenBytes) {
		return EndpointToken{}, errors.New("bearer credential is invalid")
	}
	return ParseEndpointToken(strings.TrimPrefix(value, endpointBearerPrefix))
}

func unauthenticatedTransport() error {
	return status.Error(codes.Unauthenticated, endpointAuthenticationFailure)
}

type authenticatedServerStream struct {
	grpc.ServerStream
	ctx context.Context //nolint:containedctx // immutable wrapper replaces only the authenticated stream context.
}

func (stream authenticatedServerStream) Context() context.Context { return stream.ctx }
