package fleetd

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"

	fleetsvc "fleetd/internal/fleet"
	"fleetd/internal/store"
	"fleetd/pkg/spec"
)

const (
	fleetProtocolVersion = 3
	maxNodePayloadBytes  = 25 * 1024 * 1024
)

type Backend struct {
	cfg      Config
	store    store.Store
	started  time.Time
	upgrader websocket.Upgrader

	mu             sync.RWMutex
	sessionsByNode map[string]*nodeSession
	sessionsByDev  map[string]*nodeSession
}

type nodeSession struct {
	backend *Backend
	conn    *websocket.Conn
	connID  string

	sendMu sync.Mutex
	mu     sync.Mutex

	deviceID     string
	nodeID       string
	displayName  string
	publicKey    string
	role         string
	scopes       []string
	clientID     string
	clientMode   string
	platform     string
	deviceFamily string
	modelID      string
	version      string
	pathEnv      string
	commands     []string
	caps         []string
	permissions  map[string]bool
	remoteIP     string
	connectedAt  time.Time
	lastSeenAt   time.Time

	waiters map[string]chan nodeInvokeResult
	closed  chan struct{}
	once    sync.Once
}

type wsFrame struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	OK      bool            `json:"ok,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *wsError        `json:"error,omitempty"`
	Event   string          `json:"event,omitempty"`
	Seq     *int64          `json:"seq,omitempty"`
}

type wsError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

type connectParams struct {
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
		InstanceID      string `json:"instanceId,omitempty"`
	} `json:"client"`
	Caps        []string        `json:"caps,omitempty"`
	Commands    []string        `json:"commands,omitempty"`
	Permissions map[string]bool `json:"permissions,omitempty"`
	PathEnv     string          `json:"pathEnv,omitempty"`
	Role        string          `json:"role,omitempty"`
	Scopes      []string        `json:"scopes,omitempty"`
	Device      *struct {
		ID        string `json:"id"`
		PublicKey string `json:"publicKey"`
		Signature string `json:"signature"`
		SignedAt  int64  `json:"signedAt"`
		Nonce     string `json:"nonce"`
	} `json:"device,omitempty"`
	Auth *struct {
		Token       string `json:"token,omitempty"`
		Bootstrap   string `json:"bootstrapToken,omitempty"`
		DeviceToken string `json:"deviceToken,omitempty"`
		Password    string `json:"password,omitempty"`
	} `json:"auth,omitempty"`
}

type helloOK struct {
	Type     string `json:"type"`
	Protocol int    `json:"protocol"`
	Server   struct {
		Version string `json:"version"`
		ConnID  string `json:"connId"`
	} `json:"server"`
	Features struct {
		Methods []string `json:"methods"`
		Events  []string `json:"events"`
	} `json:"features"`
	Snapshot struct {
		Presence     []map[string]any `json:"presence"`
		Health       map[string]any   `json:"health"`
		StateVersion struct {
			Presence int `json:"presence"`
			Health   int `json:"health"`
		} `json:"stateVersion"`
		UptimeMs int64  `json:"uptimeMs"`
		AuthMode string `json:"authMode,omitempty"`
	} `json:"snapshot"`
	Auth struct {
		DeviceToken string   `json:"deviceToken"`
		Role        string   `json:"role"`
		Scopes      []string `json:"scopes"`
		IssuedAtMs  int64    `json:"issuedAtMs"`
	} `json:"auth"`
	Policy struct {
		MaxPayload       int `json:"maxPayload"`
		MaxBufferedBytes int `json:"maxBufferedBytes"`
		TickIntervalMs   int `json:"tickIntervalMs"`
	} `json:"policy"`
}

type nodeInvokeRequestEvent struct {
	ID             string `json:"id"`
	NodeID         string `json:"nodeId"`
	Command        string `json:"command"`
	ParamsJSON     string `json:"paramsJSON,omitempty"`
	TimeoutMs      int    `json:"timeoutMs,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

type nodeInvokeResult struct {
	ID          string `json:"id"`
	NodeID      string `json:"nodeId"`
	OK          bool   `json:"ok"`
	Payload     any    `json:"payload,omitempty"`
	PayloadJSON string `json:"payloadJSON,omitempty"`
	Error       *struct {
		Code    string `json:"code,omitempty"`
		Message string `json:"message,omitempty"`
	} `json:"error,omitempty"`
}

func NewBackend(cfg Config, backing store.Store) *Backend {
	return &Backend{
		cfg:            cfg,
		store:          backing,
		started:        time.Now().UTC(),
		upgrader:       websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		sessionsByNode: map[string]*nodeSession{},
		sessionsByDev:  map[string]*nodeSession{},
	}
}

