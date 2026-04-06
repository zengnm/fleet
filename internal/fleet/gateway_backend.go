package fleet

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"fleetd/internal/store"
	"fleetd/pkg/spec"
)

const (
	defaultGatewayURL            = "ws://127.0.0.1:18789"
	defaultGatewayRequestTimeout = 30 * time.Second
	gatewayProtocolVersion       = 3
)

var defaultGatewayScopes = []string{
	"operator.admin",
	"operator.read",
	"operator.write",
	"operator.approvals",
	"operator.pairing",
	"talk.secrets",
}

type GatewayBackendConfig struct {
	URL                string
	Token              string
	Password           string
	CAFile             string
	DeviceIdentityFile string
	RequestTimeout     time.Duration
	ClientID           string
	ClientDisplayName  string
	ClientVersion      string
	Platform           string
	DeviceFamily       string
	ModelIdentifier    string
}

type GatewayBackend struct {
	cfg GatewayBackendConfig

	identityMu sync.Mutex
}

type gatewayIdentity struct {
	Version       int      `json:"version"`
	DeviceID      string   `json:"deviceId"`
	PublicKeyPEM  string   `json:"publicKeyPem"`
	PrivateKeyPEM string   `json:"privateKeyPem"`
	CreatedAtMs   int64    `json:"createdAtMs"`
	DeviceToken   string   `json:"deviceToken,omitempty"`
	TokenScopes   []string `json:"tokenScopes,omitempty"`
}

