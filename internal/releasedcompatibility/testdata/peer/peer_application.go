package main

import (
	"bufio"
	"errors"
	"io"
)

type PeerApplication struct {
	mode   string
	input  io.Reader
	output io.Writer
}

func NewPeerApplication(arguments []string, input io.Reader, output io.Writer) (*PeerApplication, error) {
	if len(arguments) != 2 || input == nil || output == nil {
		return nil, errors.New("one peer mode and process streams are required")
	}
	switch arguments[1] {
	case "engine-server", "engine-client", "plugin-client":
	default:
		return nil, errors.New("peer mode is unsupported")
	}
	return &PeerApplication{mode: arguments[1], input: input, output: output}, nil
}

func (application *PeerApplication) Run() error {
	reader := bufio.NewReader(application.input)
	config, err := ParseProcessConfig(reader)
	if err != nil {
		return err
	}
	switch application.mode {
	case "engine-server":
		server, constructErr := NewEngineServer(config, reader, application.output)
		if constructErr != nil {
			return constructErr
		}
		return server.Run()
	case "engine-client":
		clientValue, constructErr := NewEngineClient(config, application.output)
		if constructErr != nil {
			return constructErr
		}
		return clientValue.Run()
	case "plugin-client":
		clientValue, constructErr := NewPluginClient(config, application.output)
		if constructErr != nil {
			return constructErr
		}
		return clientValue.Run()
	default:
		return errors.New("peer mode is unsupported")
	}
}
