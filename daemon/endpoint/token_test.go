package endpoint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

func TestTokenGenerationParsingComparisonAndRedaction(t *testing.T) {
	t.Parallel()
	if reflect.TypeFor[Token]().Comparable() {
		t.Fatal("endpoint credentials must not support accidental == comparison")
	}
	raw := make([]byte, TokenBytes)
	for index := range raw {
		raw[index] = byte(index + 1) // #nosec G115 -- fixture index is bounded to 32.
	}
	token, err := generateToken(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := token.AuthorizationValue()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseAuthorizationValue(authorization)
	if err != nil || !parsed.Equal(token) || !token.Equal(parsed) {
		t.Fatalf("parsed token equality = %v, %v", parsed, err)
	}
	if token.Equal(Token{}) || (Token{}).Equal(token) {
		t.Fatal("invalid token compared equal")
	}
	secret := strings.TrimPrefix(authorization, BearerPrefix)
	for _, format := range []string{
		"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%o", "%b", "%c", "%U", "%p", "%T", "%20.8v",
	} {
		formatted := fmt.Sprintf(format, token)
		if strings.Contains(formatted, secret) || strings.Contains(formatted, "1 2 3 4 5 6 7 8") {
			t.Fatalf("token format %q exposed credential data: %q", format, formatted)
		}
	}
	var logOutput bytes.Buffer
	slog.New(slog.NewJSONHandler(&logOutput, nil)).Info("credential canary", "token", token)
	encodedJSON, marshalErr := json.Marshal(token)
	if marshalErr != nil || strings.Contains(logOutput.String(), secret) || strings.Contains(string(encodedJSON), secret) ||
		string(encodedJSON) != `"[REDACTED endpoint token]"` {
		t.Fatal("structured formatting exposed the endpoint token")
	}
}

func TestTokenRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	valid, err := generateToken(bytes.NewReader(bytes.Repeat([]byte{1}, TokenBytes)))
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := valid.AuthorizationValue()
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.TrimPrefix(authorization, BearerPrefix)
	for name, value := range map[string]string{
		"empty": "", "padded": encoded + "=", "space": " " + encoded,
		"zero": strings.Repeat("A", len(encoded)), "syntax": strings.Repeat("!", len(encoded)),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, parseErr := ParseToken(value); parseErr == nil {
				t.Fatal("invalid token was accepted")
			}
		})
	}
	for _, value := range []string{"", "Basic " + encoded, "bearer " + encoded, BearerPrefix + encoded + "="} {
		if _, parseErr := ParseAuthorizationValue(value); parseErr == nil {
			t.Fatalf("invalid authorization %q was accepted", value)
		}
	}
	if _, err = generateToken(nil); err == nil {
		t.Fatal("nil randomness was accepted")
	}
	if _, err = generateToken(bytes.NewReader(nil)); err == nil {
		t.Fatal("short randomness was accepted")
	}
	const entropySecret = "entropy-error-secret-canary"
	if _, err = generateToken(iotest.ErrReader(errors.New(entropySecret))); err == nil ||
		strings.Contains(err.Error(), entropySecret) {
		t.Fatalf("entropy failure was not safely redacted: %v", err)
	}
	if _, err = generateToken(bytes.NewReader(make([]byte, TokenBytes*tokenAttempts))); err == nil {
		t.Fatal("all-zero randomness was accepted")
	}
	if err = (Token{}).Validate(); err == nil {
		t.Fatal("zero token validated")
	}
	if _, err = (Token{}).AuthorizationValue(); err == nil {
		t.Fatal("zero token produced authorization metadata")
	}
}

func TestGenerateTokenUsesSystemRandomness(t *testing.T) {
	t.Parallel()
	first, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if first.Equal(second) {
		t.Fatal("independent generated tokens matched")
	}
}