func (b *Backend) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := b.upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	session := &nodeSession{
		backend:  b,
		conn:     conn,
		connID:   randomRequestID(),
		remoteIP: remoteHost(r.RemoteAddr),
		waiters:  map[string]chan nodeInvokeResult{},
		closed:   make(chan struct{}),
	}
	if err := session.writeEvent("connect.challenge", map[string]any{"nonce": session.connID}); err != nil {
		_ = conn.Close()
		return
	}
	frame, err := session.readFrame(b.cfg.RequestTimeout)
	if err != nil {
		session.close()
		return
	}
	if frame.Type != "req" || frame.Method != "connect" {
		_ = session.writeResponse(frame.ID, false, nil, &wsError{Code: "INVALID_REQUEST", Message: "connect is required"})
		session.close()
		return
	}
	var params connectParams
	if err := json.Unmarshal(frame.Params, &params); err != nil {
		_ = session.writeResponse(frame.ID, false, nil, &wsError{Code: "INVALID_REQUEST", Message: err.Error()})
		session.close()
		return
	}
	hello, state, err := b.acceptConnect(r.Context(), session, params, session.connID)
	if err != nil {
		_ = session.writeResponse(frame.ID, false, nil, &wsError{Code: "CONNECT_FAILED", Message: err.Error()})
		session.close()
		return
	}
	session.applyState(state)
	b.registerSession(session)
	if err := session.writeResponse(frame.ID, true, hello, nil); err != nil {
		b.unregisterSession(session)
		session.close()
		return
	}
	go session.tickLoop(b.cfg.TickInterval)
	go b.readLoop(session)
}

