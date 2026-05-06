package fleetnode

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Identity struct {
	Version       int      `json:"version"`
	DeviceID      string   `json:"deviceId"`
	PublicKeyPEM  string   `json:"publicKeyPem"`
	PrivateKeyPEM string   `json:"privateKeyPem"`
	CreatedAtMs   int64    `json:"createdAtMs"`
	DeviceToken   string   `json:"deviceToken,omitempty"`
	TokenScopes   []string `json:"tokenScopes,omitempty"`
}

func LoadOrCreateIdentity(path string) (*Identity, error) {
	if raw, err := os.ReadFile(path); err == nil {
		var identity Identity
		if err := json.Unmarshal(raw, &identity); err != nil {
			return nil, err
		}
		if err := normalizeIdentity(&identity); err != nil {
			return nil, err
		}
		if err := WriteIdentityFile(path, &identity); err != nil {
			return &identity, nil
		}
		return &identity, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	identity, err := GenerateIdentity()
	if err != nil {
		return nil, err
	}
	if err := WriteIdentityFile(path, identity); err != nil {
		return nil, err
	}
	return identity, nil
}

func GenerateIdentity() (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	return &Identity{
		Version:       1,
		DeviceID:      deriveDeviceID(pub),
		PublicKeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})),
		PrivateKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})),
		CreatedAtMs:   time.Now().UnixMilli(),
	}, nil
}

func WriteIdentityFile(path string, identity *Identity) error {
	if err := normalizeIdentity(identity); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(payload, '\n'), 0o600)
}

func normalizeIdentity(identity *Identity) error {
	if identity == nil {
		return errors.New("identity is required")
	}
	identity.Version = 1
	publicKey, err := parsePublicKeyPEM(identity.PublicKeyPEM)
	if err != nil {
		return err
	}
	if _, err := parsePrivateKeyPEM(identity.PrivateKeyPEM); err != nil {
		return err
	}
	identity.DeviceID = deriveDeviceID(publicKey)
	return nil
}

func (i *Identity) Sign(payload string) (signature string, publicKey string, err error) {
	privateKey, err := parsePrivateKeyPEM(i.PrivateKeyPEM)
	if err != nil {
		return "", "", err
	}
	pub, err := parsePublicKeyPEM(i.PublicKeyPEM)
	if err != nil {
		return "", "", err
	}
	signatureRaw := ed25519.Sign(privateKey, []byte(payload))
	return base64.RawURLEncoding.EncodeToString(signatureRaw), base64.RawURLEncoding.EncodeToString(pub), nil
}

func parsePublicKeyPEM(value string) (ed25519.PublicKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, errors.New("decode public key pem")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("public key is not ed25519")
	}
	return key, nil
}

func parsePrivateKeyPEM(value string) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, errors.New("decode private key pem")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not ed25519")
	}
	return key, nil
}

func deriveDeviceID(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:])
}

func buildDeviceAuthPayloadV3(params deviceAuthPayloadParams) string {
	return strings.Join([]string{
		"v3",
		params.DeviceID,
		params.ClientID,
		params.ClientMode,
		params.Role,
		strings.Join(params.Scopes, ","),
		strconv.FormatInt(params.SignedAtMs, 10),
		params.Token,
		params.Nonce,
		normalizeDeviceMetadataForAuth(params.Platform),
		normalizeDeviceMetadataForAuth(params.DeviceFamily),
	}, "|")
}

type deviceAuthPayloadParams struct {
	DeviceID     string
	ClientID     string
	ClientMode   string
	Role         string
	Scopes       []string
	SignedAtMs   int64
	Token        string
	Nonce        string
	Platform     string
	DeviceFamily string
}

func normalizeDeviceMetadataForAuth(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ToLower(value)
}
