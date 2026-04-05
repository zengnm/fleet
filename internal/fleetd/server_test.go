package fleetd

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"

	"fleetd/pkg/spec"
)

func TestFleetdEndToEnd(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	server, baseURL, wsURL := newTestFleetd(t, Config{
		StoreDSN:        "file:" + filepath.Join(t.TempDir(), "fleetd.db") + "?_pragma=busy_timeout(5000)",
		MasterKey:       "fleetd-master-key",
		AuthMode:        "hs256",
		JWTHS256Secret:  "fleet-ui-secret",
		RuntimeAuthMode: "api_key",
		APIKey:          "runtime-key",
		GatewayToken:    "node-bootstrap-token",
		TickInterval:    20 * time.Millisecond,
		RequestTimeout:  2 * time.Second,
	})

	node := connectTestNode(t, wsURL, testNodeOptions{
		DisplayName: "Headless Node",
		Token:       "node-bootstrap-token",
	})
	defer node.Close()

	claims, err := server.fleet.ListClaims(ctx)
	if err != nil {
		t.Fatalf("list claims: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("expected 1 pending claim, got %d", len(claims))
	}
	claim := claims[0]
	pageReq, err := http.NewRequest(http.MethodGet, baseURL+"/fleet/claims", nil)
	if err != nil {
		t.Fatalf("new claims page request: %v", err)
	}
	pageReq.Header.Set("Authorization", "Bearer "+signedJWT(t, "fleet-ui-secret", "user-a"))
	pageRes, err := http.DefaultClient.Do(pageReq)
	if err != nil {
		t.Fatalf("claims page request: %v", err)
	}
	defer pageRes.Body.Close()
	if pageRes.StatusCode != http.StatusOK {
		t.Fatalf("claims page status = %d", pageRes.StatusCode)
	}
	pageBody := new(bytes.Buffer)
	if _, err := pageBody.ReadFrom(pageRes.Body); err != nil {
		t.Fatalf("read claims page: %v", err)
	}
	if !strings.Contains(pageBody.String(), claim.DeviceID) || !strings.Contains(pageBody.String(), "输入设备 ID 后 6 位") {
		t.Fatalf("claims page did not render the pending claim")
	}

	deviceSuffix := claim.DeviceID
	if len(deviceSuffix) > 6 {
		deviceSuffix = deviceSuffix[len(deviceSuffix)-6:]
	}
	if _, _, err := server.fleet.ApproveClaim(ctx, "user-a", claim.PairingID, deviceSuffix); err != nil {
		t.Fatalf("approve claim: %v", err)
	}

	nodes := runtimeListNodes(t, baseURL, "runtime-key", "user-a")
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node for user-a, got %d", len(nodes))
	}
	if nodes[0].NodeID != node.DeviceID {
		t.Fatalf("unexpected node id: %s", nodes[0].NodeID)
	}

	otherNodes := runtimeListNodes(t, baseURL, "runtime-key", "user-b")
	if len(otherNodes) != 0 {
		t.Fatalf("expected 0 nodes for user-b, got %d", len(otherNodes))
	}

	invokeResult := runtimeInvokeNode(t, baseURL, "runtime-key", "user-a", node.DeviceID, map[string]any{
		"command": "system.which",
		"params":  map[string]any{"name": "git"},
	})
	if !invokeResult.OK {
		t.Fatalf("expected invoke ok=true, got %+v", invokeResult)
	}

	runResult := runtimeRunNode(t, baseURL, "runtime-key", "user-a", node.DeviceID, map[string]any{
		"command": []string{"echo", "hello"},
	})
	stdout, _ := runResult.Result["stdout"].(string)
	if stdout != "hello\n" {
		t.Fatalf("unexpected run stdout: %#v", runResult.Result)
	}

	node.Close()
	waitForNodeOffline(t, baseURL, "runtime-key", "user-a", node.DeviceID)
	runtimeInvokeNodeError(t, baseURL, "runtime-key", "user-a", node.DeviceID, map[string]any{
		"command": "system.which",
		"params":  map[string]any{"name": "git"},
	}, http.StatusConflict, "node_offline")

	reconnected := connectTestNode(t, wsURL, testNodeOptions{
		Identity:    node.Identity,
		DisplayName: "Reconnected Node",
		DeviceToken: node.DeviceToken,
	})
	defer reconnected.Close()
	if reconnected.DeviceID != node.DeviceID {
		t.Fatalf("device id changed on reconnect: %s -> %s", node.DeviceID, reconnected.DeviceID)
	}
	waitForNodeOnline(t, baseURL, "runtime-key", "user-a", node.DeviceID, "Reconnected Node")
	claims, err = server.fleet.ListClaims(ctx)
	if err != nil {
		t.Fatalf("list claims after reconnect: %v", err)
	}
	if len(claims) != 0 {
		t.Fatalf("expected 0 pending claims after reconnect, got %d", len(claims))
	}
}

