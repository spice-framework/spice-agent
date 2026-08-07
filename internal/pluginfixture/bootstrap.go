package pluginfixture

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spice-framework/spice-agent/daemon/localipc"
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"google.golang.org/grpc"
)

const (
	maximumBootstrapBytes = 4096
	shutdownDelay         = 25 * time.Millisecond
)

type bootstrapRequest struct {
	Address string `json:"address"`
	Secret  string `json:"secret"`
}

// Serve reads exactly one bounded fixture-only bootstrap object from stdin,
// emits exactly one readiness record to stdout, and serves plugin/v1 over the
// supplied current-user local IPC address. It is conformance infrastructure,
// not the production plugin host or launch contract.
func Serve(input io.Reader, output io.Writer) (returnErr error) {
	if input == nil || output == nil {
		return errors.New("fixture bootstrap streams are required")
	}
	request, secret, err := decodeBootstrap(input)
	if err != nil {
		return err
	}
	defer clear(secret)
	listener, err := localipc.Listen(request.Address)
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
	if _, err = io.WriteString(output, "{\"ready\":true}\n"); err != nil {
		return fmt.Errorf("write fixture readiness: %w", err)
	}
	if flusher, ok := output.(interface{ Flush() error }); ok {
		if err = flusher.Flush(); err != nil {
			return fmt.Errorf("flush fixture readiness: %w", err)
		}
	}
	if err = server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("serve fixture plugin: %w", err)
	}
	return nil
}

func decodeBootstrap(input io.Reader) (bootstrapRequest, []byte, error) {
	limited := &io.LimitedReader{R: input, N: maximumBootstrapBytes + 1}
	decoder := json.NewDecoder(bufio.NewReader(limited))
	decoder.DisallowUnknownFields()
	var request bootstrapRequest
	if err := decoder.Decode(&request); err != nil {
		return bootstrapRequest{}, nil, errors.New("decode fixture bootstrap")
	}
	if limited.N <= 0 {
		return bootstrapRequest{}, nil, errors.New("fixture bootstrap exceeds its byte limit")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return bootstrapRequest{}, nil, errors.New("fixture bootstrap contains trailing data")
	}
	secret, err := base64.RawURLEncoding.DecodeString(request.Secret)
	if err != nil || len(secret) != pluginv1.HandshakeSecretBytes {
		clear(secret)
		return bootstrapRequest{}, nil, errors.New("fixture bootstrap secret is invalid")
	}
	if request.Address == "" {
		clear(secret)
		return bootstrapRequest{}, nil, errors.New("fixture bootstrap address is required")
	}
	return request, secret, nil
}
