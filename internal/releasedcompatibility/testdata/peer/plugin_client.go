package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"runtime"
	"time"

	"github.com/spice-framework/spice-agent/daemon/localipc"
	"github.com/spice-framework/spice-agent/plugin/conformance"
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"github.com/spice-framework/spice-agent/tool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type PluginClient struct {
	config ProcessConfig
	output io.Writer
}

func NewPluginClient(config ProcessConfig, output io.Writer) (*PluginClient, error) {
	if config.Secret == "" || output == nil {
		return nil, errors.New("plugin client configuration is incomplete")
	}
	return &PluginClient{config: config, output: output}, nil
}

func (clientValue *PluginClient) Run() error {
	secret, err := base64.RawURLEncoding.DecodeString(clientValue.config.Secret)
	if err != nil || len(secret) != pluginv1.HandshakeSecretBytes {
		return errors.New("plugin secret is invalid")
	}
	defer clear(secret)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := grpc.NewClient(
		"passthrough:///spice-released-plugin-peer",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(dialContext context.Context, _ string) (net.Conn, error) {
			return localipc.Dial(dialContext, clientValue.config.Address)
		}),
		grpc.WithNoProxy(),
		grpc.WithDisableRetry(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(pluginv1.InitializeBootstrapMaximumBytes),
			grpc.MaxCallSendMsgSize(pluginv1.InitializeBootstrapMaximumBytes),
		),
	)
	if err != nil {
		return err
	}
	service := pluginv1.NewPluginServiceClient(connection)
	err = conformance.Run(ctx, service, conformance.Config{
		HostBuild: &pluginv1.BuildIdentity{
			Component: "released-generation-conformance-client",
			Version:   "cross-generation",
			Commit:    "public-module",
			Runtime:   runtime.Version(),
		},
		Limits: &pluginv1.Limits{
			MaxMessageBytes:      pluginv1.InitializeBootstrapMaximumBytes,
			MaxTools:             16,
			MaxSchemaBytes:       64 << 10,
			MaxCallArgumentBytes: 64 << 10,
			MaxResultBytes:       pluginv1.InitializeBootstrapMaximumBytes,
			MaxProgressBytes:     tool.MaximumProgressBytes,
			MaxConcurrentCalls:   8,
		},
		Secret:           secret,
		OperationTimeout: 3 * time.Second,
	})
	closeErr := connection.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return json.NewEncoder(clientValue.output).Encode(PluginResult{Protocol: "1.0.0", Conformant: true})
}
