package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/client"
	"github.com/spice-framework/spice-agent/daemon"
	"github.com/spice-framework/spice-agent/daemon/endpoint"
	"github.com/spice-framework/spice-agent/daemon/grpcserver"
	"github.com/spice-framework/spice-agent/daemon/localipc"
	"github.com/spice-framework/spice-agent/stage"
	"google.golang.org/grpc"
)

type EngineServer struct {
	config   ProcessConfig
	commands io.Reader
	output   io.Writer
}

func NewEngineServer(config ProcessConfig, commands io.Reader, output io.Writer) (*EngineServer, error) {
	if config.Authorization == "" || config.AuthorityDirectory == "" || commands == nil || output == nil {
		return nil, errors.New("engine server configuration is incomplete")
	}
	return &EngineServer{config: config, commands: commands, output: output}, nil
}

func (server *EngineServer) Run() error {
	token, err := endpoint.ParseAuthorizationValue(server.config.Authorization)
	if err != nil {
		return errors.New("engine authorization is invalid")
	}
	root, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()
	host, sessions, err := server.newRunHost(root)
	if err != nil {
		return fmt.Errorf("construct engine run host: %w", err)
	}
	build, err := client.NewBuild("released-generation-server", "cross-generation", "public-module", runtime.Version())
	if err != nil {
		return err
	}
	grpcServer, err := grpcserver.NewServer(grpcserver.ServerConfig{
		Root: root, EndpointToken: token, Host: host, Sessions: sessions,
		Build: build, Capabilities: []string{"events"}, MaximumSessions: 4,
	})
	if err != nil {
		return err
	}
	listener, err := localipc.Listen(server.config.Address)
	if err != nil {
		return err
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- grpcServer.Serve(listener) }()
	if err = json.NewEncoder(server.output).Encode(struct {
		Ready bool `json:"ready"`
	}{Ready: true}); err != nil {
		return err
	}
	command, err := io.ReadAll(server.commands)
	if err != nil || strings.TrimSpace(string(command)) != "STOP" {
		return errors.New("engine stop command is invalid")
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverErr := grpcServer.Shutdown(shutdownContext)
	listenerErr := listener.Close()
	serveErr := <-serveDone
	hostErr := host.Shutdown(shutdownContext)
	if serverErr != nil {
		return serverErr
	}
	if listenerErr != nil {
		return listenerErr
	}
	if hostErr != nil {
		return hostErr
	}
	if serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
		return serveErr
	}
	return nil
}

func (server *EngineServer) newRunHost(root context.Context) (*daemon.RunHost, *daemon.SessionStore, error) {
	limits, err := client.NewLimits(2<<20, 256, 128, 2<<20, 8, 8)
	if err != nil {
		return nil, nil, err
	}
	sessions, err := daemon.NewSessionStore(root, 4)
	if err != nil {
		return nil, nil, err
	}
	dispatcher, err := stage.NewDispatcher(nil)
	if err != nil {
		return nil, nil, err
	}
	engine, err := agent.NewEngine(EchoProvider{}, dispatcher, &agent.AtomicIDSource{}, time.Now, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	agentDefinition, err := agent.NewDefinition("worker", "scripted", 1)
	if err != nil {
		return nil, nil, err
	}
	definition, err := daemon.NewDefinition("worker", "revision-1", agentDefinition)
	if err != nil {
		return nil, nil, err
	}
	definitions, err := daemon.NewDefinitionSet([]daemon.Definition{definition})
	if err != nil {
		return nil, nil, err
	}
	authority, err := daemon.NewRunAuthority(daemon.RunAuthorityConfig{Directory: server.config.AuthorityDirectory})
	if err != nil {
		return nil, nil, err
	}
	ledger, err := daemon.NewLedger(4, 64)
	if err != nil {
		return nil, nil, err
	}
	pending, err := daemon.NewPendingHub(daemon.DefaultPendingLimits())
	if err != nil {
		return nil, nil, err
	}
	host, err := daemon.NewRunHost(daemon.RunHostConfig{
		Root: root, Engine: engine, Authority: authority, Sessions: sessions,
		Ledger: ledger, Pending: pending, Definitions: definitions, Limits: limits,
		TerminalRuns: 16, TerminalBytes: 2 << 20,
	})
	return host, sessions, err
}
