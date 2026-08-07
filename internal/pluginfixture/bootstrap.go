package pluginfixture

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spice-framework/spice-agent/daemon/localipc"
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"google.golang.org/grpc"
)

const shutdownDelay = 25 * time.Millisecond

// Serve consumes the public plugin/v1 launch bootstrap from stdin, emits the
// public readiness record to stdout, and serves plugin/v1 over the supplied
// current-user local IPC address. It is a conformance implementation, not the
// production plugin host.
func Serve(input io.Reader, output io.Writer) (returnErr error) {
	if input == nil || output == nil {
		return errors.New("fixture bootstrap streams are required")
	}
	address, secret, err := pluginv1.DecodeBootstrap(input)
	if err != nil {
		return err
	}
	defer clear(secret)
	listener, err := localipc.Listen(address)
	if err != nil {
		return fmt.Errorf("open fixture local IPC listener: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, listener.Close()) }()
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(pluginv1.InitializeBootstrapMaximumBytes),
		grpc.MaxSendMsgSize(pluginv1.InitializeBootstrapMaximumBytes),
	)
	service, err := NewService(secret, func() {
		time.AfterFunc(shutdownDelay, server.GracefulStop)
	})
	if err != nil {
		return err
	}
	pluginv1.RegisterPluginServiceServer(server, service)
	if err = pluginv1.WriteReadiness(output); err != nil {
		return fmt.Errorf("write fixture readiness: %w", err)
	}
	if err = server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("serve fixture plugin: %w", err)
	}
	return nil
}