func TestFleetdRejectsInvalidCredentials(t *testing.T) {
	t.Parallel()

	_, _, wsURL := newTestFleetd(t, Config{
		StoreDSN:        "file:" + filepath.Join(t.TempDir(), "fleetd.db") + "?_pragma=busy_timeout(5000)",
		MasterKey:       "fleetd-master-key",
		GatewayToken:    "expected-token",
		TickInterval:    20 * time.Millisecond,
		RequestTimeout:  2 * time.Second,
		RuntimeAuthMode: "disabled",
	})

	_, err := connectTestNodeExpectFailure(wsURL, testNodeOptions{
		DisplayName: "Bad Token Node",
		Token:       "wrong-token",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid node credentials") {
		t.Fatalf("expected invalid credentials error, got %v", err)
	}

	node := connectTestNode(t, wsURL, testNodeOptions{
		DisplayName: "Good Node",
		Token:       "expected-token",
	})
	defer node.Close()

	_, err = connectTestNodeExpectFailure(wsURL, testNodeOptions{
		Identity:    node.Identity,
		DisplayName: "Bad Device Token",
		DeviceToken: "bogus-device-token",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid device token") && !strings.Contains(err.Error(), "device token mismatch") {
		t.Fatalf("expected invalid device token error, got %v", err)
	}
}

func TestFleetdAcceptsWebSocketOnRootPath(t *testing.T) {
	t.Parallel()

	_, baseURL, _ := newTestFleetd(t, Config{
		StoreDSN:       "file:" + filepath.Join(t.TempDir(), "fleetd.db") + "?_pragma=busy_timeout(5000)",
		MasterKey:      "fleetd-master-key",
		GatewayToken:   "root-path-token",
		TickInterval:   20 * time.Millisecond,
		RequestTimeout: 2 * time.Second,
	})

	wsRootURL := "ws" + strings.TrimPrefix(baseURL, "http")
	node := connectTestNode(t, wsRootURL, testNodeOptions{
		DisplayName: "Root Path Node",
		Token:       "root-path-token",
	})
	defer node.Close()

	if node.DeviceID == "" {
		t.Fatalf("expected device id after connecting on root path")
	}
}

func newTestFleetd(t *testing.T, cfg Config) (*Server, string, string) {
	t.Helper()
	cfg.ListenAddr = ""
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://127.0.0.1"
	}
	server, err := NewServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	return server, httpServer.URL, wsURL
}

type testIdentity struct {
	Public  ed25519.PublicKey
	Private ed25519.PrivateKey
}

type testNodeOptions struct {
	Identity    *testIdentity
	DisplayName string
	Token       string
	DeviceToken string
}

type testNodeClient struct {
	t           *testing.T
	conn        *websocket.Conn
	Identity    *testIdentity
	DeviceID    string
	DeviceToken string

	closed chan struct{}
	once   sync.Once
}

func connectTestNode(t *testing.T, wsURL string, opts testNodeOptions) *testNodeClient {
	t.Helper()
	node, err := connectNode(wsURL, opts)
	if err != nil {
		t.Fatalf("connect node: %v", err)
	}
	return node
}

func connectTestNodeExpectFailure(wsURL string, opts testNodeOptions) (*testNodeClient, error) {
	return connectNode(wsURL, opts)
}

func connectNode(wsURL string, opts testNodeOptions) (*testNodeClient, error) {
	if opts.Identity == nil {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		opts.Identity = &testIdentity{Public: pub, Private: priv}
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, err
	}
	_, rawChallenge, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	var challenge wsFrame
	if err := json.Unmarshal(rawChallenge, &challenge); err != nil {
		_ = conn.Close()
		return nil, err
	}
	var challengePayload struct {
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(challenge.Payload, &challengePayload); err != nil {
		_ = conn.Close()
		return nil, err
	}
	publicKey := base64.RawURLEncoding.EncodeToString(opts.Identity.Public)
	deviceID := deriveDeviceID(opts.Identity.Public)
	params := connectParams{
		MinProtocol: fleetProtocolVersion,
		MaxProtocol: fleetProtocolVersion,
		Commands:    []string{"system.run.prepare", "system.run", "system.which"},
		Permissions: map[string]bool{"exec": true},
		PathEnv:     "/usr/bin:/bin",
		Role:        "node",
	}
	params.Client.ID = "node-host"
	params.Client.DisplayName = opts.DisplayName
	params.Client.Version = "test"
	params.Client.Platform = "darwin"
	params.Client.DeviceFamily = "desktop"
	params.Client.Mode = "node"
	params.Device = &struct {
		ID        string `json:"id"`
		PublicKey string `json:"publicKey"`
		Signature string `json:"signature"`
		SignedAt  int64  `json:"signedAt"`
		Nonce     string `json:"nonce"`
	}{
		ID:        deviceID,
		PublicKey: publicKey,
		SignedAt:  time.Now().UnixMilli(),
		Nonce:     challengePayload.Nonce,
	}
	params.Auth = &struct {
		Token       string `json:"token,omitempty"`
		Bootstrap   string `json:"bootstrapToken,omitempty"`
		DeviceToken string `json:"deviceToken,omitempty"`
		Password    string `json:"password,omitempty"`
	}{}
	signatureToken := ""
	switch {
	case strings.TrimSpace(opts.DeviceToken) != "":
		params.Auth.DeviceToken = opts.DeviceToken
		signatureToken = opts.DeviceToken
	case strings.TrimSpace(opts.Token) != "":
		params.Auth.Token = opts.Token
		signatureToken = opts.Token
	}
	payload := buildDeviceAuthPayloadV3(deviceAuthPayloadParams{
		DeviceID:     deviceID,
		ClientID:     params.Client.ID,
		ClientMode:   params.Client.Mode,
		Role:         params.Role,
		Scopes:       params.Scopes,
		SignedAtMs:   params.Device.SignedAt,
		Token:        signatureToken,
		Nonce:        params.Device.Nonce,
		Platform:     params.Client.Platform,
		DeviceFamily: params.Client.DeviceFamily,
	})
	params.Device.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(opts.Identity.Private, []byte(payload)))
	if err := conn.WriteJSON(wsFrame{
		Type:   "req",
		ID:     "connect-1",
		Method: "connect",
		Params: mustRawJSON(params),
	}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_, rawResponse, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	var response wsFrame
	if err := json.Unmarshal(rawResponse, &response); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !response.OK {
		if response.Error != nil {
			_ = conn.Close()
			return nil, errors.New(response.Error.Message)
		}
		_ = conn.Close()
		return nil, errors.New("connect failed")
	}
	var hello helloOK
	if err := json.Unmarshal(response.Payload, &hello); err != nil {
		_ = conn.Close()
		return nil, err
	}
	client := &testNodeClient{
		t:           nil,
		conn:        conn,
		Identity:    opts.Identity,
		DeviceID:    deviceID,
		DeviceToken: hello.Auth.DeviceToken,
		closed:      make(chan struct{}),
	}
	go client.loop()
	return client, nil
}

func (c *testNodeClient) loop() {
	defer c.Close()
	for {
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var frame wsFrame
		if err := json.Unmarshal(payload, &frame); err != nil {
			return
		}
		if frame.Type != "event" || frame.Event != "node.invoke.request" {
			continue
		}
		var invoke nodeInvokeRequestEvent
		if err := json.Unmarshal(frame.Payload, &invoke); err != nil {
			return
		}
		result := nodeInvokeResult{
			ID:     invoke.ID,
			NodeID: invoke.NodeID,
			OK:     true,
		}
		switch invoke.Command {
		case "system.run.prepare":
			result.Payload = map[string]any{
				"plan": map[string]any{
					"argv":        []string{"/bin/sh", "-lc", "echo hello"},
					"cwd":         "/tmp",
					"commandText": "echo hello",
				},
			}
		case "system.run":
			result.Payload = map[string]any{
				"stdout":   "hello\n",
				"stderr":   "",
				"exitCode": 0,
			}
		default:
			result.Payload = map[string]any{"resolved": true}
		}
		_ = c.conn.WriteJSON(wsFrame{
			Type:   "req",
			ID:     "invoke-result-" + invoke.ID,
			Method: "node.invoke.result",
			Params: mustRawJSON(result),
		})
	}
}

func (c *testNodeClient) Close() {
	c.once.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
	})
}

