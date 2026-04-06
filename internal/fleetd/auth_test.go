package fleetd

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseFleetRS256PublicKeyAcceptsCommonFormats(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	pemText := string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyDER,
	}))
	escaped := strings.ReplaceAll(strings.TrimSpace(pemText), "\n", `\n`)
	collapsed := strings.Join(strings.Fields(strings.TrimSpace(pemText)), " ")
	base64Only := base64.StdEncoding.EncodeToString(publicKeyDER)

	testCases := []struct {
		name string
		raw  string
	}{
		{name: "multiline pem", raw: pemText},
		{name: "escaped newlines", raw: escaped},
		{name: "collapsed whitespace", raw: collapsed},
		{name: "base64 der", raw: base64Only},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, err := parseFleetRS256PublicKey(testCase.raw)
			if err != nil {
				t.Fatalf("parseFleetRS256PublicKey(%s): %v", testCase.name, err)
			}
			if parsed.N.Cmp(privateKey.PublicKey.N) != 0 || parsed.E != privateKey.PublicKey.E {
				t.Fatalf("parsed key does not match original public key")
			}
		})
	}
}

func TestParseFleetRS256PublicKeyRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	if _, err := parseFleetRS256PublicKey("not-a-public-key"); err == nil {
		t.Fatalf("expected invalid public key to be rejected")
	}
}

func TestRuntimeAuthenticatorAcceptsProxySafeHeaders(t *testing.T) {
	t.Parallel()

	authenticator, err := NewRuntimeAuthenticator(Config{
		RuntimeAuthMode: "api_key",
		APIKey:          "runtime-key",
	})
	if err != nil {
		t.Fatalf("NewRuntimeAuthenticator: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/runtime/fleet/nodes", nil)
	req.Header.Set("X-API-Key", "runtime-key")
	req.Header.Set("X-User-Id", "user-a")

	principal, err := authenticator.Authenticate(req)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if principal.Subject != "user-a" {
		t.Fatalf("principal subject = %q, want %q", principal.Subject, "user-a")
	}
}