func (b *Backend) ListPendingClaims(ctx context.Context) ([]spec.FleetPendingClaim, error) {
	claims, err := b.store.ListFleetPendingClaims(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for index := range claims {
		if claims[index].Status == "" {
			claims[index].Status = "pending"
		}
		if claims[index].RequestedAt.IsZero() {
			claims[index].RequestedAt = now
		}
		if claims[index].UpdatedAt.IsZero() {
			claims[index].UpdatedAt = now
		}
	}
	return claims, nil
}

func (b *Backend) ApproveClaim(ctx context.Context, pairingID string) (spec.FleetOwnedDevice, []spec.FleetOwnedNode, error) {
	claim, err := b.store.GetFleetPendingClaim(ctx, pairingID)
	if err != nil {
		return spec.FleetOwnedDevice{}, nil, err
	}
	device := spec.FleetOwnedDevice{
		DeviceID:     claim.DeviceID,
		DisplayName:  claim.DisplayName,
		Platform:     claim.Platform,
		DeviceFamily: claim.DeviceFamily,
		ClientID:     claim.ClientID,
		ClientMode:   claim.ClientMode,
		Role:         claim.Role,
		RemoteIP:     claim.RemoteIP,
		TokenState:   "issued",
		ApprovedAt:   time.Now().UTC(),
		LastSeenAt:   time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	node, err := b.describeLiveNode(ctx, claim.DeviceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return device, nil, nil
		}
		return spec.FleetOwnedDevice{}, nil, err
	}
	device.DisplayName = firstNonEmpty(node.DisplayName, device.DisplayName)
	device.LastSeenAt = node.LastSeenAt
	return device, []spec.FleetOwnedNode{node}, nil
}

func (b *Backend) RejectClaim(ctx context.Context, pairingID string) error {
	claim, err := b.store.GetFleetPendingClaim(ctx, pairingID)
	if err != nil {
		return err
	}
	if session := b.sessionByDevice(claim.DeviceID); session != nil {
		session.close()
	}
	if err := b.store.DeleteFleetNodeAuthState(ctx, claim.DeviceID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return nil
}

func (b *Backend) UnclaimDevice(ctx context.Context, deviceID string) error {
	state, err := b.store.GetFleetNodeAuthState(ctx, deviceID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if errors.Is(err, store.ErrNotFound) {
		state = nil
	}
	session := b.sessionByDevice(deviceID)
	now := time.Now().UTC()
	if claim := pendingClaimFromNodeState(deviceID, session, state, now); claim != nil {
		if err := b.store.UpsertFleetPendingClaim(ctx, *claim); err != nil {
			return err
		}
	}
	if session != nil {
		session.close()
	}
	return nil
}

func (b *Backend) ListNodes(ctx context.Context) ([]spec.FleetOwnedNode, error) {
	b.mu.RLock()
	sessions := make([]*nodeSession, 0, len(b.sessionsByNode))
	for _, session := range b.sessionsByNode {
		sessions = append(sessions, session)
	}
	b.mu.RUnlock()
	nodes := make([]spec.FleetOwnedNode, 0, len(sessions))
	for _, session := range sessions {
		node, err := b.buildNodeSnapshot(ctx, session)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	slices.SortFunc(nodes, func(a, c spec.FleetOwnedNode) int {
		return strings.Compare(a.NodeID, c.NodeID)
	})
	return nodes, nil
}

func (b *Backend) DescribeNode(ctx context.Context, nodeID string) (spec.FleetOwnedNode, error) {
	return b.describeLiveNode(ctx, nodeID)
}

func (b *Backend) InvokeNode(ctx context.Context, nodeID, command string, params map[string]any) (spec.FleetInvokeResponse, error) {
	session := b.sessionByNode(nodeID)
	if session == nil {
		return spec.FleetInvokeResponse{}, fleetsvc.ErrNodeOffline
	}
	requestID := randomRequestID()
	resultCh := make(chan nodeInvokeResult, 1)
	session.addWaiter(requestID, resultCh)
	defer session.removeWaiter(requestID)

	paramsJSON := ""
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return spec.FleetInvokeResponse{}, err
		}
		paramsJSON = string(raw)
	}
	invokeTimeout := nodeInvokeTimeout(b.cfg.RequestTimeout, params)
	if err := session.writeEvent("node.invoke.request", nodeInvokeRequestEvent{
		ID:             requestID,
		NodeID:         nodeID,
		Command:        command,
		ParamsJSON:     paramsJSON,
		TimeoutMs:      int(invokeTimeout / time.Millisecond),
		IdempotencyKey: randomRequestID(),
	}); err != nil {
		return spec.FleetInvokeResponse{}, err
	}

	select {
	case <-ctx.Done():
		return spec.FleetInvokeResponse{}, ctx.Err()
	case <-session.closed:
		return spec.FleetInvokeResponse{}, fmt.Errorf("%w: disconnected while waiting for command result", fleetsvc.ErrNodeOffline)
	case result := <-resultCh:
		response := spec.FleetInvokeResponse{
			NodeID:      firstNonEmpty(result.NodeID, nodeID),
			Command:     command,
			OK:          result.OK,
			Payload:     result.Payload,
			PayloadJSON: result.PayloadJSON,
		}
		if !result.OK && result.Error != nil {
			response.Payload = map[string]any{
				"error": map[string]any{
					"code":    result.Error.Code,
					"message": result.Error.Message,
				},
			}
		}
		return response, nil
	}
}

func nodeInvokeTimeout(defaultTimeout time.Duration, params map[string]any) time.Duration {
	timeout := defaultTimeout
	if paramsTimeout := durationFromAny(params["timeoutMs"]); paramsTimeout > timeout {
		timeout = paramsTimeout
	}
	return timeout
}

func durationFromAny(value any) time.Duration {
	switch typed := value.(type) {
	case int:
		if typed > 0 {
			return time.Duration(typed) * time.Millisecond
		}
	case int64:
		if typed > 0 {
			return time.Duration(typed) * time.Millisecond
		}
	case float64:
		if typed > 0 && !math.IsNaN(typed) && !math.IsInf(typed, 0) {
			return time.Duration(typed) * time.Millisecond
		}
	case json.Number:
		if value, err := typed.Int64(); err == nil && value > 0 {
			return time.Duration(value) * time.Millisecond
		}
	case string:
		if value, err := time.ParseDuration(strings.TrimSpace(typed)); err == nil && value > 0 {
			return value
		}
	}
	return 0
}

func (b *Backend) RunNode(ctx context.Context, nodeID string, request spec.FleetRunRequest) (spec.FleetRunResponse, error) {
	if len(request.Command) == 0 {
		return spec.FleetRunResponse{}, errors.New("command is required")
	}
	prepareParams := map[string]any{
		"command":    request.Command,
		"rawCommand": shellQuote(request.Command),
	}
	if strings.TrimSpace(request.CWD) != "" {
		prepareParams["cwd"] = request.CWD
	}
	prepare, err := b.InvokeNode(ctx, nodeID, "system.run.prepare", prepareParams)
	if err != nil {
		return spec.FleetRunResponse{}, err
	}
	if !prepare.OK {
		return spec.FleetRunResponse{}, errorFromInvokeResponse("system.run.prepare", prepare)
	}
	plan, err := decodeInvokePlan(prepare)
	if err != nil {
		return spec.FleetRunResponse{}, err
	}
	runParams := map[string]any{
		"command":       plan.Argv,
		"rawCommand":    firstNonEmpty(plan.CommandText, shellQuote(request.Command)),
		"systemRunPlan": map[string]any{"argv": plan.Argv, "cwd": plan.CWD, "commandText": plan.CommandText, "agentId": plan.AgentID, "sessionKey": plan.SessionKey},
	}
	if strings.TrimSpace(plan.CWD) != "" {
		runParams["cwd"] = plan.CWD
	} else if strings.TrimSpace(request.CWD) != "" {
		runParams["cwd"] = request.CWD
	}
	if len(request.Env) > 0 {
		runParams["env"] = request.Env
	}
	run, err := b.InvokeNode(ctx, nodeID, "system.run", runParams)
	if err != nil {
		return spec.FleetRunResponse{}, err
	}
	if !run.OK {
		return spec.FleetRunResponse{}, errorFromInvokeResponse("system.run", run)
	}
	result, err := payloadAsMap(run.Payload, run.PayloadJSON)
	if err != nil {
		return spec.FleetRunResponse{}, err
	}
	return spec.FleetRunResponse{
		NodeID:   firstNonEmpty(run.NodeID, nodeID),
		Accepted: true,
		Result:   result,
	}, nil
}

func (b *Backend) acceptConnect(ctx context.Context, session *nodeSession, params connectParams, nonce string) (helloOK, spec.FleetNodeAuthState, error) {
	if strings.TrimSpace(params.Role) != "node" {
		return helloOK{}, spec.FleetNodeAuthState{}, errors.New("only role=node is supported")
	}
	if params.Device == nil {
		return helloOK{}, spec.FleetNodeAuthState{}, errors.New("device identity is required")
	}
	deviceID, err := verifyDeviceSignature(params, nonce, b.cfg, b.store)
	if err != nil {
		return helloOK{}, spec.FleetNodeAuthState{}, err
	}
	now := time.Now().UTC()
	state, err := b.store.GetFleetNodeAuthState(ctx, deviceID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return helloOK{}, spec.FleetNodeAuthState{}, err
	}
	authMethod, scopes, err := b.authenticateConnect(ctx, deviceID, params, state)
	if err != nil {
		return helloOK{}, spec.FleetNodeAuthState{}, err
	}
	token, issuedAt, err := b.mintDeviceToken(deviceID, params.Role, scopes)
	if err != nil {
		return helloOK{}, spec.FleetNodeAuthState{}, err
	}
	nextState := spec.FleetNodeAuthState{
		DeviceID:        deviceID,
		NodeID:          deviceID,
		DisplayName:     firstNonEmpty(params.Client.DisplayName, deviceID),
		PublicKey:       params.Device.PublicKey,
		Role:            params.Role,
		Scopes:          append([]string(nil), scopes...),
		ClientID:        params.Client.ID,
		ClientMode:      params.Client.Mode,
		Platform:        params.Client.Platform,
		DeviceFamily:    params.Client.DeviceFamily,
		ModelID:         params.Client.ModelIdentifier,
		Version:         params.Client.Version,
		PathEnv:         params.PathEnv,
		Commands:        cloneStrings(params.Commands),
		Caps:            cloneStrings(params.Caps),
		Permissions:     cloneBoolMap(params.Permissions),
		BootstrapMethod: authMethod,
		TokenHash:       b.hashToken(token),
		TokenIssuedAt:   issuedAt,
		LastSeenAt:      now,
		UpdatedAt:       now,
	}
	if err := b.store.UpsertFleetNodeAuthState(ctx, nextState); err != nil {
		return helloOK{}, spec.FleetNodeAuthState{}, err
	}
	if owned, err := b.store.FindFleetOwnedDeviceByDeviceID(ctx, deviceID); err == nil && owned.UserID != "" {
		_ = b.store.DeleteFleetPendingClaim(ctx, pairingIDForDevice(deviceID))
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return helloOK{}, spec.FleetNodeAuthState{}, err
	} else if errors.Is(err, store.ErrNotFound) {
		if err := b.store.UpsertFleetPendingClaim(ctx, spec.FleetPendingClaim{
			PairingID:    pairingIDForDevice(deviceID),
			DeviceID:     deviceID,
			DisplayName:  nextState.DisplayName,
			PublicKey:    nextState.PublicKey,
			Platform:     nextState.Platform,
			DeviceFamily: nextState.DeviceFamily,
			ClientID:     nextState.ClientID,
			ClientMode:   nextState.ClientMode,
			Role:         nextState.Role,
			RemoteIP:     session.remoteIP,
			Status:       "pending",
			RequestedAt:  now,
			UpdatedAt:    now,
		}); err != nil {
			return helloOK{}, spec.FleetNodeAuthState{}, err
		}
	}

	hello := helloOK{Type: "hello-ok", Protocol: fleetProtocolVersion}
	hello.Server.Version = "fleetd"
	hello.Server.ConnID = session.connID
	hello.Features.Methods = []string{"connect", "node.invoke.result", "node.event", "skills.bins"}
	hello.Features.Events = []string{"connect.challenge", "node.invoke.request", "tick", "shutdown"}
	hello.Snapshot.Presence = []map[string]any{}
	hello.Snapshot.Health = map[string]any{}
	hello.Snapshot.StateVersion.Presence = 0
	hello.Snapshot.StateVersion.Health = 0
	hello.Snapshot.UptimeMs = time.Since(b.started).Milliseconds()
	hello.Snapshot.AuthMode = b.snapshotAuthMode()
	hello.Auth.DeviceToken = token
	hello.Auth.Role = params.Role
	hello.Auth.Scopes = append([]string(nil), scopes...)
	hello.Auth.IssuedAtMs = issuedAt.UnixMilli()
	hello.Policy.MaxPayload = maxNodePayloadBytes
	hello.Policy.MaxBufferedBytes = maxNodePayloadBytes
	tickMs := int(b.cfg.TickInterval / time.Millisecond)
	if tickMs < 1 {
		tickMs = 1
	}
	hello.Policy.TickIntervalMs = tickMs
	return hello, nextState, nil
}

func (b *Backend) authenticateConnect(ctx context.Context, deviceID string, params connectParams, state *spec.FleetNodeAuthState) (string, []string, error) {
	scopes := cloneStrings(params.Scopes)
	if params.Auth != nil && strings.TrimSpace(params.Auth.DeviceToken) != "" {
		if state == nil {
			var err error
			state, err = b.store.GetFleetNodeAuthState(ctx, deviceID)
			if err != nil {
				return "", nil, errors.New("device token is not recognized")
			}
		}
		if err := b.validateDeviceToken(deviceID, strings.TrimSpace(params.Auth.DeviceToken), state); err != nil {
			return "", nil, err
		}
		if len(scopes) == 0 && len(state.Scopes) > 0 {
			scopes = cloneStrings(state.Scopes)
		}
		return "device_token", scopes, nil
	}
	if params.Auth != nil && strings.TrimSpace(params.Auth.Token) != "" && strings.TrimSpace(params.Auth.Token) == strings.TrimSpace(b.cfg.GatewayToken) {
		return "token", scopes, nil
	}
	if params.Auth != nil && strings.TrimSpace(params.Auth.Password) != "" && strings.TrimSpace(params.Auth.Password) == strings.TrimSpace(b.cfg.GatewayPassword) {
		return "password", scopes, nil
	}
	if strings.TrimSpace(b.cfg.GatewayToken) == "" && strings.TrimSpace(b.cfg.GatewayPassword) == "" {
		return "none", scopes, nil
	}
	return "", nil, errors.New("invalid node credentials")
}

func (b *Backend) validateDeviceToken(deviceID, token string, state *spec.FleetNodeAuthState) error {
	parsed, err := jwt.Parse(token, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(b.cfg.MasterKey), nil
	})
	if err != nil || !parsed.Valid {
		return errors.New("invalid device token")
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return errors.New("invalid device token claims")
	}
	if fleetAsString(claims["sub"], "") != deviceID {
		return errors.New("device token subject mismatch")
	}
	if subtle.ConstantTimeCompare([]byte(state.TokenHash), []byte(b.hashToken(token))) != 1 {
		return errors.New("device token mismatch")
	}
	return nil
}

func (b *Backend) mintDeviceToken(deviceID, role string, scopes []string) (string, time.Time, error) {
	issuedAt := time.Now().UTC()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":    deviceID,
		"role":   role,
		"scopes": scopes,
		"iat":    issuedAt.Unix(),
	})
	signed, err := token.SignedString([]byte(b.cfg.MasterKey))
	return signed, issuedAt, err
}

