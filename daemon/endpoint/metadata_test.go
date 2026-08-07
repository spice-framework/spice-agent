package endpoint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/client"
)

func TestMetadataCanonicalRoundTripAndRedaction(t *testing.T) {
	t.Parallel()
	metadata := metadataFixture(t, TransportWindowsNamedPipe, `\\.\pipe\spice-agent-test`)
	encoded, err := encodeMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeMetadata(encoded)
	if err != nil {
		t.Fatal(err)
	}
	decodedAuthorization, _ := decoded.Token().AuthorizationValue()
	originalAuthorization, _ := metadata.Token().AuthorizationValue()
	if decoded.Transport() != metadata.Transport() || decoded.Address() != metadata.Address() ||
		decoded.Server() != metadata.Server() || decoded.Protocol() != metadata.Protocol() ||
		decoded.Process().ID() != metadata.Process().ID() ||
		!decoded.Process().StartedAt().Equal(metadata.Process().StartedAt()) ||
		!bytes.Equal(decoded.Process().InstanceID(), metadata.Process().InstanceID()) ||
		decodedAuthorization != originalAuthorization {
		t.Fatalf("decoded endpoint metadata differs: %#v", decoded)
	}
	secret := strings.TrimPrefix(originalAuthorization, BearerPrefix)
	for _, formatted := range []string{fmt.Sprint(metadata), fmt.Sprintf("%#v", metadata), fmt.Sprintf("%x", metadata)} {
		if strings.Contains(formatted, secret) || !strings.Contains(formatted, "REDACTED") {
			t.Fatalf("metadata formatting exposed secret: %q", formatted)
		}
	}
	structured, err := json.Marshal(metadata)
	if err != nil || string(structured) != `"[REDACTED local endpoint metadata]"` {
		t.Fatalf("metadata JSON = %q, %v", structured, err)
	}
	var logged bytes.Buffer
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("endpoint", "metadata", metadata)
	if strings.Contains(logged.String(), secret) {
		t.Fatal("structured log exposed endpoint credential")
	}
}

func TestMetadataSupportsOnlyCanonicalLocalAddresses(t *testing.T) {
	t.Parallel()
	for name, testCase := range map[string]struct {
		transport Transport
		address   string
		valid     bool
	}{
		"unix":          {TransportUnixSocket, "/run/user/1000/spice-agent.sock", true},
		"unix relative": {TransportUnixSocket, "spice-agent.sock", false},
		"unix dirty":    {TransportUnixSocket, "/run/user/../spice-agent.sock", false},
		"pipe":          {TransportWindowsNamedPipe, `\\.\pipe\spice-agent-user`, true},
		"foreign pipe":  {TransportWindowsNamedPipe, `\\.\pipe\other`, false},
		"pipe segment":  {TransportWindowsNamedPipe, `\\.\pipe\spice-agent-a\b`, false},
		"pipe Unicode":  {TransportWindowsNamedPipe, `\\.\pipe\spice-agent-café`, false},
		"tcp":           {Transport("tcp"), "127.0.0.1:1234", false},
		"control":       {TransportUnixSocket, "/tmp/spice\nagent.sock", false},
		"Unix root":     {TransportUnixSocket, "/", false},
		"Unix Unicode":  {TransportUnixSocket, "/tmp/café.sock", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := NewMetadata(
				testCase.transport, testCase.address, tokenFixture(t), buildFixture(t),
				protocolFixture(t), processFixture(t),
			)
			if (err == nil) != testCase.valid {
				t.Fatalf("address validity = %v, want %v: %v", err == nil, testCase.valid, err)
			}
		})
	}
}

func TestWindowsPipeMetadataMatchesLocalIPCBoundary(t *testing.T) {
	t.Parallel()
	maximumSuffix := strings.Repeat("a", maximumWindowsPipeSuffixLength)
	if err := validateAddress(TransportWindowsNamedPipe, `\\.\pipe\spice-agent-`+maximumSuffix); err != nil {
		t.Fatalf("maximum compatible Windows pipe suffix: %v", err)
	}
	if err := validateAddress(TransportWindowsNamedPipe, `\\.\pipe\spice-agent-`+maximumSuffix+"a"); err == nil {
		t.Fatal("metadata accepted a Windows pipe suffix local IPC cannot consume")
	}
}

func TestMetadataDecoderRejectsNoncanonicalAndMalformedRecords(t *testing.T) {
	t.Parallel()
	metadata := metadataFixture(t, TransportUnixSocket, "/tmp/spice-agent.sock")
	canonical, err := encodeMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err = json.Unmarshal(canonical, &wire); err != nil {
		t.Fatal(err)
	}
	withUnknown := mapsClone(wire)
	withUnknown["unknown"] = true
	wrongSchema := mapsClone(wire)
	wrongSchema["schema"] = "spice.agent.local-endpoint/v2"
	zeroPID := mapsClone(wire)
	zeroPID["process_id"] = float64(0)
	zeroToken := mapsClone(wire)
	zeroToken["token"] = strings.Repeat("A", 43)
	for name, encoded := range map[string][]byte{
		"empty": nil, "oversize": bytes.Repeat([]byte{'x'}, maximumMetadataSize+1),
		"trailing": append(slicesClone(canonical), []byte("{}")...),
		"spacing":  bytes.Replace(slicesClone(canonical), []byte(`{"schema"`), []byte("{\n  \"schema\""), 1),
		"unknown":  marshalFixture(t, withUnknown), "schema": marshalFixture(t, wrongSchema),
		"pid": marshalFixture(t, zeroPID), "token": marshalFixture(t, zeroToken),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, decodeErr := decodeMetadata(encoded); decodeErr == nil {
				t.Fatal("invalid endpoint record decoded")
			}
		})
	}
}

