package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"time"

	"github.com/spice-framework/spice-agent/client"
	"github.com/spice-framework/spice-agent/client/grpcclient"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon/endpoint"
	"github.com/spice-framework/spice-agent/daemon/localipc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type EngineClient struct {
	config ProcessConfig
	output io.Writer
}

func NewEngineClient(config ProcessConfig, output io.Writer) (*EngineClient, error) {
	if config.Authorization == "" || output == nil {
		return nil, errors.New("engine client configuration is incomplete")
	}
	return &EngineClient{config: config, output: output}, nil
}

func (clientValue *EngineClient) Run() error {
	token, err := endpoint.ParseAuthorizationValue(clientValue.config.Authorization)
	if err != nil {
		return errors.New("engine authorization is invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connection, err := grpc.NewClient(
		"passthrough:///spice-released-engine-peer",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDisableServiceConfig(),
		grpc.WithContextDialer(func(dialContext context.Context, _ string) (net.Conn, error) {
			return localipc.Dial(dialContext, clientValue.config.Address)
		}),
	)
	if err != nil {
		return err
	}
	defer connection.Close()
	request, err := clientValue.initializeRequest()
	if err != nil {
		return err
	}
	wrongToken, err := endpoint.GenerateToken()
	if err != nil {
		return err
	}
	wrongConnector, err := grpcclient.New(grpcclient.Config{Connection: connection, Token: wrongToken})
	if err != nil {
		return err
	}
	_, wrongErr := wrongConnector.Initialize(ctx, request)
	statusFailure, rejected := errors.AsType[*client.StatusError](wrongErr)
	if !rejected || statusFailure.Code() != client.ErrorUnauthenticated {
		return errors.New("wrong endpoint token was not rejected")
	}
	connector, err := grpcclient.New(grpcclient.Config{Connection: connection, Token: token})
	if err != nil {
		return err
	}
	session, err := connector.Initialize(ctx, request)
	if err != nil {
		return err
	}
	defer session.Close()
	completed, err := clientValue.startRun(ctx, session, "released.engine.complete", "hello")
	if err != nil {
		return err
	}
	completedKind, completedText, err := clientValue.consumeRun(ctx, session, completed.Run())
	if err != nil {
		return err
	}
	if completedKind != client.EventRunCompleted || completedText != "released peer handled: hello" {
		return fmt.Errorf("completed run returned an unexpected terminal or text")
	}
	cancelled, err := clientValue.startRun(ctx, session, "released.engine.cancel", "wait for cancellation")
	if err != nil {
		return err
	}
	cancelOperation, err := client.NewOperationID("released.engine.cancel.operation")
	if err != nil {
		return err
	}
	cancelRequest, err := client.NewCancelRequest(cancelled.Run(), cancelOperation, "released compatibility cancellation")
	if err != nil {
		return err
	}
	if _, err = session.Cancel(ctx, cancelRequest); err != nil {
		return err
	}
	cancelKind, _, err := clientValue.consumeRun(ctx, session, cancelled.Run())
	if err != nil {
		return err
	}
	if cancelKind != client.EventRunCancelled {
		return errors.New("cancelled run did not reach the cancelled terminal")
	}
	var health client.Health
	deadline := time.Now().Add(5 * time.Second)
	for {
		health, err = session.Health(ctx)
		if err == nil && health.ActiveRuns() == 0 {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("engine active runs did not drain")
		}
		time.Sleep(10 * time.Millisecond)
	}
	result := EngineResult{
		Protocol:             fmt.Sprintf("%d.%d.%d", session.Connection().Protocol().Major(), session.Connection().Protocol().Minor(), session.Connection().Protocol().Patch()),
		WrongTokenRejected:   true,
		CompletedText:        completedText,
		CancellationTerminal: "cancelled",
		ActiveRuns:           health.ActiveRuns(),
	}
	return json.NewEncoder(clientValue.output).Encode(result)
}

func (clientValue *EngineClient) initializeRequest() (client.InitializeRequest, error) {
	version, err := client.NewProtocolVersion(commonv1.ProtocolMajor, commonv1.ProtocolMinor, commonv1.ProtocolPatch)
	if err != nil {
		return client.InitializeRequest{}, err
	}
	protocol, err := client.NewProtocolRange(version, version)
	if err != nil {
		return client.InitializeRequest{}, err
	}
	build, err := client.NewBuild("released-generation-client", "cross-generation", "public-module", runtime.Version())
	if err != nil {
		return client.InitializeRequest{}, err
	}
	limits, err := client.NewLimits(2<<20, 256, 128, 2<<20, 8, 8)
	if err != nil {
		return client.InitializeRequest{}, err
	}
	attempt, err := client.NewInitializationAttemptID()
	if err != nil {
		return client.InitializeRequest{}, err
	}
	return client.NewInitializeRequestWithAttempt(
		protocol, build, []string{"events"}, []string{"events"}, limits, attempt,
	)
}

func (clientValue *EngineClient) startRun(
	ctx context.Context,
	session client.Session,
	prefix string,
	text string,
) (client.StartResult, error) {
	operation, err := client.NewOperationID(prefix + ".start")
	if err != nil {
		return client.StartResult{}, err
	}
	definition, err := client.NewDefinitionRef("worker", "revision-1")
	if err != nil {
		return client.StartResult{}, err
	}
	input, err := client.NewInput(prefix+".message", text)
	if err != nil {
		return client.StartResult{}, err
	}
	request, err := client.NewStartRequest(operation, definition, input)
	if err != nil {
		return client.StartResult{}, err
	}
	return session.Start(ctx, request)
}

func (clientValue *EngineClient) consumeRun(
	ctx context.Context,
	session client.Session,
	run client.RunRef,
) (client.EventKind, string, error) {
	cursor, err := client.NewCursor(run, 0)
	if err != nil {
		return client.EventRunFailed, "", err
	}
	options, err := client.NewEventStreamOptions(128, true, session.Connection().Limits())
	if err != nil {
		return client.EventRunFailed, "", err
	}
	stream, err := session.Events(ctx, cursor, options)
	if err != nil {
		return client.EventRunFailed, "", err
	}
	defer stream.Close()
	var text strings.Builder
	for {
		frame, nextErr := stream.Next(ctx)
		if nextErr != nil {
			return client.EventRunFailed, "", nextErr
		}
		event, ok := frame.Event()
		if !ok {
			continue
		}
		if event.Kind() == client.EventModelDelta {
			value, _ := event.Detail().Text()
			text.WriteString(value)
		}
		switch event.Kind() {
		case client.EventRunCompleted, client.EventRunFailed, client.EventRunCancelled:
			return event.Kind(), text.String(), nil
		}
	}
}
