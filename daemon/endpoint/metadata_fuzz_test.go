package endpoint

import (
	"bytes"
	"testing"
)

func FuzzDecodeMetadata(f *testing.F) {
	seed := metadataFixture(f, TransportUnixSocket, "/tmp/spice-agent-fuzz.sock")
	encoded, err := encodeMetadata(seed)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{"schema":"spice.agent.local-endpoint/v1"}`))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, value []byte) {
		metadata, decodeErr := decodeMetadata(value)
		if decodeErr != nil {
			return
		}
		if err := metadata.Validate(); err != nil {
			t.Fatalf("decoded invalid endpoint metadata: %v", err)
		}
		canonical, encodeErr := encodeMetadata(metadata)
		if encodeErr != nil || !bytes.Equal(canonical, value) {
			t.Fatalf("accepted noncanonical endpoint metadata: %v", encodeErr)
		}
	})
}