func (b *Backend) hashToken(token string) string {
	mac := hmac.New(sha256.New, []byte(b.cfg.MasterKey))
	_, _ = mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

func (b *Backend) readLoop(session *nodeSession) {
	defer func() {
		b.unregisterSession(session)
		session.close()
	}()
	for {
		frame, err := session.readFrame(0)
		if err != nil {
			return
		}
		switch {
		case frame.Type != "req":
			continue
		case frame.Method == "node.invoke.result":
			var result nodeInvokeResult
			if err := json.Unmarshal(frame.Params, &result); err != nil {
				_ = session.writeResponse(frame.ID, false, nil, &wsError{Code: "INVALID_REQUEST", Message: err.Error()})
				continue
			}
			session.touch()
			session.deliver(result)
			_ = session.writeResponse(frame.ID, true, map[string]any{"status": "received"}, nil)
		case frame.Method == "node.event":
			session.touch()
			_ = session.writeResponse(frame.ID, true, map[string]any{"status": "received"}, nil)
		case frame.Method == "skills.bins":
			session.touch()
			_ = session.writeResponse(frame.ID, true, map[string]any{"bins": []any{}}, nil)
		default:
			_ = session.writeResponse(frame.ID, false, nil, &wsError{Code: "UNKNOWN_METHOD", Message: "unsupported method"})
		}
	}
}

func (b *Backend) registerSession(session *nodeSession) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if current := b.sessionsByDev[session.deviceID]; current != nil && current != session {
		current.close()
	}
	b.sessionsByDev[session.deviceID] = session
	b.sessionsByNode[session.nodeID] = session
}