type gatewayFrame struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	OK      bool            `json:"ok,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *gatewayError   `json:"error,omitempty"`
	Event   string          `json:"event,omitempty"`
}

type gatewayError struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	Retryable    bool   `json:"retryable,omitempty"`
	RetryAfterMs int64  `json:"retryAfterMs,omitempty"`
}

type gatewayHelloOk struct {
	Type string `json:"type"`
	Auth struct {
		DeviceToken string   `json:"deviceToken"`
		Scopes      []string `json:"scopes"`
	} `json:"auth"`
}

type gatewayConnectChallenge struct {
	Nonce string `json:"nonce"`
}

type gatewayConnectParams struct {
	MinProtocol int `json:"minProtocol"`
	MaxProtocol int `json:"maxProtocol"`
	Client      struct {
		ID              string `json:"id"`
		DisplayName     string `json:"displayName,omitempty"`
		Version         string `json:"version"`
		Platform        string `json:"platform"`
		DeviceFamily    string `json:"deviceFamily,omitempty"`
		ModelIdentifier string `json:"modelIdentifier,omitempty"`
		Mode            string `json:"mode"`
	} `json:"client"`
	Role   string   `json:"role,omitempty"`
	Scopes []string `json:"scopes,omitempty"`
	Device *struct {
		ID        string `json:"id"`
		PublicKey string `json:"publicKey"`
		Signature string `json:"signature"`
		SignedAt  int64  `json:"signedAt"`
		Nonce     string `json:"nonce"`
	} `json:"device,omitempty"`
	Auth *struct {
		Token       string `json:"token,omitempty"`
		DeviceToken string `json:"deviceToken,omitempty"`
		Password    string `json:"password,omitempty"`
	} `json:"auth,omitempty"`
}

type gatewayDevicePairList struct {
	Pending []gatewayPendingDevice `json:"pending"`
}

type gatewayPendingDevice struct {
	RequestID    string   `json:"requestId"`
	DeviceID     string   `json:"deviceId"`
	PublicKey    string   `json:"publicKey"`
	DisplayName  string   `json:"displayName"`
	Platform     string   `json:"platform"`
	DeviceFamily string   `json:"deviceFamily"`
	ClientID     string   `json:"clientId"`
	ClientMode   string   `json:"clientMode"`
	Role         string   `json:"role"`
	Roles        []string `json:"roles"`
	RemoteIP     string   `json:"remoteIp"`
	TS           int64    `json:"ts"`
}

type gatewayApproveDeviceResponse struct {
	RequestID string              `json:"requestId"`
	Device    gatewayPairedDevice `json:"device"`
}

type gatewayPairedDevice struct {
	DeviceID     string `json:"deviceId"`
	DisplayName  string `json:"displayName"`
	Platform     string `json:"platform"`
	DeviceFamily string `json:"deviceFamily"`
	ClientID     string `json:"clientId"`
	ClientMode   string `json:"clientMode"`
	Role         string `json:"role"`
	RemoteIP     string `json:"remoteIp"`
	ApprovedAtMs int64  `json:"approvedAtMs"`
}

type gatewayNodeListResponse struct {
	Nodes []gatewayNode `json:"nodes"`
}

type gatewayNode struct {
	NodeID          string          `json:"nodeId"`
	DisplayName     string          `json:"displayName"`
	Platform        string          `json:"platform"`
	Version         string          `json:"version"`
	CoreVersion     string          `json:"coreVersion"`
	UIVersion       string          `json:"uiVersion"`
	ClientID        string          `json:"clientId"`
	ClientMode      string          `json:"clientMode"`
	RemoteIP        string          `json:"remoteIp"`
	DeviceFamily    string          `json:"deviceFamily"`
	ModelIdentifier string          `json:"modelIdentifier"`
	PathEnv         string          `json:"pathEnv"`
	Caps            []string        `json:"caps"`
	Commands        []string        `json:"commands"`
	Permissions     map[string]bool `json:"permissions"`
	Paired          bool            `json:"paired"`
	Connected       bool            `json:"connected"`
	ConnectedAtMs   int64           `json:"connectedAtMs"`
	ApprovedAtMs    int64           `json:"approvedAtMs"`
}

type gatewayInvokePayload struct {
	OK          bool   `json:"ok"`
	NodeID      string `json:"nodeId"`
	Command     string `json:"command"`
	Payload     any    `json:"payload"`
	PayloadJSON string `json:"payloadJSON"`
}

type gatewayPreparedRun struct {
	CmdText string `json:"cmdText"`
	Plan    struct {
		Argv        []string `json:"argv"`
		Cwd         string   `json:"cwd"`
		CommandText string   `json:"commandText"`
		RawCommand  string   `json:"rawCommand"`
		AgentID     string   `json:"agentId"`
		SessionKey  string   `json:"sessionKey"`
	} `json:"plan"`
}

type gatewaySession struct {
	conn           *websocket.Conn
	requestTimeout time.Duration
}

type gatewayRequestError struct {
	method string
	err    gatewayError
}

func (e *gatewayRequestError) Error() string {
	if strings.TrimSpace(e.err.Message) != "" {
		return fmt.Sprintf("%s failed: %s", e.method, e.err.Message)
	}
	if strings.TrimSpace(e.err.Code) != "" {
		return fmt.Sprintf("%s failed: %s", e.method, e.err.Code)
	}
	return fmt.Sprintf("%s failed", e.method)
}

func NewGatewayBackend(cfg GatewayBackendConfig) (*GatewayBackend, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		cfg.URL = defaultGatewayURL
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaultGatewayRequestTimeout
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		cfg.ClientID = "gateway-client"
	}
	if strings.TrimSpace(cfg.ClientDisplayName) == "" {
		cfg.ClientDisplayName = "fleet"
	}
	if strings.TrimSpace(cfg.ClientVersion) == "" {
		cfg.ClientVersion = "fleetd"
	}
	if strings.TrimSpace(cfg.Platform) == "" {
		cfg.Platform = runtime.GOOS
	}
	if strings.TrimSpace(cfg.DeviceFamily) == "" {
		cfg.DeviceFamily = "server"
	}
	if strings.TrimSpace(cfg.DeviceIdentityFile) == "" {
		cfg.DeviceIdentityFile = defaultGatewayIdentityPath()
	}
	return &GatewayBackend{cfg: cfg}, nil
}

func (b *GatewayBackend) ListPendingClaims(ctx context.Context) ([]spec.FleetPendingClaim, error) {
	session, err := b.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	var payload gatewayDevicePairList
	if err := session.request(ctx, "device.pair.list", map[string]any{}, &payload); err != nil {
		return nil, err
	}
	claims := make([]spec.FleetPendingClaim, 0, len(payload.Pending))
	for _, item := range payload.Pending {
		if !isNodeClaim(item.Role, item.Roles) {
			continue
		}
		requestedAt := unixMilli(item.TS)
		claims = append(claims, spec.FleetPendingClaim{
			PairingID:    item.RequestID,
			DeviceID:     item.DeviceID,
			DisplayName:  item.DisplayName,
			PublicKey:    item.PublicKey,
			Platform:     item.Platform,
			DeviceFamily: item.DeviceFamily,
			ClientID:     item.ClientID,
			ClientMode:   item.ClientMode,
			Role:         firstNonEmpty(item.Role, "node"),
			RemoteIP:     item.RemoteIP,
			Status:       "pending",
			RequestedAt:  requestedAt,
			UpdatedAt:    requestedAt,
		})
	}
	sort.Slice(claims, func(i, j int) bool {
		return claims[i].UpdatedAt.After(claims[j].UpdatedAt)
	})
	return claims, nil
}

func (b *GatewayBackend) ApproveClaim(ctx context.Context, pairingID string) (spec.FleetOwnedDevice, []spec.FleetOwnedNode, error) {
	session, err := b.connect(ctx)
	if err != nil {
		return spec.FleetOwnedDevice{}, nil, err
	}
	defer session.Close()

	var payload gatewayApproveDeviceResponse
	if err := session.request(ctx, "device.pair.approve", map[string]any{"requestId": pairingID}, &payload); err != nil {
		return spec.FleetOwnedDevice{}, nil, err
	}
	device := toOwnedDevice(payload.Device)
	nodes, err := b.listNodesWithSession(ctx, session)
	if err != nil {
		return device, nil, nil
	}
	var matched []spec.FleetOwnedNode
	for _, node := range nodes {
		if node.NodeID == device.DeviceID || node.DeviceID == device.DeviceID {
			matched = append(matched, node)
		}
	}
	return device, matched, nil
}

func (b *GatewayBackend) RejectClaim(ctx context.Context, pairingID string) error {
	session, err := b.connect(ctx)
	if err != nil {
		return err
	}
	defer session.Close()
	return session.request(ctx, "device.pair.reject", map[string]any{"requestId": pairingID}, nil)
}

func (b *GatewayBackend) UnclaimDevice(ctx context.Context, deviceID string) error {
	session, err := b.connect(ctx)
	if err != nil {
		return err
	}
	defer session.Close()
	return session.request(ctx, "device.pair.remove", map[string]any{"deviceId": deviceID}, nil)
}

func (b *GatewayBackend) ListNodes(ctx context.Context) ([]spec.FleetOwnedNode, error) {
	session, err := b.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	return b.listNodesWithSession(ctx, session)
}

func (b *GatewayBackend) DescribeNode(ctx context.Context, nodeID string) (spec.FleetOwnedNode, error) {
	session, err := b.connect(ctx)
	if err != nil {
		return spec.FleetOwnedNode{}, err
	}
	defer session.Close()

	var payload gatewayNode
	if err := session.request(ctx, "node.describe", map[string]any{"nodeId": nodeID}, &payload); err != nil {
		if isUnknownNodeError(err) {
			return spec.FleetOwnedNode{}, store.ErrNotFound
		}
		return spec.FleetOwnedNode{}, err
	}
	return toOwnedNode(payload), nil
}

func (b *GatewayBackend) InvokeNode(ctx context.Context, nodeID, command string, params map[string]any) (spec.FleetInvokeResponse, error) {
	session, err := b.connect(ctx)
	if err != nil {
		return spec.FleetInvokeResponse{}, err
	}
	defer session.Close()

	payload, err := b.invokeNodeWithSession(ctx, session, nodeID, command, params, b.cfg.RequestTimeout)
	if err != nil {
		return spec.FleetInvokeResponse{}, err
	}
	return spec.FleetInvokeResponse{
		NodeID:      firstNonEmpty(payload.NodeID, nodeID),
		Command:     firstNonEmpty(payload.Command, command),
		OK:          payload.OK,
		Payload:     payload.Payload,
		PayloadJSON: payload.PayloadJSON,
	}, nil
}

func (b *GatewayBackend) RunNode(ctx context.Context, nodeID string, request spec.FleetRunRequest) (spec.FleetRunResponse, error) {
	if len(request.Command) == 0 {
		return spec.FleetRunResponse{}, errors.New("command is required")
	}
	session, err := b.connect(ctx)
	if err != nil {
		return spec.FleetRunResponse{}, err
	}
	defer session.Close()

	rawCommand := shellQuote(request.Command)
	prepareParams := map[string]any{
		"command":    request.Command,
		"rawCommand": rawCommand,
	}
	if strings.TrimSpace(request.CWD) != "" {
		prepareParams["cwd"] = request.CWD
	}
	prepare, err := b.invokeNodeWithSession(ctx, session, nodeID, "system.run.prepare", prepareParams, 15*time.Second)
	if err != nil {
		return spec.FleetRunResponse{}, err
	}
	var prepared gatewayPreparedRun
	if err := decodeInvokePayload(prepare, &prepared); err != nil {
		return spec.FleetRunResponse{}, fmt.Errorf("decode system.run.prepare payload: %w", err)
	}
	if len(prepared.Plan.Argv) == 0 {
		return spec.FleetRunResponse{}, errors.New("system.run.prepare returned an empty argv")
	}

	runParams := map[string]any{
		"command":       prepared.Plan.Argv,
		"rawCommand":    firstNonEmpty(prepared.Plan.CommandText, prepared.Plan.RawCommand, prepared.CmdText, rawCommand),
		"systemRunPlan": prepared.Plan,
	}
	if strings.TrimSpace(prepared.Plan.Cwd) != "" {
		runParams["cwd"] = prepared.Plan.Cwd
	} else if strings.TrimSpace(request.CWD) != "" {
		runParams["cwd"] = request.CWD
	}
	if len(request.Env) > 0 {
		runParams["env"] = request.Env
	}

	run, err := b.invokeNodeWithSession(ctx, session, nodeID, "system.run", runParams, b.cfg.RequestTimeout)
	if err != nil {
		return spec.FleetRunResponse{}, err
	}
	result, err := mapPayload(run)
	if err != nil {
		return spec.FleetRunResponse{}, err
	}
	return spec.FleetRunResponse{
		NodeID:   firstNonEmpty(run.NodeID, nodeID),
		Accepted: true,
		Result:   result,
	}, nil
}

func (b *GatewayBackend) listNodesWithSession(ctx context.Context, session *gatewaySession) ([]spec.FleetOwnedNode, error) {
	var payload gatewayNodeListResponse
	if err := session.request(ctx, "node.list", map[string]any{}, &payload); err != nil {
		return nil, err
	}
	nodes := make([]spec.FleetOwnedNode, 0, len(payload.Nodes))
	for _, item := range payload.Nodes {
		nodes = append(nodes, toOwnedNode(item))
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].NodeID < nodes[j].NodeID
	})
	return nodes, nil
}

func (b *GatewayBackend) describeNodeWithSession(ctx context.Context, session *gatewaySession, nodeID string) (spec.FleetOwnedNode, error) {
	var payload gatewayNode
	if err := session.request(ctx, "node.describe", map[string]any{"nodeId": nodeID}, &payload); err != nil {
		return spec.FleetOwnedNode{}, err
	}
	return toOwnedNode(payload), nil
}

func (b *GatewayBackend) invokeNodeWithSession(ctx context.Context, session *gatewaySession, nodeID, command string, params any, timeout time.Duration) (gatewayInvokePayload, error) {
	invokeTimeout := int(math.Max(0, float64(timeout/time.Millisecond)))
	var payload gatewayInvokePayload
	err := session.request(ctx, "node.invoke", map[string]any{
		"nodeId":         nodeID,
		"command":        command,
		"params":         params,
		"timeoutMs":      invokeTimeout,
		"idempotencyKey": randomRequestID(),
	}, &payload)
	if err != nil {
		return gatewayInvokePayload{}, err
	}
	if payload.Payload == nil && strings.TrimSpace(payload.PayloadJSON) != "" {
		var decoded any
		if json.Unmarshal([]byte(payload.PayloadJSON), &decoded) == nil {
			payload.Payload = decoded
		}
	}
	return payload, nil
}

func (b *GatewayBackend) connect(ctx context.Context) (*gatewaySession, error) {
	session, identity, usedStoredToken, err := b.connectOnce(ctx)
	if err == nil {
		return session, nil
	}
	if !usedStoredToken {
		return nil, err
	}
	if clearErr := b.updateIdentity(identity.DeviceID, func(current *gatewayIdentity) {
		current.DeviceToken = ""
		current.TokenScopes = nil
	}); clearErr != nil {
		return nil, err
	}
	return b.connectOnceRetry(ctx)
}

func (b *GatewayBackend) connectOnceRetry(ctx context.Context) (*gatewaySession, error) {
	session, _, _, err := b.connectOnce(ctx)
	return session, err
}

func (b *GatewayBackend) connectOnce(ctx context.Context) (*gatewaySession, *gatewayIdentity, bool, error) {
	identity, err := b.loadOrCreateIdentity()
	if err != nil {
		return nil, nil, false, err
	}
	conn, err := b.dial(ctx)
	if err != nil {
		return nil, identity, false, err
	}
	session := &gatewaySession{conn: conn, requestTimeout: b.cfg.RequestTimeout}
	challenge, err := session.waitForChallenge(ctx)
	if err != nil {
		session.Close()
		return nil, identity, false, err
	}

	params, usedStoredToken, err := b.buildConnectParams(identity, challenge.Nonce)
	if err != nil {
		session.Close()
		return nil, identity, false, err
	}
	var hello gatewayHelloOk
	if err := session.request(ctx, "connect", params, &hello); err != nil {
		session.Close()
		return nil, identity, usedStoredToken, err
	}
	if hello.Type != "hello-ok" {
		session.Close()
		return nil, identity, usedStoredToken, errors.New("gateway connect did not return hello-ok")
	}
	if strings.TrimSpace(hello.Auth.DeviceToken) != "" && hello.Auth.DeviceToken != identity.DeviceToken {
		_ = b.updateIdentity(identity.DeviceID, func(current *gatewayIdentity) {
			current.DeviceToken = hello.Auth.DeviceToken
			current.TokenScopes = hello.Auth.Scopes
		})
	}
	return session, identity, usedStoredToken, nil
}

func (b *GatewayBackend) dial(ctx context.Context) (*websocket.Conn, error) {
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: b.cfg.RequestTimeout,
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(b.cfg.URL)), "wss://") {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
		if strings.TrimSpace(b.cfg.CAFile) != "" {
			pool, err := x509.SystemCertPool()
			if err != nil || pool == nil {
				pool = x509.NewCertPool()
			}
			pemBytes, err := os.ReadFile(b.cfg.CAFile)
			if err != nil {
				return nil, fmt.Errorf("read fabric gateway ca file: %w", err)
			}
			if !pool.AppendCertsFromPEM(pemBytes) {
				return nil, errors.New("fabric gateway ca file does not contain a valid certificate")
			}
			tlsConfig.RootCAs = pool
		}
		dialer.TLSClientConfig = tlsConfig
	}
	conn, _, err := dialer.DialContext(ctx, b.cfg.URL, nil)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (b *GatewayBackend) buildConnectParams(identity *gatewayIdentity, nonce string) (gatewayConnectParams, bool, error) {
	signedAt := time.Now().UnixMilli()
	scopes := append([]string(nil), defaultGatewayScopes...)
	signatureToken := strings.TrimSpace(b.cfg.Token)
	auth := &struct {
		Token       string `json:"token,omitempty"`
		DeviceToken string `json:"deviceToken,omitempty"`
		Password    string `json:"password,omitempty"`
	}{}
	usedStoredToken := false
	if strings.TrimSpace(identity.DeviceToken) != "" {
		auth.DeviceToken = identity.DeviceToken
		signatureToken = identity.DeviceToken
		usedStoredToken = true
	}
	if !usedStoredToken && strings.TrimSpace(b.cfg.Token) != "" {
		auth.Token = strings.TrimSpace(b.cfg.Token)
	}
	if !usedStoredToken && strings.TrimSpace(b.cfg.Password) != "" {
		auth.Password = strings.TrimSpace(b.cfg.Password)
	}
	if auth.Token == "" && auth.DeviceToken == "" && auth.Password == "" {
		auth = nil
	}

	payload := buildDeviceAuthPayloadV3(deviceAuthPayloadParams{
		DeviceID:     identity.DeviceID,
		ClientID:     b.cfg.ClientID,
		ClientMode:   "backend",
		Role:         "operator",
		Scopes:       scopes,
		SignedAtMs:   signedAt,
		Token:        signatureToken,
		Nonce:        nonce,
		Platform:     b.cfg.Platform,
		DeviceFamily: b.cfg.DeviceFamily,
	})
	signature, publicKey, err := signIdentityPayload(identity, payload)
	if err != nil {
		return gatewayConnectParams{}, false, err
	}
	params := gatewayConnectParams{
		MinProtocol: gatewayProtocolVersion,
		MaxProtocol: gatewayProtocolVersion,
		Role:        "operator",
		Scopes:      scopes,
		Auth:        auth,
	}
	params.Client.ID = b.cfg.ClientID
	params.Client.DisplayName = b.cfg.ClientDisplayName
	params.Client.Version = b.cfg.ClientVersion
	params.Client.Platform = b.cfg.Platform
	params.Client.DeviceFamily = b.cfg.DeviceFamily
	params.Client.ModelIdentifier = b.cfg.ModelIdentifier
	params.Client.Mode = "backend"
	params.Device = &struct {
		ID        string `json:"id"`
		PublicKey string `json:"publicKey"`
		Signature string `json:"signature"`
		SignedAt  int64  `json:"signedAt"`
		Nonce     string `json:"nonce"`
	}{
		ID:        identity.DeviceID,
		PublicKey: publicKey,
		Signature: signature,
		SignedAt:  signedAt,
		Nonce:     nonce,
	}
	return params, usedStoredToken, nil
}

func (b *GatewayBackend) loadOrCreateIdentity() (*gatewayIdentity, error) {
	b.identityMu.Lock()
	defer b.identityMu.Unlock()

	if raw, err := os.ReadFile(b.cfg.DeviceIdentityFile); err == nil {
		var stored gatewayIdentity
		if json.Unmarshal(raw, &stored) == nil {
			if err := normalizeIdentity(&stored); err == nil {
				if err := writeGatewayIdentityFile(b.cfg.DeviceIdentityFile, &stored); err == nil {
					return &stored, nil
				}
				return &stored, nil
			}
		}
	}
	identity, err := generateGatewayIdentity()
	if err != nil {
		return nil, err
	}
	if err := writeGatewayIdentityFile(b.cfg.DeviceIdentityFile, identity); err != nil {
		return nil, err
	}
	return identity, nil
}

func (b *GatewayBackend) updateIdentity(deviceID string, fn func(*gatewayIdentity)) error {
	b.identityMu.Lock()
	defer b.identityMu.Unlock()

	raw, err := os.ReadFile(b.cfg.DeviceIdentityFile)
	if err != nil {
		return err
	}
	var current gatewayIdentity
	if err := json.Unmarshal(raw, &current); err != nil {
		return err
	}
	if current.DeviceID != deviceID {
		return nil
	}
	fn(&current)
	return writeGatewayIdentityFile(b.cfg.DeviceIdentityFile, &current)
}

func normalizeIdentity(identity *gatewayIdentity) error {
	identity.Version = 1
	pub, err := parsePublicKeyPEM(identity.PublicKeyPEM)
	if err != nil {
		return err
	}
	if _, err := parsePrivateKeyPEM(identity.PrivateKeyPEM); err != nil {
		return err
	}
	identity.DeviceID = deriveDeviceID(pub)
	return nil
}

func generateGatewayIdentity() (*gatewayIdentity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	publicDer, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	privateDer, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	return &gatewayIdentity{
		Version:       1,
		DeviceID:      deriveDeviceID(pub),
		PublicKeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDer})),
		PrivateKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDer})),
		CreatedAtMs:   time.Now().UnixMilli(),
	}, nil
}

func writeGatewayIdentityFile(path string, identity *gatewayIdentity) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(payload, '\n'), 0o600)
}

func signIdentityPayload(identity *gatewayIdentity, payload string) (string, string, error) {
	privateKey, err := parsePrivateKeyPEM(identity.PrivateKeyPEM)
	if err != nil {
		return "", "", err
	}
	publicKey, err := parsePublicKeyPEM(identity.PublicKeyPEM)
	if err != nil {
		return "", "", err
	}
	signature := ed25519.Sign(privateKey, []byte(payload))
	return base64.RawURLEncoding.EncodeToString(signature), base64.RawURLEncoding.EncodeToString(publicKey), nil
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

func defaultGatewayIdentityPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(os.TempDir(), "fleet-device.json")
	}
	return filepath.Join(home, ".fleet", "device.json")
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
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return strings.ToLower(trimmed)
}

func (s *gatewaySession) Close() error {
	return s.conn.Close()
}

func (s *gatewaySession) waitForChallenge(ctx context.Context) (gatewayConnectChallenge, error) {
	for {
		frame, err := s.readFrame(ctx)
		if err != nil {
			return gatewayConnectChallenge{}, err
		}
		if frame.Type != "event" || frame.Event != "connect.challenge" {
			continue
		}
		var payload gatewayConnectChallenge
		if err := json.Unmarshal(frame.Payload, &payload); err != nil {
			return gatewayConnectChallenge{}, err
		}
		if strings.TrimSpace(payload.Nonce) == "" {
			return gatewayConnectChallenge{}, errors.New("gateway connect.challenge did not include a nonce")
		}
		return payload, nil
	}
}

func (s *gatewaySession) request(ctx context.Context, method string, params any, target any) error {
	id := randomRequestID()
	if err := s.writeFrame(ctx, gatewayFrame{
		Type:   "req",
		ID:     id,
		Method: method,
		Params: params,
	}); err != nil {
		return err
	}
	for {
		frame, err := s.readFrame(ctx)
		if err != nil {
			return err
		}
		if frame.Type != "res" || frame.ID != id {
			continue
		}
		if !frame.OK {
			return &gatewayRequestError{method: method, err: derefGatewayError(frame.Error)}
		}
		if target == nil || len(frame.Payload) == 0 || string(frame.Payload) == "null" {
			return nil
		}
		return json.Unmarshal(frame.Payload, target)
	}
}

func (s *gatewaySession) writeFrame(ctx context.Context, frame gatewayFrame) error {
	if err := s.conn.SetWriteDeadline(resolveDeadline(ctx, s.requestTimeout)); err != nil {
		return err
	}
	return s.conn.WriteJSON(frame)
}

func (s *gatewaySession) readFrame(ctx context.Context) (gatewayFrame, error) {
	if err := s.conn.SetReadDeadline(resolveDeadline(ctx, s.requestTimeout)); err != nil {
		return gatewayFrame{}, err
	}
	_, payload, err := s.conn.ReadMessage()
	if err != nil {
		return gatewayFrame{}, err
	}
	var frame gatewayFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		return gatewayFrame{}, err
	}
	return frame, nil
}

func resolveDeadline(ctx context.Context, fallback time.Duration) time.Time {
	now := time.Now()
	fallbackDeadline := now.Add(fallback)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(fallbackDeadline) {
		return deadline
	}
	return fallbackDeadline
}

func randomRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func derefGatewayError(err *gatewayError) gatewayError {
	if err == nil {
		return gatewayError{}
	}
	return *err
}

func isNodeClaim(role string, roles []string) bool {
	if strings.EqualFold(strings.TrimSpace(role), "node") {
		return true
	}
	for _, value := range roles {
		if strings.EqualFold(strings.TrimSpace(value), "node") {
			return true
		}
	}
	return false
}

func toOwnedDevice(device gatewayPairedDevice) spec.FleetOwnedDevice {
	approvedAt := unixMilli(device.ApprovedAtMs)
	return spec.FleetOwnedDevice{
		DeviceID:     device.DeviceID,
		DisplayName:  device.DisplayName,
		Platform:     device.Platform,
		DeviceFamily: device.DeviceFamily,
		ClientID:     device.ClientID,
		ClientMode:   device.ClientMode,
		Role:         device.Role,
		RemoteIP:     device.RemoteIP,
		TokenState:   "paired",
		ApprovedAt:   approvedAt,
		LastSeenAt:   approvedAt,
		UpdatedAt:    time.Now().UTC(),
	}
}

func toOwnedNode(node gatewayNode) spec.FleetOwnedNode {
	connectedAt := unixMilli(node.ConnectedAtMs)
	approvedAt := unixMilli(node.ApprovedAtMs)
	lastSeenAt := connectedAt
	if lastSeenAt.IsZero() {
		lastSeenAt = approvedAt
	}
	status := "offline"
	if node.Connected {
		status = "online"
	}
	return spec.FleetOwnedNode{
		DeviceID:      firstNonEmpty(node.NodeID),
		NodeID:        node.NodeID,
		BackendNodeID: node.NodeID,
		DisplayName:   node.DisplayName,
		Platform:      node.Platform,
		Version:       node.Version,
		CoreVersion:   node.CoreVersion,
		UIVersion:     node.UIVersion,
		ClientID:      node.ClientID,
		ClientMode:    node.ClientMode,
		RemoteIP:      node.RemoteIP,
		DeviceFamily:  node.DeviceFamily,
		ModelID:       node.ModelIdentifier,
		PathEnv:       node.PathEnv,
		Caps:          append([]string(nil), node.Caps...),
		Commands:      append([]string(nil), node.Commands...),
		Permissions:   cloneBoolMap(node.Permissions),
		Status:        status,
		Paired:        node.Paired,
		Connected:     node.Connected,
		ConnectedAt:   connectedAt,
		ApprovedAt:    approvedAt,
		LastSeenAt:    lastSeenAt,
		UpdatedAt:     time.Now().UTC(),
	}
}

func mapPayload(payload gatewayInvokePayload) (map[string]any, error) {
	if payload.Payload == nil && strings.TrimSpace(payload.PayloadJSON) != "" {
		var decoded any
		if err := json.Unmarshal([]byte(payload.PayloadJSON), &decoded); err == nil {
			payload.Payload = decoded
		}
	}
	if payload.Payload == nil {
		return map[string]any{}, nil
	}
	object, ok := payload.Payload.(map[string]any)
	if ok {
		return object, nil
	}
	return map[string]any{"payload": payload.Payload}, nil
}

func decodeInvokePayload(payload gatewayInvokePayload, target any) error {
	if payload.Payload != nil {
		raw, err := json.Marshal(payload.Payload)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, target)
	}
	if strings.TrimSpace(payload.PayloadJSON) == "" {
		return errors.New("invoke payload is empty")
	}
	return json.Unmarshal([]byte(payload.PayloadJSON), target)
}

func unixMilli(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func cloneBoolMap(input map[string]bool) map[string]bool {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]bool, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func shellQuote(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	parts := make([]string, 0, len(argv))
	for _, item := range argv {
		if item == "" {
			parts = append(parts, "''")
			continue
		}
		if strings.IndexFunc(item, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '\'' || r == '"' || r == '\\'
		}) == -1 {
			parts = append(parts, item)
			continue
		}
		parts = append(parts, "'"+strings.ReplaceAll(item, "'", `'"'"'`)+"'")
	}
	return strings.Join(parts, " ")
}

func isUnknownNodeError(err error) bool {
	var requestErr *gatewayRequestError
	if !errors.As(err, &requestErr) {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(requestErr.err.Message))
	return strings.Contains(message, "unknown nodeid") || strings.Contains(message, "unknown node")
}
