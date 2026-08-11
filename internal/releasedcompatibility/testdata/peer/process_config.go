package main

import (
	"encoding/json"
	"errors"
	"io"
)

type ProcessConfig struct {
	Address            string `json:"address"`
	Authorization      string `json:"authorization,omitempty"`
	AuthorityDirectory string `json:"authority_directory,omitempty"`
	Secret             string `json:"secret,omitempty"`
}

func ParseProcessConfig(reader io.Reader) (ProcessConfig, error) {
	if reader == nil {
		return ProcessConfig{}, errors.New("process configuration reader is required")
	}
	var value ProcessConfig
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return ProcessConfig{}, errors.New("process configuration is invalid")
	}
	if value.Address == "" {
		return ProcessConfig{}, errors.New("process address is required")
	}
	return value, nil
}
