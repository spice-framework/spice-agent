package grpcserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestEndpointTokenGenerationEncodingAndRedaction(t *testing.T) {
	t.Parallel()
	raw := make([]byte, endpointTokenBytes)
	for index := range raw {
		raw[index] = byte(index + 1) // #nosec G115 -- the fixture index is bounded to 32.
	}
	token, err := generateEndpointToken(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := token.AuthorizationValue()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(authorization, endpointBearerPrefix) || strings.Contains(fmt.Sprint(token), authorization) ||
		strings.Contains(fmt.Sprintf("%#v", token), authorization) {
		t.Fatal("endpoint token formatting or authorization form is invalid")
	}
	for _, format := range []string{
		"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%o", "%b", "%c", "%U", "%p", "%T", "%20.8v",
	} {
		formatted := fmt.Sprintf(format, token)
		if strings.Contains(formatted, strings.TrimPrefix(authorization, endpointBearerPrefix)) ||
			strings.Contains(formatted, "1 2 3 4 5 6 7 8") {
			t.Fatalf("endpoint token format %q exposed credential data: %q", format, formatted)
		}
		if format != "%p" && format != "%T" && formatted != "[REDACTED endpoint token]" {
			t.Fatalf("endpoint token format %q = %q", format, formatted)
		}
	}
	if formattedErr := fmt.Errorf("credential: %v", token); formattedErr.Error() != "credential: [REDACTED endpoint token]" {
		t.Fatalf("formatted endpoint token error = %q", formattedErr)
	}
	var logOutput bytes.Buffer
	slog.New(slog.NewJSONHandler(&logOutput, nil)).Info("credential canary", "token", token)
	encodedJSON, marshalErr := json.Marshal(token)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	secret := strings.TrimPrefix(authorization, endpointBearerPrefix)
	if strings.Contains(logOutput.String(), secret) || strings.Contains(string(encodedJSON), secret) ||
		string(encodedJSON) != `"[REDACTED endpoint token]"` {
		t.Fatal("structured formatting exposed the endpoint token")
	}
	parsed, err := ParseEndpointToken(strings.TrimPrefix(authorization, endpointBearerPrefix))
	parsedAuthorization, parsedAuthorizationErr := parsed.AuthorizationValue()
	if err != nil || parsedAuthorizationErr != nil || parsedAuthorization != authorization {
		t.Fatalf("parsed endpoint token = %v, %v", parsed, err)
	}

	for name, encoded := range map[string]string{
		"empty":  "",
		"padded": strings.TrimPrefix(authorization, endpointBearerPrefix) + "=",
		"space":  " " + strings.TrimPrefix(authorization, endpointBearerPrefix),
		"zero":   strings.Repeat("A", base64TokenLength()),
		"syntax": strings.Repeat("!", base64TokenLength()),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, parseErr := ParseEndpointToken(encoded); parseErr == nil {
				t.Fatal("invalid endpoint token was accepted")
			}
		})
	}
	if _, err = generateEndpointToken(bytes.NewReader(nil)); err == nil {
		t.Fatal("short token randomness was accepted")
	}
	if _, err = generateEndpointToken(bytes.NewReader(make([]byte, endpointTokenBytes*endpointTokenAttempts))); err == nil {
		t.Fatal("all-zero token randomness was accepted")
	}
	if _, err = (EndpointToken{}).AuthorizationValue(); err == nil {
		t.Fatal("zero endpoint token produced authorization metadata")
	}
}

func TestEndpointTokenPublicGenerationAndDirectRedaction(t *testing.T) {
	t.Parallel()
	token, err := GenerateEndpointToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = token.AuthorizationValue(); err != nil {
		t.Fatal(err)
	}
	if token.String() != "[REDACTED endpoint token]" || token.GoString() != "grpcserver.EndpointToken([REDACTED])" {
		t.Fatal("direct token formatting was not redacted")
	}
	//nolint:staticcheck // Boundary coverage intentionally proves nil contexts fail closed.
	if transportAuthenticated(nil) {
		t.Fatal("nil context was authenticated")
	}
}