func signedJWT(t *testing.T, secret, subject string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": subject})
	value, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return value
}

func runtimeListNodes(t *testing.T, baseURL, apiKey, userID string) []spec.FleetOwnedNode {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/runtime/fleet/nodes", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("API_KEY", apiKey)
	req.Header.Set("USER_ID", userID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list nodes status = %d", res.StatusCode)
	}
	var payload struct {
		Nodes []spec.FleetOwnedNode `json:"nodes"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode list nodes: %v", err)
	}
	return payload.Nodes
}

func runtimeDescribeNode(t *testing.T, baseURL, apiKey, userID, nodeID string) spec.FleetOwnedNode {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/runtime/fleet/nodes/"+nodeID, nil)
	if err != nil {
		t.Fatalf("new describe request: %v", err)
	}
	req.Header.Set("API_KEY", apiKey)
	req.Header.Set("USER_ID", userID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("describe node: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("describe status = %d", res.StatusCode)
	}
	var payload struct {
		Node spec.FleetOwnedNode `json:"node"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode describe response: %v", err)
	}
	return payload.Node
}

func waitForNodeOffline(t *testing.T, baseURL, apiKey, userID, nodeID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		nodes := runtimeListNodes(t, baseURL, apiKey, userID)
		for _, node := range nodes {
			if node.NodeID != nodeID {
				continue
			}
			if node.Status == "offline" && !node.Connected {
				described := runtimeDescribeNode(t, baseURL, apiKey, userID, nodeID)
				if described.Status == "offline" && !described.Connected {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("node %s did not transition to offline", nodeID)
}

func waitForNodeOnline(t *testing.T, baseURL, apiKey, userID, nodeID, displayName string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		described := runtimeDescribeNode(t, baseURL, apiKey, userID, nodeID)
		if described.Status == "online" && described.Connected && described.DisplayName == displayName {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("node %s did not transition to online with display name %q", nodeID, displayName)
}

func runtimeInvokeNode(t *testing.T, baseURL, apiKey, userID, nodeID string, body map[string]any) spec.FleetInvokeResponse {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/runtime/fleet/nodes/"+nodeID+"/invoke", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new invoke request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("API_KEY", apiKey)
	req.Header.Set("USER_ID", userID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("invoke node: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("invoke status = %d", res.StatusCode)
	}
	var payload struct {
		Result spec.FleetInvokeResponse `json:"result"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode invoke response: %v", err)
	}
	return payload.Result
}

func runtimeInvokeNodeError(t *testing.T, baseURL, apiKey, userID, nodeID string, body map[string]any, wantStatus int, wantCode string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/runtime/fleet/nodes/"+nodeID+"/invoke", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new invoke request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("API_KEY", apiKey)
	req.Header.Set("USER_ID", userID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("invoke node: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != wantStatus {
		t.Fatalf("invoke status = %d, want %d", res.StatusCode, wantStatus)
	}
	var payload spec.Envelope
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode invoke error response: %v", err)
	}
	if payload.Error == nil || payload.Error.Code != wantCode {
		t.Fatalf("unexpected invoke error payload: %+v", payload)
	}
}

func runtimeRunNode(t *testing.T, baseURL, apiKey, userID, nodeID string, body map[string]any) spec.FleetRunResponse {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/runtime/fleet/nodes/"+nodeID+"/run", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new run request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("API_KEY", apiKey)
	req.Header.Set("USER_ID", userID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("run node: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("run status = %d", res.StatusCode)
	}
	var payload struct {
		Result spec.FleetRunResponse `json:"result"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	return payload.Result
}