func TestProcessRejectsInvalidIdentity(t *testing.T) {
	t.Parallel()
	validID := bytes.Repeat([]byte{1}, ProcessInstanceIDBytes)
	for name, value := range map[string]struct {
		id       uint32
		started  time.Time
		instance []byte
	}{
		"zero PID":      {0, time.Unix(1, 0).UTC(), validID},
		"zero time":     {1, time.Time{}, validID},
		"local time":    {1, time.Unix(1, 0).In(time.FixedZone("test", 60)), validID},
		"overflow time": {1, time.Date(2600, time.January, 1, 0, 0, 0, 0, time.UTC), validID},
		"short ID":      {1, time.Unix(1, 0).UTC(), []byte{1}},
		"zero ID":       {1, time.Unix(1, 0).UTC(), make([]byte, ProcessInstanceIDBytes)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewProcess(value.id, value.started, value.instance); err == nil {
				t.Fatal("invalid process identity succeeded")
			}
		})
	}
}

func TestProcessGenerationRetriesZeroAndRedactsEntropyFailure(t *testing.T) {
	t.Parallel()
	startedAt := time.Unix(1_700_000_000, 123).UTC()
	random := append(make([]byte, ProcessInstanceIDBytes), bytes.Repeat([]byte{7}, ProcessInstanceIDBytes)...)
	process, err := generateProcess(bytes.NewReader(random), 42, startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if process.ID() != 42 || !process.StartedAt().Equal(startedAt) ||
		!bytes.Equal(process.InstanceID(), bytes.Repeat([]byte{7}, ProcessInstanceIDBytes)) {
		t.Fatalf("generated process identity = %#v", process)
	}
	if _, err = generateProcess(nil, 42, startedAt); err == nil {
		t.Fatal("nil process randomness succeeded")
	}
	if _, err = generateProcess(
		bytes.NewReader(make([]byte, ProcessInstanceIDBytes*processIDAttempts)), 42, startedAt,
	); err == nil {
		t.Fatal("bounded all-zero process randomness succeeded")
	}
	const secret = "entropy-reader-secret"
	if _, err = generateProcess(failingProcessReader{errors.New(secret)}, 42, startedAt); err == nil ||
		strings.Contains(err.Error(), secret) {
		t.Fatalf("entropy failure was not redacted: %v", err)
	}
	if _, err = generateProcess(bytes.NewReader(bytes.Repeat([]byte{1}, ProcessInstanceIDBytes)), 0, startedAt); err == nil {
		t.Fatal("generated process accepted an invalid PID")
	}
	counting := &countingProcessReader{}
	if _, err = generateProcess(counting, 0, startedAt); err == nil || counting.reads != 0 {
		t.Fatalf("invalid deterministic input performed %d entropy reads: %v", counting.reads, err)
	}
}

func TestGenerateProcessUsesSystemRandomness(t *testing.T) {
	t.Parallel()
	startedAt := time.Now().UTC()
	first, err := GenerateProcess(1234, startedAt)
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateProcess(1234, startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.InstanceID(), second.InstanceID()) {
		t.Fatal("independent process lifetime identities matched")
	}
}

type failingProcessReader struct{ err error }

func (reader failingProcessReader) Read([]byte) (int, error) { return 0, reader.err }

type countingProcessReader struct{ reads int }

func (reader *countingProcessReader) Read(value []byte) (int, error) {
	reader.reads++
	for index := range value {
		value[index] = 1
	}
	return len(value), nil
}

func metadataFixture(tb testing.TB, transport Transport, address string) Metadata {
	tb.Helper()
	value, err := NewMetadata(transport, address, tokenFixture(tb), buildFixture(tb), protocolFixture(tb), processFixture(tb))
	if err != nil {
		tb.Fatal(err)
	}
	return value
}

func tokenFixture(tb testing.TB) Token {
	tb.Helper()
	value, err := generateToken(bytes.NewReader(bytes.Repeat([]byte{7}, TokenBytes)))
	if err != nil {
		tb.Fatal(err)
	}
	return value
}

func buildFixture(tb testing.TB) client.Build {
	tb.Helper()
	value, err := client.NewBuild("spice-agentd", "v0.1.0-preview.1", "abc123", "go1.26.5")
	if err != nil {
		tb.Fatal(err)
	}
	return value
}

func protocolFixture(tb testing.TB) client.ProtocolVersion {
	tb.Helper()
	value, err := client.NewProtocolVersion(1, 2, 0)
	if err != nil {
		tb.Fatal(err)
	}
	return value
}

func processFixture(tb testing.TB) Process {
	tb.Helper()
	value, err := NewProcess(1234, time.Unix(1_700_000_000, 123).UTC(), bytes.Repeat([]byte{9}, ProcessInstanceIDBytes))
	if err != nil {
		tb.Fatal(err)
	}
	return value
}

func mapsClone(source map[string]any) map[string]any {
	return maps.Clone(source)
}

func slicesClone(source []byte) []byte { return append([]byte(nil), source...) }

func marshalFixture(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return append(encoded, '\n')
}