func (b *Backend) unregisterSession(session *nodeSession) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if current := b.sessionsByDev[session.deviceID]; current == session {
		delete(b.sessionsByDev, session.deviceID)
	}
	if current := b.sessionsByNode[session.nodeID]; current == session {
		delete(b.sessionsByNode, session.nodeID)
	}
}

func (b *Backend) sessionByNode(nodeID string) *nodeSession {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.sessionsByNode[nodeID]
}

func (b *Backend) sessionByDevice(deviceID string) *nodeSession {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.sessionsByDev[deviceID]
}

func (b *Backend) describeLiveNode(ctx context.Context, nodeID string) (spec.FleetOwnedNode, error) {
	session := b.sessionByNode(nodeID)
	if session == nil {
		return spec.FleetOwnedNode{}, store.ErrNotFound
	}
	return b.buildNodeSnapshot(ctx, session)
}

func (b *Backend) buildNodeSnapshot(ctx context.Context, session *nodeSession) (spec.FleetOwnedNode, error) {
	session.mu.Lock()
	snapshot := spec.FleetOwnedNode{
		DeviceID:     session.deviceID,
		NodeID:       session.nodeID,
		DisplayName:  session.displayName,
		Platform:     session.platform,
		Version:      session.version,
		ClientID:     session.clientID,
		ClientMode:   session.clientMode,
		RemoteIP:     session.remoteIP,
		DeviceFamily: session.deviceFamily,
		ModelID:      session.modelID,
		PathEnv:      session.pathEnv,
		Caps:         cloneStrings(session.caps),
		Commands:     cloneStrings(session.commands),
		Permissions:  cloneBoolMap(session.permissions),
		Status:       "online",
		Connected:    true,
		ConnectedAt:  session.connectedAt,
		LastSeenAt:   session.lastSeenAt,
		UpdatedAt:    time.Now().UTC(),
	}
	session.mu.Unlock()
	if owned, err := b.store.FindFleetOwnedDeviceByDeviceID(ctx, snapshot.DeviceID); err == nil {
		snapshot.Paired = true
		snapshot.ApprovedAt = owned.ApprovedAt
	}
	return snapshot, nil
}

