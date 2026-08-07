package endpoint

import (
	"strings"
	"testing"
	"time"
)

func TestZeroUserScopeFailsClosed(t *testing.T) {
	t.Parallel()

	var scope UserScope
	if err := scope.Validate(); err == nil {
		t.Fatal("zero user scope validation succeeded")
	}
	if _, err := scope.OpenStore(time.Millisecond); err == nil {
		t.Fatal("zero user scope opened a store")
	}
}

func TestUserScopeRejectsInvalidFieldsBeforeStorage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scope UserScope
		match string
	}{
		{
			name:  "relative directory",
			scope: UserScope{directory: "state", transport: platformTestTransport(), address: platformTestAddress()},
			match: "canonical absolute path",
		},
		{
			name:  "invalid address",
			scope: UserScope{directory: platformTestDirectory(), transport: platformTestTransport(), address: "remote"},
			match: "endpoint address",
		},
		{
			name:  "wrong transport",
			scope: UserScope{directory: platformTestDirectory(), transport: platformOtherTransport(), address: platformOtherAddress()},
			match: platformTransportError(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.scope.validateFields()
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("validateFields() error = %v, want containing %q", err, test.match)
			}
		})
	}
}
