package pluginfixture

import (
	"bytes"
	"context"
	"encoding/base64"
	"runtime"
	"slices"
	"strings"
	"testing"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
)

func TestDecodeBootstrapRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, pluginv1.HandshakeSecretBytes))
	for name, input := range map[string]string{
		"empty":    "",
		"unknown":  `{"address":"value","secret":"` + secret + `","other":true}`,
		"trailing": `{"address":"value","secret":"` + secret + `"} {}`,
		"secret":   `{"address":"value","secret":"short"}`,
		"address":  `{"address":"","secret":"` + secret + `"}`,
		"large":    `{"address":"` + strings.Repeat("x", maximumBootstrapBytes) + `","secret":"` + secret + `"}`,
	} {
		if _, value, err := decodeBootstrap(strings.NewReader(input)); err == nil {
			clear(value)
			t.Errorf("invalid %s bootstrap succeeded", name)
		}
	}
}

func TestFixtureConstructorsRejectMissingOwnership(t *testing.T) {
	t.Parallel()
	if _, err := NewService([]byte("short"), func() {}); err == nil {
		t.Fatal("short fixture secret succeeded")
	}
	if _, err := NewService(bytes.Repeat([]byte{1}, pluginv1.HandshakeSecretBytes), nil); err == nil {
		t.Fatal("nil fixture shutdown callback succeeded")
	}
	if err := Serve(nil, &bytes.Buffer{}); err == nil {
		t.Fatal("nil fixture input succeeded")
	}
	if err := Serve(&bytes.Buffer{}, nil); err == nil {
		t.Fatal("nil fixture output succeeded")
	}
}

func TestInitializeReturnsBoundedFailureAndAuthenticatedConflict(t *testing.T) {
	t.Parallel()
	secret := bytes.Repeat([]byte{1}, pluginv1.HandshakeSecretBytes)
	server, err := NewService(secret, func() {})
	if err != nil {
		t.Fatal(err)
	}
	request := validFixtureInitializeRequest()
	invalid := cloneFixtureRequest(request)
	invalid.Host = nil
	response, err := server.Initialize(context.Background(), invalid)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT ||
		len(response.GetHandshakeProof()) != pluginv1.HandshakeProofBytes {
		t.Fatalf("invalid initialization response = %#v", response)
	}

	response, err = server.Initialize(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err = pluginv1.ValidateInitializeResponseForRequest(request, response, secret); err != nil {
		t.Fatal(err)
	}
	conflict, err := server.Initialize(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err = pluginv1.ValidateInitializeResponseForRequest(request, conflict, secret); err == nil {
		t.Fatal("duplicate initialization unexpectedly succeeded")
	}
	if _, err = server.Drain(context.Background(), &pluginv1.DrainRequest{SessionId: []byte("wrong")}); err == nil {
		t.Fatal("wrong drain session succeeded")
	}
	if _, err = server.Shutdown(context.Background(), &pluginv1.ShutdownRequest{SessionId: response.GetSessionId()}); err == nil {
		t.Fatal("shutdown before drain succeeded")
	}
}

func FuzzBootstrap(fuzz *testing.F) {
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, pluginv1.HandshakeSecretBytes))
	fuzz.Add([]byte(`{"address":"fixture","secret":"` + secret + `"}`))
	fuzz.Add([]byte{})
	fuzz.Add(bytes.Repeat([]byte{'x'}, maximumBootstrapBytes+1))
	fuzz.Fuzz(func(t *testing.T, input []byte) {
		_, value, _ := decodeBootstrap(bytes.NewReader(input))
		clear(value)
	})
}

func validFixtureInitializeRequest() *pluginv1.InitializeRequest {
	return &pluginv1.InitializeRequest{
		Protocol: pluginv1.SupportedProtocolRange(),
		Host: &pluginv1.BuildIdentity{
			Component: "fixture-test", Version: "v1", Commit: "test", Runtime: runtime.Version(),
		},
		SupportedCapabilities: &commonv1.CapabilitySet{Names: []string{pluginv1.CapabilityRuntimeToolsV1}},
		RequiredCapabilities:  &commonv1.CapabilitySet{Names: []string{pluginv1.CapabilityRuntimeToolsV1}},
		RequestedLimits:       fixtureLimits(),
		LaunchId:              bytes.Repeat([]byte{2}, pluginv1.LaunchIDBytes),
		HandshakeChallenge:    bytes.Repeat([]byte{3}, pluginv1.HandshakeChallengeBytes),
	}
}

func cloneFixtureRequest(value *pluginv1.InitializeRequest) *pluginv1.InitializeRequest {
	return &pluginv1.InitializeRequest{
		Protocol:              pluginv1.SupportedProtocolRange(),
		Host:                  value.GetHost(),
		SupportedCapabilities: &commonv1.CapabilitySet{Names: slices.Clone(value.GetSupportedCapabilities().GetNames())},
		RequiredCapabilities:  &commonv1.CapabilitySet{Names: slices.Clone(value.GetRequiredCapabilities().GetNames())},
		RequestedLimits:       fixtureLimits(),
		LaunchId:              slices.Clone(value.GetLaunchId()),
		HandshakeChallenge:    slices.Clone(value.GetHandshakeChallenge()),
	}
}
