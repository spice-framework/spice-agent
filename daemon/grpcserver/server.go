package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"

	"github.com/spice-framework/spice-agent/client"
	"github.com/spice-framework/spice-agent/daemon"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/grpc"
)

// ServerConfig contains the generated daemon dependencies and immutable public
// build facts for one local engine server. The server opens no listener and
// does not own Host or Sessions.
type ServerConfig struct {
	Root            context.Context //nolint:containedctx // transferred as the server service lifetime.
	EndpointToken   EndpointToken
	Host            *daemon.RunHost
	Sessions        *daemon.SessionStore
	Build           client.Build
	Capabilities    []string
	MaximumSessions int
}

type runHostBoundary interface {
	Describe(context.Context) (daemon.RunHostDescription, error)
	Health(context.Context, daemon.Session) (client.Health, error)
}

type sessionStoreBoundary interface {
	Fresh() (daemon.Session, error)
	ReconnectContext(context.Context, string, uint64) (daemon.Session, error)
	Check(string, uint64) error
}

type serverDependencies struct {
	root            context.Context //nolint:containedctx // transferred as the adapter service lifetime.
	token           EndpointToken
	host            runHostBoundary
	sessions        sessionStoreBoundary
	build           client.Build
	capabilities    []string
	maximumSessions int
}

// Server is one authenticated local gRPC boundary. It wraps gRPC so callers
// cannot register the engine service without both authentication interceptors.
type Server struct {
	grpc     *grpc.Server
	registry *negotiatedSessionRegistry
	cancel   context.CancelFunc
}

// NewServer constructs the authenticated boundary without opening an OS
// listener. Generated applications remain responsible for endpoint ownership.
func NewServer(config ServerConfig) (*Server, error) {
	if config.Host == nil || config.Sessions == nil {
		return nil, errors.New("gRPC server requires run host and session store")
	}
	return newServer(serverDependencies{
		root: config.Root, token: config.EndpointToken, host: config.Host,
		sessions: config.Sessions, build: config.Build,
		capabilities: config.Capabilities, maximumSessions: config.MaximumSessions,
	})
}

func newServer(dependencies serverDependencies) (*Server, error) {
	if dependencies.root == nil || dependencies.host == nil || dependencies.sessions == nil {
		return nil, errors.New("gRPC server requires root, run host, and session store")
	}
	if err := dependencies.root.Err(); err != nil {
		return nil, errors.New("gRPC server root is already canceled")
	}
	build, err := buildToWire(dependencies.build)
	if err != nil {
		return nil, err
	}
	capabilities, err := capabilitiesToWire(dependencies.capabilities)
	if err != nil {
		return nil, err
	}
	description, err := dependencies.host.Describe(dependencies.root)
	if err != nil {
		return nil, fmt.Errorf("describe run host: %w", err)
	}
	if err = description.Validate(); err != nil {
		return nil, fmt.Errorf("validate run host description: %w", err)
	}
	limits, err := limitsToWire(description.Health().Limits())
	if err != nil {
		return nil, err
	}
	if _, err = healthToWire(description.Health()); err != nil {
		return nil, err
	}
	if _, err = definitionsToWire(description.Definitions(), limits); err != nil {
		return nil, err
	}
	maximumMessageBytes := max(limits.GetMaxMessageBytes(), uint64(enginev1.InitializeBootstrapMaximumBytes))
	if maximumMessageBytes > uint64(math.MaxInt) {
		return nil, errors.New("gRPC server message limit exceeds platform integer capacity")
	}
	unary, stream, err := newAuthenticationInterceptors(dependencies.token)
	if err != nil {
		return nil, err
	}
	root, cancel := context.WithCancel(dependencies.root)
	registry, err := newNegotiatedSessionRegistry(root, dependencies.maximumSessions)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("construct negotiated session registry: %w", err)
	}
	service := &engineService{
		root: root, host: dependencies.host, sessions: dependencies.sessions,
		registry: registry, build: build, capabilities: capabilities, limits: limits,
	}
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(int(maximumMessageBytes)),
		grpc.MaxSendMsgSize(int(maximumMessageBytes)),
		grpc.UnaryInterceptor(unary),
		grpc.StreamInterceptor(stream),
	)
	enginev1.RegisterEngineServiceServer(server, service)
	return &Server{grpc: server, registry: registry, cancel: cancel}, nil
}

// Serve accepts authenticated connections from the caller-owned listener.
func (server *Server) Serve(listener net.Listener) error {
	if server == nil || server.grpc == nil || listener == nil {
		return errors.New("gRPC server and listener are required")
	}
	return server.grpc.Serve(listener)
}

// Stop immediately stops gRPC work and closes negotiated-session lookup. It
// does not close the transport-independent RunHost or SessionStore.
func (server *Server) Stop() {
	if server == nil {
		return
	}
	if server.grpc != nil {
		server.grpc.Stop()
	}
	if server.cancel != nil {
		server.cancel()
	}
	if server.registry != nil {
		server.registry.close()
	}
}

// GracefulStop stops admission, waits for accepted RPCs, and then releases the
// adapter's negotiated-session state. Long-lived streams must be canceled by
// the owning daemon lifetime before calling this method.
func (server *Server) GracefulStop() {
	if server == nil {
		return
	}
	if server.grpc != nil {
		server.grpc.GracefulStop()
	}
	if server.cancel != nil {
		server.cancel()
	}
	if server.registry != nil {
		server.registry.close()
	}
}
