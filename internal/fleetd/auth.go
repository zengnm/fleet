package fleetd

import (
	"encoding/base64"
	"crypto/rsa"
	"crypto/subtle"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

const fleetCookieName = "fleetd_token"

type Principal struct {
	Subject string
	Raw     jwt.MapClaims
}

type Authenticator struct {
	enabled        bool
	rs256PublicKey *rsa.PublicKey
	userIDField    string
}

func NewAuthenticator(cfg Config) (*Authenticator, error) {
	authenticator := &Authenticator{
		userIDField: strings.TrimSpace(cfg.JWTUserIDField),
	}
	if authenticator.userIDField == "" {
		authenticator.userIDField = "sub"
	}
	if strings.TrimSpace(cfg.JWTRS256PublicKey) == "" {
		return authenticator, nil
	}
	publicKey, err := parseFleetRS256PublicKey(cfg.JWTRS256PublicKey)
	if err != nil {
		return nil, err
	}
	authenticator.enabled = true
	authenticator.rs256PublicKey = publicKey
	return authenticator, nil
}

func (a *Authenticator) Authenticate(r *http.Request) (*Principal, error) {
	if !a.enabled {
		return &Principal{Subject: "anonymous", Raw: jwt.MapClaims{}}, nil
	}
	return a.authenticateToken(r)
}

func (a *Authenticator) Enabled() bool {
	return a.enabled
}

func (a *Authenticator) authenticateToken(r *http.Request) (*Principal, error) {
	tokenString := fleetBearerToken(r)
	if tokenString == "" {
		return nil, errors.New("missing authorization header")
	}
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"RS256"}))
	_, err := parser.ParseWithClaims(tokenString, claims, func(_ *jwt.Token) (any, error) {
		return a.rs256PublicKey, nil
	})
	if err != nil {
		return nil, err
	}
	subject := fleetAsString(claims[a.userIDField], "")
	if subject == "" {
		return nil, fmt.Errorf("invalid user id claim %q", a.userIDField)
	}
	return &Principal{
		Subject: subject,
		Raw:     claims,
	}, nil
}

type RuntimeAuthenticator struct {
	mode   string
	apiKey string
}

func NewRuntimeAuthenticator(cfg Config) (*RuntimeAuthenticator, error) {
	authenticator := &RuntimeAuthenticator{
		mode:   cfg.RuntimeAuthMode,
		apiKey: cfg.APIKey,
	}
	switch authenticator.mode {
	case "", "disabled", "api_key":
		return authenticator, nil
	default:
		return nil, fmt.Errorf("unsupported runtime auth mode %q", authenticator.mode)
	}
}

func (a *RuntimeAuthenticator) Authenticate(r *http.Request) (*Principal, error) {
	switch a.mode {
	case "", "disabled":
		return &Principal{Subject: "anonymous", Raw: jwt.MapClaims{}}, nil
	case "api_key":
		apiKey := strings.TrimSpace(r.Header.Get("API_KEY"))
		if apiKey == "" {
			return nil, errors.New("missing API_KEY header")
		}
		if subtle.ConstantTimeCompare([]byte(apiKey), []byte(a.apiKey)) != 1 {
			return nil, errors.New("invalid API_KEY header")
		}
		userID := strings.TrimSpace(r.Header.Get("USER_ID"))
		if userID == "" {
			return nil, errors.New("missing USER_ID header")
		}
		return &Principal{Subject: userID, Raw: jwt.MapClaims{}}, nil
	default:
		return nil, errors.New("unsupported runtime auth mode")
	}
}

func fleetBearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header != "" {
		return strings.TrimSpace(strings.TrimPrefix(header, "Bearer"))
	}
	cookie, err := r.Cookie(fleetCookieName)
	if err == nil && strings.TrimSpace(cookie.Value) != "" {
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}

func parseFleetRS256PublicKey(raw string) (*rsa.PublicKey, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("FLEETD_JWT_RS256_PUBLIC_KEY is required to enable Web login")
	}
	normalized := normalizeFleetPublicKeyText(raw)
	candidates := []string{normalized}
	if repaired, ok := rebuildFleetPEMBlock(normalized); ok && repaired != normalized {
		candidates = append(candidates, repaired)
	}
	for _, candidate := range candidates {
		block, _ := pem.Decode([]byte(candidate))
		if block == nil {
			continue
		}
		return parseFleetRS256PublicKeyBlock(block.Bytes)
	}
	if der, err := decodeFleetPublicKeyDER(normalized); err == nil {
		return parseFleetRS256PublicKeyBlock(der)
	}
	return nil, errors.New("invalid RS256 public key PEM")
}

func normalizeFleetPublicKeyText(raw string) string {
	normalized := strings.TrimSpace(raw)
	normalized = strings.ReplaceAll(normalized, `\r\n`, "\n")
	normalized = strings.ReplaceAll(normalized, `\n`, "\n")
	normalized = strings.ReplaceAll(normalized, `\r`, "")
	normalized = strings.ReplaceAll(normalized, "\r\n", "\n")
	return normalized
}

func rebuildFleetPEMBlock(raw string) (string, bool) {
	for _, marker := range []struct {
		begin string
		end   string
	}{
		{begin: "-----BEGIN PUBLIC KEY-----", end: "-----END PUBLIC KEY-----"},
		{begin: "-----BEGIN RSA PUBLIC KEY-----", end: "-----END RSA PUBLIC KEY-----"},
	} {
		start := strings.Index(raw, marker.begin)
		end := strings.Index(raw, marker.end)
		if start == -1 || end == -1 || end < start {
			continue
		}
		body := raw[start+len(marker.begin) : end]
		body = strings.Join(strings.Fields(body), "")
		if body == "" {
			return "", false
		}
		return marker.begin + "\n" + wrapFleetPEMBody(body) + "\n" + marker.end, true
	}
	return "", false
}

func wrapFleetPEMBody(body string) string {
	if len(body) <= 64 {
		return body
	}
	lines := make([]string, 0, (len(body)+63)/64)
	for len(body) > 64 {
		lines = append(lines, body[:64])
		body = body[64:]
	}
	if body != "" {
		lines = append(lines, body)
	}
	return strings.Join(lines, "\n")
}

func decodeFleetPublicKeyDER(raw string) ([]byte, error) {
	sanitized := raw
	for _, marker := range []string{
		"-----BEGIN PUBLIC KEY-----",
		"-----END PUBLIC KEY-----",
		"-----BEGIN RSA PUBLIC KEY-----",
		"-----END RSA PUBLIC KEY-----",
	} {
		sanitized = strings.ReplaceAll(sanitized, marker, "")
	}
	sanitized = strings.Join(strings.Fields(sanitized), "")
	if sanitized == "" {
		return nil, errors.New("empty public key")
	}
	return base64.StdEncoding.DecodeString(sanitized)
}

func parseFleetRS256PublicKeyBlock(der []byte) (*rsa.PublicKey, error) {
	if publicKey, err := x509.ParsePKIXPublicKey(der); err == nil {
		rsaKey, ok := publicKey.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("RS256 public key must be RSA")
		}
		return rsaKey, nil
	}
	rsaKey, err := x509.ParsePKCS1PublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse RS256 public key: %w", err)
	}
	return rsaKey, nil
}

func fleetAsString(value any, fallback string) string {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return fallback
	}
	return text
}
