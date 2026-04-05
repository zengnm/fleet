package fleetd

import (
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
	mode           string
	hs256Secret    []byte
	rs256PublicKey *rsa.PublicKey
	issuer         string
	audience       string
}

func NewAuthenticator(cfg Config) (*Authenticator, error) {
	authenticator := &Authenticator{
		mode:        cfg.AuthMode,
		hs256Secret: []byte(cfg.JWTHS256Secret),
		issuer:      cfg.JWTIssuer,
		audience:    cfg.JWTAudience,
	}
	switch authenticator.mode {
	case "", "disabled":
		return authenticator, nil
	case "hs256":
		if len(authenticator.hs256Secret) == 0 {
			return nil, errors.New("FLEETD_JWT_HS256_SECRET is required when FLEETD_AUTH_MODE=hs256")
		}
		return authenticator, nil
	case "rs256":
		publicKey, err := parseFleetRS256PublicKey(cfg.JWTRS256PublicKey)
		if err != nil {
			return nil, err
		}
		authenticator.rs256PublicKey = publicKey
		return authenticator, nil
	default:
		return nil, fmt.Errorf("unsupported auth mode %q", authenticator.mode)
	}
}

func (a *Authenticator) Mode() string {
	return a.mode
}

func (a *Authenticator) Authenticate(r *http.Request) (*Principal, error) {
	switch a.mode {
	case "", "disabled":
		return &Principal{Subject: "anonymous", Raw: jwt.MapClaims{}}, nil
	case "hs256":
		return a.authenticateToken(r, []string{"HS256"}, func(_ *jwt.Token) (any, error) { return a.hs256Secret, nil })
	case "rs256":
		return a.authenticateToken(r, []string{"RS256"}, func(_ *jwt.Token) (any, error) { return a.rs256PublicKey, nil })
	default:
		return nil, errors.New("unsupported auth mode")
	}
}

func (a *Authenticator) authenticateToken(r *http.Request, methods []string, keyFunc jwt.Keyfunc) (*Principal, error) {
	tokenString := fleetBearerToken(r)
	if tokenString == "" {
		return nil, errors.New("missing authorization header")
	}
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(jwt.WithValidMethods(methods))
	_, err := parser.ParseWithClaims(tokenString, claims, keyFunc)
	if err != nil {
		return nil, err
	}
	if a.issuer != "" && claims["iss"] != a.issuer {
		return nil, errors.New("invalid issuer")
	}
	if a.audience != "" && !fleetMatchesAudience(claims["aud"], a.audience) {
		return nil, errors.New("invalid audience")
	}
	return &Principal{
		Subject: fleetAsString(claims["sub"], "unknown"),
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
		return nil, errors.New("FLEETD_JWT_RS256_PUBLIC_KEY is required when FLEETD_AUTH_MODE=rs256")
	}
	normalized := strings.ReplaceAll(raw, `\n`, "\n")
	block, _ := pem.Decode([]byte(normalized))
	if block == nil {
		return nil, errors.New("invalid RS256 public key PEM")
	}
	if publicKey, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		rsaKey, ok := publicKey.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("RS256 public key must be RSA")
		}
		return rsaKey, nil
	}
	rsaKey, err := x509.ParsePKCS1PublicKey(block.Bytes)
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

func fleetMatchesAudience(value any, audience string) bool {
	switch typed := value.(type) {
	case string:
		return typed == audience
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && text == audience {
				return true
			}
		}
	}
	return false
}
