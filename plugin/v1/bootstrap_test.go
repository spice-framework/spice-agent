package pluginv1_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
)

func TestBootstrapRoundTripIsDeterministicAndOwned(t *testing.T) {
	t.Parallel()
	secret := bytes.Repeat([]byte{0x5a}, pluginv1.HandshakeSecretBytes)
	const address = `\\.\pipe\spice-agent-plugin`
	const expected = `{"address":"\\\\.\\pipe\\spice-agent-plugin","secret":"WlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlo"}` + "\n"

	encoded, err := pluginv1.EncodeBootstrap(address, secret)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != expected {
		t.Fatalf("bootstrap = %q, want %q", encoded, expected)
	}
	again, err := pluginv1.EncodeBootstrap(address, secret)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, again) {
		t.Fatalf("bootstrap changed between encodes: %q != %q", encoded, again)
	}
	if !bytes.Equal(secret, bytes.Repeat([]byte{0x5a}, pluginv1.HandshakeSecretBytes)) {
		t.Fatal("encoding mutated the caller-owned secret")
	}

	decodedAddress, decodedSecret, err := pluginv1.DecodeBootstrap(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(decodedSecret)
	if decodedAddress != address || !bytes.Equal(decodedSecret, secret) {
		t.Fatalf("decoded bootstrap address or secret does not match")
	}
	decodedSecret[0]++
	if decodedSecret[0] == secret[0] || secret[0] != 0x5a {
		t.Fatal("decoded secret does not have independent caller ownership")
	}
}

func TestDecodeBootstrapAcceptsFieldOrderAndJSONEscapes(t *testing.T) {
	t.Parallel()
	input := ` {"secret":"AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE","address":"/tmp/spice-\u03c0.sock"} ` + "\n"
	address, secret, err := pluginv1.DecodeBootstrap(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(secret)
	if address != "/tmp/spice-π.sock" || !bytes.Equal(secret, bytes.Repeat([]byte{1}, pluginv1.HandshakeSecretBytes)) {
		t.Fatal("decoded reordered bootstrap does not match")
	}
}

func TestEncodeBootstrapValidatesAddressSecretAndBound(t *testing.T) {
	t.Parallel()
	const marker = "PRIVATE-MARKER-DO-NOT-REFLECT"
	secret := bytes.Repeat([]byte{1}, pluginv1.HandshakeSecretBytes)
	invalidUTF8 := string([]byte{'/', 0xff})
	for name, test := range map[string]struct {
		address string
		secret  []byte
	}{
		"empty address":    {secret: secret},
		"whitespace":       {address: " /tmp/socket", secret: secret},
		"NUL":              {address: "/tmp/" + marker + "\x00suffix", secret: secret},
		"control":          {address: "/tmp/socket\nsuffix", secret: secret},
		"invalid UTF-8":    {address: invalidUTF8, secret: secret},
		"missing secret":   {address: "/tmp/socket"},
		"short secret":     {address: "/tmp/socket", secret: []byte(marker)},
		"long secret":      {address: "/tmp/socket", secret: append(secret, 1)},
		"oversized record": {address: strings.Repeat("x", pluginv1.BootstrapMaximumBytes), secret: secret},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if encoded, err := pluginv1.EncodeBootstrap(test.address, test.secret); err == nil || encoded != nil {
				t.Fatalf("EncodeBootstrap() = %q, %v; want nil error result", encoded, err)
			} else if strings.Contains(err.Error(), marker) {
				t.Fatalf("error reflected bootstrap content: %q", err)
			}
		})
	}
}

func TestBootstrapEncodingHonorsExactByteBoundary(t *testing.T) {
	t.Parallel()
	secret := bytes.Repeat([]byte{1}, pluginv1.HandshakeSecretBytes)
	base, err := pluginv1.EncodeBootstrap("x", secret)
	if err != nil {
		t.Fatal(err)
	}
	address := strings.Repeat("x", pluginv1.BootstrapMaximumBytes-len(base)+1)
	encoded, err := pluginv1.EncodeBootstrap(address, secret)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != pluginv1.BootstrapMaximumBytes {
		t.Fatalf("boundary bootstrap size = %d, want %d", len(encoded), pluginv1.BootstrapMaximumBytes)
	}
	if encoded, err = pluginv1.EncodeBootstrap(address+"x", secret); err == nil || encoded != nil {
		t.Fatalf("oversized boundary bootstrap succeeded: %q", encoded)
	}
}

