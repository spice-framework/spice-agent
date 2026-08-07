package pluginhost

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestPrivateInputDestroysConsumedBootstrap(t *testing.T) {
	t.Parallel()
	input := newPrivateInput([]byte("private-bootstrap"))
	buffer := make([]byte, 7)
	var received []byte
	for {
		count, err := input.Read(buffer)
		received = append(received, buffer[:count]...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if string(received) != "private-bootstrap" || !input.cleared() {
		t.Fatalf("received=%q cleared=%t", received, input.cleared())
	}
	if count, err := input.Read(buffer); count != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("post-clear Read = %d, %v", count, err)
	}
}

func TestPrivateInputExplicitClearAndFormattingAreRedacted(t *testing.T) {
	t.Parallel()
	const secret = "secret-bootstrap"
	input := newPrivateInput([]byte(secret))
	input.Clear()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{fmt.Sprint(input), fmt.Sprintf("%+v", input), string(encoded)} {
		if strings.Contains(rendered, secret) || !strings.Contains(rendered, "REDACTED") {
			t.Fatalf("unsafe rendering %q", rendered)
		}
	}
}