func (b *Backend) snapshotAuthMode() string {
	switch {
	case strings.TrimSpace(b.cfg.GatewayToken) != "":
		return "token"
	case strings.TrimSpace(b.cfg.GatewayPassword) != "":
		return "password"
	default:
		return "none"
	}
}

func (s *nodeSession) applyState(state spec.FleetNodeAuthState) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deviceID = state.DeviceID
	s.nodeID = firstNonEmpty(state.NodeID, state.DeviceID)
	s.displayName = firstNonEmpty(state.DisplayName, state.DeviceID)
	s.publicKey = state.PublicKey
	s.role = state.Role
	s.scopes = cloneStrings(state.Scopes)
	s.clientID = state.ClientID
	s.clientMode = state.ClientMode
	s.platform = state.Platform
	s.deviceFamily = state.DeviceFamily
	s.modelID = state.ModelID
	s.version = state.Version
	s.pathEnv = state.PathEnv
	s.commands = cloneStrings(state.Commands)
	s.caps = cloneStrings(state.Caps)
	s.permissions = cloneBoolMap(state.Permissions)
	s.connectedAt = now
	s.lastSeenAt = now
}

func (s *nodeSession) addWaiter(id string, ch chan nodeInvokeResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waiters[id] = ch
}

func (s *nodeSession) removeWaiter(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.waiters, id)
}

func (s *nodeSession) deliver(result nodeInvokeResult) {
	s.mu.Lock()
	ch := s.waiters[result.ID]
	s.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- result:
	default:
	}
}

func (s *nodeSession) touch() {
	s.mu.Lock()
	s.lastSeenAt = time.Now().UTC()
	s.mu.Unlock()
}