func TestAuthenticationInterceptorsRejectBeforeHandlers(t *testing.T) {
	t.Parallel()
	if unary, stream, err := newAuthenticationInterceptors(EndpointToken{}); err == nil || unary != nil || stream != nil {
		t.Fatalf("zero-token interceptors = %v, %v, %v", unary, stream, err)
	}
	token := endpointTokenFixture(t, 1)
	other := endpointTokenFixture(t, 2)
	unary, stream, err := newAuthenticationInterceptors(token)
	if err != nil {
		t.Fatal(err)
	}
	good, _ := token.AuthorizationValue()
	wrong, _ := other.AuthorizationValue()

	for name, values := range map[string][]string{
		"missing":   nil,
		"wrong":     {wrong},
		"duplicate": {good, good},
		"scheme":    {"Basic " + strings.TrimPrefix(good, endpointBearerPrefix)},
		"case":      {"bearer " + strings.TrimPrefix(good, endpointBearerPrefix)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			if values != nil {
				ctx = metadata.NewIncomingContext(ctx, metadata.MD{AuthenticationMetadataKey: values})
			}
			var unaryCalled atomic.Bool
			_, unaryErr := unary(ctx, nil, nil, func(context.Context, any) (any, error) {
				unaryCalled.Store(true)
				return struct{}{}, nil
			})
			if !isSecretSafeAuthenticationFailure(unaryErr, good, wrong) || unaryCalled.Load() {
				t.Fatalf("unary rejection = %v, called %v", unaryErr, unaryCalled.Load())
			}
			serverStream := &authenticationFixtureStream{ctx: ctx}
			var streamCalled atomic.Bool
			streamErr := stream(nil, serverStream, nil, func(_ any, _ grpc.ServerStream) error {
				streamCalled.Store(true)
				return nil
			})
			if !isSecretSafeAuthenticationFailure(streamErr, good, wrong) || streamCalled.Load() {
				t.Fatalf("stream rejection = %v, called %v", streamErr, streamCalled.Load())
			}
		})
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(AuthenticationMetadataKey, good))
	requestMarker := &struct{}{}
	info := &grpc.UnaryServerInfo{FullMethod: "/fixture.Unary"}
	if _, err = unary(ctx, requestMarker, info, func(handlerContext context.Context, request any) (any, error) {
		if !transportAuthenticated(handlerContext) {
			t.Fatal("unary handler context is not authenticated")
		}
		if request != requestMarker || handlerContext.Err() != nil {
			t.Fatal("unary interceptor changed the request or context lifetime")
		}
		return struct{}{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	serverStream := &authenticationFixtureStream{ctx: ctx}
	serviceMarker := &struct{}{}
	streamInfo := &grpc.StreamServerInfo{FullMethod: "/fixture.Stream", IsServerStream: true}
	if err = stream(serviceMarker, serverStream, streamInfo, func(service any, authenticated grpc.ServerStream) error {
		if !transportAuthenticated(authenticated.Context()) {
			t.Fatal("stream handler context is not authenticated")
		}
		if service != serviceMarker {
			t.Fatal("stream interceptor changed the service receiver")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = stream(nil, nil, nil, func(any, grpc.ServerStream) error {
		t.Fatal("nil stream reached its handler")
		return nil
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("nil server stream = %v", err)
	}
}

func TestAuthenticationInterceptorsOperateOnRealGRPCBoundary(t *testing.T) {
	t.Parallel()
	token := endpointTokenFixture(t, 3)
	unary, stream, err := newAuthenticationInterceptors(token)
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(grpc.UnaryInterceptor(unary), grpc.StreamInterceptor(stream))
	fixture := &authenticationFixtureService{}
	enginev1.RegisterEngineServiceServer(server, fixture)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	connection, err := grpc.NewClient(
		"passthrough:///spice-agent-auth-fixture",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := enginev1.NewEngineServiceClient(connection)

	var unaryHeader, unaryTrailer metadata.MD
	if _, err = client.Health(
		t.Context(),
		&enginev1.HealthRequest{},
		grpc.Header(&unaryHeader),
		grpc.Trailer(&unaryTrailer),
	); !isSecretSafeAuthenticationFailure(err, authorizationValues(token)...) ||
		metadataContainsSecrets(unaryHeader, authorizationValues(token)...) ||
		metadataContainsSecrets(unaryTrailer, authorizationValues(token)...) {
		t.Fatalf("unauthenticated unary = %v", err)
	}
	streamClient, streamErr := client.StreamEvents(t.Context(), &enginev1.StreamEventsRequest{})
	if streamErr == nil {
		_, streamErr = streamClient.Recv()
	}
	var streamTrailer metadata.MD
	if streamClient != nil {
		streamTrailer = streamClient.Trailer()
	}
	if !isSecretSafeAuthenticationFailure(streamErr, authorizationValues(token)...) ||
		metadataContainsSecrets(streamTrailer, authorizationValues(token)...) {
		t.Fatalf("unauthenticated stream = %v", streamErr)
	}
	if fixture.calls.Load() != 0 {
		t.Fatalf("unauthenticated handlers called %d times", fixture.calls.Load())
	}

	authorization, _ := token.AuthorizationValue()
	wrongAuthorization, _ := endpointTokenFixture(t, 4).AuthorizationValue()
	for name, values := range map[string][]string{
		"good and wrong": {authorization, wrongAuthorization},
		"duplicate good": {authorization, authorization},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			duplicateContext := metadata.NewOutgoingContext(
				t.Context(), metadata.MD{AuthenticationMetadataKey: values},
			)
			_, duplicateErr := client.Health(duplicateContext, &enginev1.HealthRequest{})
			if !isSecretSafeAuthenticationFailure(duplicateErr, authorization, wrongAuthorization) {
				t.Fatalf("duplicate credential result = %v", duplicateErr)
			}
		})
	}
	ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs(AuthenticationMetadataKey, authorization))
	if _, err = client.Health(ctx, &enginev1.HealthRequest{}); err != nil {
		t.Fatal(err)
	}
	streamClient, err = client.StreamEvents(ctx, &enginev1.StreamEventsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = streamClient.Recv(); err != nil {
		t.Fatal(err)
	}
	if fixture.calls.Load() != 2 {
		t.Fatalf("authenticated handlers called %d times", fixture.calls.Load())
	}
}

func endpointTokenFixture(t *testing.T, seed byte) EndpointToken {
	t.Helper()
	raw := bytes.Repeat([]byte{seed}, endpointTokenBytes)
	token, err := generateEndpointToken(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func base64TokenLength() int {
	value, _ := endpointTokenFixtureValue().AuthorizationValue()
	return len(strings.TrimPrefix(value, endpointBearerPrefix))
}

func endpointTokenFixtureValue() EndpointToken {
	token := EndpointToken{state: &endpointTokenState{}}
	token.state.value[0] = 1
	return token
}

func authorizationValues(tokens ...EndpointToken) []string {
	values := make([]string, 0, len(tokens)*2)
	for _, token := range tokens {
		value, err := token.AuthorizationValue()
		if err != nil {
			continue
		}
		values = append(values, value, strings.TrimPrefix(value, endpointBearerPrefix))
	}
	return values
}

func isSecretSafeAuthenticationFailure(err error, secrets ...string) bool {
	if status.Code(err) != codes.Unauthenticated || status.Convert(err).Message() != endpointAuthenticationFailure {
		return false
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(err.Error(), secret) {
			return false
		}
	}
	return true
}

func metadataContainsSecrets(values metadata.MD, secrets ...string) bool {
	for key, entries := range values {
		for _, value := range append(entries, key) {
			for _, secret := range secrets {
				if secret != "" && strings.Contains(value, secret) {
					return true
				}
			}
		}
	}
	return false
}

type authenticationFixtureStream struct {
	grpc.ServerStream
	ctx context.Context //nolint:containedctx // immutable test double context.
}

func (stream *authenticationFixtureStream) Context() context.Context { return stream.ctx }

type authenticationFixtureService struct {
	enginev1.UnimplementedEngineServiceServer
	calls atomic.Int32
}

func (service *authenticationFixtureService) Health(
	ctx context.Context,
	_ *enginev1.HealthRequest,
) (*enginev1.HealthResponse, error) {
	if !transportAuthenticated(ctx) {
		return nil, errors.New("handler received unauthenticated context")
	}
	service.calls.Add(1)
	return &enginev1.HealthResponse{}, nil
}

func (service *authenticationFixtureService) StreamEvents(
	_ *enginev1.StreamEventsRequest,
	stream grpc.ServerStreamingServer[enginev1.StreamEventsResponse],
) error {
	if !transportAuthenticated(stream.Context()) {
		return errors.New("handler received unauthenticated context")
	}
	service.calls.Add(1)
	return stream.Send(&enginev1.StreamEventsResponse{})
}