func TestDecodeBootstrapRejectsMalformedInputWithoutReflection(t *testing.T) {
	t.Parallel()
	const marker = "PRIVATE-MARKER-DO-NOT-REFLECT"
	validSecret := "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE"
	valid := `{"address":"/tmp/socket","secret":"` + validSecret + `"}`
	invalidUTF8 := append([]byte(valid[:len(valid)-2]), 0xff, '}', '\n')
	for name, input := range map[string][]byte{
		"empty":           nil,
		"missing newline": []byte(valid),
		"multiple lines":  []byte(valid + "\n\n"),
		"unknown":         []byte(`{"address":"/tmp/socket","secret":"` + validSecret + `","other":true}` + "\n"),
		"duplicate":       []byte(`{"address":"/tmp/socket","address":"/tmp/other","secret":"` + validSecret + `"}` + "\n"),
		"missing field":   []byte(`{"address":"/tmp/socket"}` + "\n"),
		"trailing value":  []byte(valid + ` {}` + "\n"),
		"after line":      []byte(valid + "\n{}"),
		"malformed":       []byte(`{"address":"` + marker + `","secret":` + "\n"),
		"invalid UTF-8":   invalidUTF8,
		"invalid base64":  []byte(`{"address":"/tmp/socket","secret":"` + marker + `"}` + "\n"),
		"padded base64":   []byte(`{"address":"/tmp/socket","secret":"` + validSecret + `="}` + "\n"),
		"short secret":    []byte(`{"address":"/tmp/socket","secret":"AQ"}` + "\n"),
		"empty address":   []byte(`{"address":"","secret":"` + validSecret + `"}` + "\n"),
		"NUL address":     []byte(`{"address":"/tmp/` + marker + `\u0000","secret":"` + validSecret + `"}` + "\n"),
		"control address": []byte(`{"address":"/tmp/` + marker + `\n","secret":"` + validSecret + `"}` + "\n"),
		"oversized":       bytes.Repeat([]byte{'x'}, pluginv1.BootstrapMaximumBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			address, secret, err := pluginv1.DecodeBootstrap(bytes.NewReader(input))
			clear(secret)
			if err == nil || address != "" || secret != nil {
				t.Fatalf("DecodeBootstrap() = %q, %x, %v; want empty result and error", address, secret, err)
			}
			if strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), validSecret) || strings.Contains(err.Error(), "/tmp/") {
				t.Fatalf("error reflected bootstrap content: %q", err)
			}
		})
	}
	if _, secret, err := pluginv1.DecodeBootstrap(nil); err == nil || secret != nil {
		clear(secret)
		t.Fatal("nil bootstrap input succeeded")
	}
}

func TestReadinessContractWritesExactlyOneRecord(t *testing.T) {
	t.Parallel()
	if pluginv1.ReadinessRecord != "{\"ready\":true}\n" {
		t.Fatalf("readiness record = %q", pluginv1.ReadinessRecord)
	}
	var output bytes.Buffer
	if err := pluginv1.WriteReadiness(&output); err != nil {
		t.Fatal(err)
	}
	if output.String() != pluginv1.ReadinessRecord {
		t.Fatalf("readiness output = %q", output.String())
	}
	flushed := &flushWriter{}
	if err := pluginv1.WriteReadiness(flushed); err != nil {
		t.Fatal(err)
	}
	if !flushed.flushed || flushed.String() != pluginv1.ReadinessRecord {
		t.Fatalf("buffered readiness output = %q, flushed = %t", flushed.String(), flushed.flushed)
	}
	if err := pluginv1.WriteReadiness(nil); err == nil {
		t.Fatal("nil readiness output succeeded")
	}
	if err := pluginv1.WriteReadiness(failingWriter{}); err == nil {
		t.Fatal("failed readiness write succeeded")
	} else if strings.Contains(err.Error(), "PRIVATE-MARKER-DO-NOT-REFLECT") {
		t.Fatalf("readiness error reflected writer content: %q", err)
	}
	if err := pluginv1.WriteReadiness(shortWriter{}); err == nil {
		t.Fatal("short readiness write succeeded")
	}
	if err := pluginv1.WriteReadiness(&failingFlushWriter{}); err == nil {
		t.Fatal("failed readiness flush succeeded")
	} else if strings.Contains(err.Error(), "PRIVATE-MARKER-DO-NOT-REFLECT") {
		t.Fatalf("readiness error reflected flusher content: %q", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("PRIVATE-MARKER-DO-NOT-REFLECT")
}

type shortWriter struct{}

func (shortWriter) Write(value []byte) (int, error) {
	return len(value) - 1, nil
}

type flushWriter struct {
	bytes.Buffer
	flushed bool
}

func (writer *flushWriter) Flush() error {
	writer.flushed = true
	return nil
}

type failingFlushWriter struct {
	bytes.Buffer
}

func (failingFlushWriter) Flush() error {
	return errors.New("PRIVATE-MARKER-DO-NOT-REFLECT")
}

func TestDecodeBootstrapReadErrorDoesNotReflectReaderError(t *testing.T) {
	t.Parallel()
	const marker = "PRIVATE-MARKER-DO-NOT-REFLECT"
	_, secret, err := pluginv1.DecodeBootstrap(failingReader{})
	clear(secret)
	if err == nil || strings.Contains(err.Error(), marker) {
		t.Fatalf("read error = %v", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("PRIVATE-MARKER-DO-NOT-REFLECT")
}

func FuzzBootstrap(fuzz *testing.F) {
	secret := bytes.Repeat([]byte{1}, pluginv1.HandshakeSecretBytes)
	encoded, err := pluginv1.EncodeBootstrap("/tmp/spice.sock", secret)
	if err != nil {
		fuzz.Fatal(err)
	}
	fuzz.Add(encoded)
	fuzz.Add([]byte{})
	fuzz.Add(bytes.Repeat([]byte{'x'}, pluginv1.BootstrapMaximumBytes+1))
	fuzz.Fuzz(func(t *testing.T, input []byte) {
		_, decoded, _ := pluginv1.DecodeBootstrap(bytes.NewReader(input))
		clear(decoded)
	})
}

var _ io.Writer = failingWriter{}