func (s *nodeSession) tickLoop(interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := s.writeEvent("tick", map[string]any{"ts": time.Now().UnixMilli()}); err != nil {
				s.close()
				return
			}
		case <-s.closed:
			return
		}
	}
}

func (s *nodeSession) close() {
	s.once.Do(func() {
		close(s.closed)
		_ = s.conn.Close()
	})
}

func (s *nodeSession) writeResponse(id string, ok bool, payload any, frameErr *wsError) error {
	return s.writeJSON(wsFrame{
		Type:    "res",
		ID:      id,
		OK:      ok,
		Payload: mustRawJSON(payload),
		Error:   frameErr,
	})
}

func (s *nodeSession) writeEvent(event string, payload any) error {
	return s.writeJSON(wsFrame{
		Type:    "event",
		Event:   event,
		Payload: mustRawJSON(payload),
	})
}

func (s *nodeSession) writeJSON(frame wsFrame) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	deadline := time.Now().Add(30 * time.Second)
	_ = s.conn.SetWriteDeadline(deadline)
	return s.conn.WriteJSON(frame)
}

func (s *nodeSession) readFrame(timeout time.Duration) (wsFrame, error) {
	if timeout > 0 {
		_ = s.conn.SetReadDeadline(time.Now().Add(timeout))
	} else {
		_ = s.conn.SetReadDeadline(time.Time{})
	}
	_, payload, err := s.conn.ReadMessage()
	if err != nil {
		return wsFrame{}, err
	}
	var frame wsFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		return wsFrame{}, err
	}
	return frame, nil
}

func verifyDeviceSignature(params connectParams, nonce string, cfg Config, backing store.Store) (string, error) {
	if params.Device == nil {
		return "", errors.New("device identity is required")
	}
	publicKeyRaw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(params.Device.PublicKey))
	if err != nil {
		return "", errors.New("invalid device public key")
	}
	if len(publicKeyRaw) != ed25519.PublicKeySize {
		return "", errors.New("invalid device public key length")
	}
	deviceID := deriveDeviceID(ed25519.PublicKey(publicKeyRaw))
	if deviceID != strings.TrimSpace(params.Device.ID) {
		return "", errors.New("device id does not match public key")
	}
	tokenForSignature := ""
	switch {
	case params.Auth != nil && strings.TrimSpace(params.Auth.DeviceToken) != "":
		tokenForSignature = strings.TrimSpace(params.Auth.DeviceToken)
	case params.Auth != nil && strings.TrimSpace(params.Auth.Token) != "":
		tokenForSignature = strings.TrimSpace(params.Auth.Token)
	case params.Auth != nil && strings.TrimSpace(params.Auth.Password) != "":
		tokenForSignature = ""
	}
	payload := buildDeviceAuthPayloadV3(deviceAuthPayloadParams{
		DeviceID:     deviceID,
		ClientID:     params.Client.ID,
		ClientMode:   params.Client.Mode,
		Role:         params.Role,
		Scopes:       params.Scopes,
		SignedAtMs:   params.Device.SignedAt,
		Token:        tokenForSignature,
		Nonce:        nonce,
		Platform:     params.Client.Platform,
		DeviceFamily: params.Client.DeviceFamily,
	})
	signature, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(params.Device.Signature))
	if err != nil {
		return "", errors.New("invalid device signature")
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKeyRaw), []byte(payload), signature) {
		return "", errors.New("device signature verification failed")
	}
	if skew := time.Since(time.UnixMilli(params.Device.SignedAt)); skew > 10*time.Minute || skew < -10*time.Minute {
		return "", errors.New("device signature timestamp is outside the accepted window")
	}
	if state, err := backing.GetFleetNodeAuthState(context.Background(), deviceID); err == nil && state.PublicKey != "" && state.PublicKey != params.Device.PublicKey {
		return "", errors.New("device public key changed unexpectedly")
	}
	return deviceID, nil
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
		strings.ToLower(strings.TrimSpace(params.Platform)),
		strings.ToLower(strings.TrimSpace(params.DeviceFamily)),
	}, "|")
}

func deriveDeviceID(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:])
}

func randomRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func pairingIDForDevice(deviceID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(deviceID)))
	return "claim-" + hex.EncodeToString(sum[:])
}

