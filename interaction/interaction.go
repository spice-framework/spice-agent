// Package interaction defines UI-independent request and response values.
package interaction

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ID identifies one interaction lifecycle.
type ID string

// Request asks a client for typed user input.
type Request struct {
	ID     ID
	Kind   string
	Prompt string
	Schema json.RawMessage
}

// Validate rejects malformed or unbounded requests.
func (request Request) Validate() error {
	if err := token("interaction ID", string(request.ID)); err != nil {
		return err
	}
	if err := token("interaction kind", request.Kind); err != nil {
		return err
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return errors.New("interaction prompt must not be empty")
	}
	if len(request.Schema) == 0 || !json.Valid(request.Schema) {
		return errors.New("interaction schema must be valid JSON")
	}
	if len(request.Schema) > 1<<20 {
		return errors.New("interaction schema exceeds 1048576 bytes")
	}
	return nil
}

// Response completes one interaction with structured user input.
type Response struct {
	ID    ID
	Value json.RawMessage
}

// Validate rejects a response with no valid JSON value.
func (response Response) Validate() error {
	if err := token("interaction ID", string(response.ID)); err != nil {
		return err
	}
	if len(response.Value) == 0 || !json.Valid(response.Value) {
		return errors.New("interaction response must contain valid JSON")
	}
	if len(response.Value) > 1<<20 {
		return errors.New("interaction response exceeds 1048576 bytes")
	}
	return nil
}

func token(label, value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must be non-empty without surrounding whitespace", label)
	}
	if len(value) > 128 {
		return fmt.Errorf("%s exceeds 128 bytes", label)
	}
	return nil
}