func pendingClaimFromNodeState(deviceID string, session *nodeSession, state *spec.FleetNodeAuthState, now time.Time) *spec.FleetPendingClaim {
	claim := spec.FleetPendingClaim{
		PairingID:   pairingIDForDevice(deviceID),
		DeviceID:    strings.TrimSpace(deviceID),
		Status:      "pending",
		RequestedAt: now,
		UpdatedAt:   now,
	}
	var hasData bool
	if state != nil {
		claim.DisplayName = firstNonEmpty(state.DisplayName, deviceID)
		claim.PublicKey = state.PublicKey
		claim.Platform = state.Platform
		claim.DeviceFamily = state.DeviceFamily
		claim.ClientID = state.ClientID
		claim.ClientMode = state.ClientMode
		claim.Role = state.Role
		hasData = true
	}
	if session != nil {
		session.mu.Lock()
		claim.DisplayName = firstNonEmpty(session.displayName, claim.DisplayName, deviceID)
		claim.PublicKey = firstNonEmpty(session.publicKey, claim.PublicKey)
		claim.Platform = firstNonEmpty(session.platform, claim.Platform)
		claim.DeviceFamily = firstNonEmpty(session.deviceFamily, claim.DeviceFamily)
		claim.ClientID = firstNonEmpty(session.clientID, claim.ClientID)
		claim.ClientMode = firstNonEmpty(session.clientMode, claim.ClientMode)
		claim.Role = firstNonEmpty(session.role, claim.Role)
		claim.RemoteIP = firstNonEmpty(session.remoteIP, claim.RemoteIP)
		session.mu.Unlock()
		hasData = true
	}
	if !hasData {
		return nil
	}
	if claim.DisplayName == "" {
		claim.DisplayName = deviceID
	}
	if claim.Role == "" {
		claim.Role = "node"
	}
	return &claim
}

func remoteHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	output := make(map[string]bool, len(values))
	for key, value := range values {
		output[key] = value
	}
	return output
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func mustRawJSON(payload any) json.RawMessage {
	if payload == nil {
		return json.RawMessage("null")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage("null")
	}
	return raw
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

type preparedPlan struct {
	CmdText string `json:"cmdText"`
	Plan    struct {
		Argv        []string `json:"argv"`
		CWD         string   `json:"cwd"`
		CommandText string   `json:"commandText"`
		RawCommand  string   `json:"rawCommand"`
		AgentID     string   `json:"agentId"`
		SessionKey  string   `json:"sessionKey"`
	} `json:"plan"`
}

type decodedPreparedPlan struct {
	Argv        []string
	CWD         string
	CommandText string
	AgentID     string
	SessionKey  string
}

func decodeInvokePlan(result spec.FleetInvokeResponse) (decodedPreparedPlan, error) {
	raw, err := payloadToJSON(result.Payload, result.PayloadJSON)
	if err != nil {
		return decodedPreparedPlan{}, err
	}
	var payload preparedPlan
	if err := json.Unmarshal(raw, &payload); err != nil {
		return decodedPreparedPlan{}, fmt.Errorf("decode system.run.prepare payload: %w", err)
	}
	if len(payload.Plan.Argv) == 0 {
		return decodedPreparedPlan{}, errors.New("system.run.prepare returned an empty argv")
	}
	return decodedPreparedPlan{
		Argv:        payload.Plan.Argv,
		CWD:         payload.Plan.CWD,
		CommandText: firstNonEmpty(payload.Plan.CommandText, payload.Plan.RawCommand, payload.CmdText),
		AgentID:     payload.Plan.AgentID,
		SessionKey:  payload.Plan.SessionKey,
	}, nil
}

func payloadAsMap(payload any, payloadJSON string) (map[string]any, error) {
	if payload == nil && strings.TrimSpace(payloadJSON) == "" {
		return map[string]any{}, nil
	}
	if payload != nil {
		if object, ok := payload.(map[string]any); ok {
			return object, nil
		}
		return map[string]any{"payload": payload}, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func payloadToJSON(payload any, payloadJSON string) ([]byte, error) {
	if payload != nil {
		return json.Marshal(payload)
	}
	if strings.TrimSpace(payloadJSON) == "" {
		return nil, errors.New("invoke payload is empty")
	}
	return []byte(payloadJSON), nil
}

func errorFromInvokeResponse(method string, response spec.FleetInvokeResponse) error {
	result, err := payloadAsMap(response.Payload, response.PayloadJSON)
	if err == nil {
		if value, ok := result["error"].(map[string]any); ok {
			code := asString(value["code"])
			message := firstNonEmpty(asString(value["message"]), code)
			switch {
			case strings.EqualFold(code, "SYSTEM_RUN_DENIED"), strings.Contains(strings.ToLower(message), "approval required"):
				return fmt.Errorf("%w: %s", fleetsvc.ErrApprovalRequired, message)
			case strings.Contains(strings.ToLower(message), "node not connected"):
				return fmt.Errorf("%w: %s", fleetsvc.ErrNodeOffline, message)
			}
			if message != "" {
				return fmt.Errorf("%s failed: %s", method, message)
			}
		}
	}
	return fmt.Errorf("%s failed", method)
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}
