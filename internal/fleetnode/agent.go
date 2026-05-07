package fleetnode

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const protocolVersion = 3

type ConnectedInfo struct {
	DeviceID  string
	ServerURL string
}

type ConnectedFunc func(ConnectedInfo)

type wsFrame struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	OK      bool            `json:"ok,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *wsError        `json:"error,omitempty"`
	Event   string          `json:"event,omitempty"`
}

type wsError struct {
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
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
	Auth struct {
		DeviceToken string   `json:"deviceToken"`
		Scopes      []string `json:"scopes"`
	} `json:"auth"`
}

type nodeInvokeRequestEvent struct {
	ID         string `json:"id"`
	NodeID     string `json:"nodeId"`
	Command    string `json:"command"`
	ParamsJSON string `json:"paramsJSON,omitempty"`
	TimeoutMs  int    `json:"timeoutMs,omitempty"`
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

func Run(ctx context.Context, cfg Config, onConnected ConnectedFunc) error {
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return err
	}
	identity, err := LoadOrCreateIdentity(cfg.IdentityPath)
	if err != nil {
		return err
	}
	for {
		err := runSession(ctx, cfg, identity, onConnected)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			_, _ = fmt.Fprintf(io.Discard, "%v", err)
		}
		timer := time.NewTimer(cfg.ReconnectDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func RunConfigFile(ctx context.Context, path string, onConnected ConnectedFunc) error {
	cfg, err := LoadConfigFile(path)
	if err != nil {
		return err
	}
	return Run(ctx, cfg, onConnected)
}

func runSession(ctx context.Context, cfg Config, identity *Identity, onConnected ConnectedFunc) error {
	wsURL, err := websocketURL(cfg.ServerURL)
	if err != nil {
		return err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	sessionCtx, stopSession := context.WithCancel(ctx)
	defer stopSession()
	go func() {
		<-sessionCtx.Done()
		_ = conn.Close()
	}()

	var challenge wsFrame
	if err := conn.ReadJSON(&challenge); err != nil {
		return err
	}
	if challenge.Type != "event" || challenge.Event != "connect.challenge" {
		return errors.New("connect.challenge is required")
	}
	var challengePayload struct {
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(challenge.Payload, &challengePayload); err != nil {
		return err
	}
	params, err := buildConnectParams(cfg, identity, challengePayload.Nonce)
	if err != nil {
		return err
	}
	connectID := randomID("connect")
	if err := conn.WriteJSON(wsFrame{
		Type:   "req",
		ID:     connectID,
		Method: "connect",
		Params: mustRawJSON(params),
	}); err != nil {
		return err
	}
	var response wsFrame
	if err := conn.ReadJSON(&response); err != nil {
		return err
	}
	if !response.OK {
		if response.Error != nil {
			return errors.New(response.Error.Message)
		}
		return errors.New("connect failed")
	}
	var hello helloOK
	if err := json.Unmarshal(response.Payload, &hello); err != nil {
		return err
	}
	if strings.TrimSpace(hello.Auth.DeviceToken) != "" {
		identity.DeviceToken = hello.Auth.DeviceToken
		identity.TokenScopes = append([]string(nil), hello.Auth.Scopes...)
		if err := WriteIdentityFile(cfg.IdentityPath, identity); err != nil {
			return err
		}
	}
	if onConnected != nil {
		onConnected(ConnectedInfo{DeviceID: identity.DeviceID, ServerURL: cfg.ServerURL})
	}
	return readLoop(sessionCtx, cfg, conn, identity)
}

func buildConnectParams(cfg Config, identity *Identity, nonce string) (connectParams, error) {
	signedAt := time.Now().UnixMilli()
	params := connectParams{
		MinProtocol: protocolVersion,
		MaxProtocol: protocolVersion,
		Caps:        []string{"system"},
		Commands:    []string{"system.which", "system.run.prepare", "system.run", "system.execApprovals.get", "system.execApprovals.set"},
		Permissions: map[string]bool{"exec": true},
		PathEnv:     os.Getenv("PATH"),
		Role:        "node",
		Scopes:      []string{},
	}
	if browserProxyAvailable(cfg) {
		params.Caps = append(params.Caps, "browser")
		params.Commands = append(params.Commands, "browser.proxy")
		params.Permissions["browser.enabled"] = true
	}
	params.Client.ID = "fleetn"
	params.Client.DisplayName = cfg.DisplayName
	params.Client.Version = "fleetn"
	params.Client.Platform = runtime.GOOS
	params.Client.DeviceFamily = "server"
	params.Client.ModelIdentifier = runtime.GOARCH
	params.Client.Mode = "node"
	params.Auth = &struct {
		Token       string `json:"token,omitempty"`
		Bootstrap   string `json:"bootstrapToken,omitempty"`
		DeviceToken string `json:"deviceToken,omitempty"`
		Password    string `json:"password,omitempty"`
	}{}
	signatureToken := ""
	switch {
	case strings.TrimSpace(identity.DeviceToken) != "":
		params.Auth.DeviceToken = identity.DeviceToken
		signatureToken = identity.DeviceToken
	case strings.TrimSpace(cfg.GatewayToken) != "":
		params.Auth.Token = cfg.GatewayToken
		signatureToken = cfg.GatewayToken
	case strings.TrimSpace(cfg.GatewayPassword) != "":
		params.Auth.Password = cfg.GatewayPassword
	}
	payload := buildDeviceAuthPayloadV3(deviceAuthPayloadParams{
		DeviceID:     identity.DeviceID,
		ClientID:     params.Client.ID,
		ClientMode:   params.Client.Mode,
		Role:         params.Role,
		Scopes:       params.Scopes,
		SignedAtMs:   signedAt,
		Token:        signatureToken,
		Nonce:        nonce,
		Platform:     params.Client.Platform,
		DeviceFamily: params.Client.DeviceFamily,
	})
	signature, publicKey, err := identity.Sign(payload)
	if err != nil {
		return connectParams{}, err
	}
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
	return params, nil
}

func readLoop(ctx context.Context, cfg Config, conn *websocket.Conn, identity *Identity) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		var frame wsFrame
		if err := conn.ReadJSON(&frame); err != nil {
			return err
		}
		if frame.Type != "event" {
			continue
		}
		switch frame.Event {
		case "node.invoke.request":
			var request nodeInvokeRequestEvent
			if err := json.Unmarshal(frame.Payload, &request); err != nil {
				return err
			}
			result := handleInvoke(ctx, cfg, identity.DeviceID, request)
			if err := conn.WriteJSON(wsFrame{
				Type:   "req",
				ID:     randomID("invoke-result"),
				Method: "node.invoke.result",
				Params: mustRawJSON(result),
			}); err != nil {
				return err
			}
		case "shutdown":
			return nil
		}
	}
}

func handleInvoke(ctx context.Context, cfg Config, nodeID string, request nodeInvokeRequestEvent) nodeInvokeResult {
	params, err := decodeParams(request.ParamsJSON)
	if err != nil {
		return invokeFailure(request, nodeID, "INVALID_PARAMS", err.Error())
	}
	payload, err := executeCommand(ctx, cfg, request, params)
	if err != nil {
		return invokeFailure(request, nodeID, "COMMAND_FAILED", err.Error())
	}
	result := nodeInvokeResult{
		ID:      request.ID,
		NodeID:  nodeID,
		OK:      true,
		Payload: payload,
	}
	return result
}

func invokeFailure(request nodeInvokeRequestEvent, nodeID, code, message string) nodeInvokeResult {
	return nodeInvokeResult{
		ID:     request.ID,
		NodeID: nodeID,
		OK:     false,
		Error: &struct {
			Code    string `json:"code,omitempty"`
			Message string `json:"message,omitempty"`
		}{Code: code, Message: message},
	}
}

func executeCommand(ctx context.Context, cfg Config, request nodeInvokeRequestEvent, params map[string]any) (any, error) {
	switch request.Command {
	case "system.which":
		return systemWhich(params)
	case "system.run.prepare":
		return systemRunPrepare(params)
	case "system.run":
		timeout := cfg.CommandTimeout
		if request.TimeoutMs > 0 {
			timeout = time.Duration(request.TimeoutMs) * time.Millisecond
		}
		return systemRun(ctx, cfg, params, timeout)
	case "system.execApprovals.get":
		return execApprovalsGet(cfg)
	case "system.execApprovals.set":
		return execApprovalsSet(cfg, params)
	case "browser.proxy":
		timeout := commandTimeout(cfg.CommandTimeout, request.TimeoutMs, params)
		return browserProxy(ctx, cfg, params, timeout)
	default:
		return nil, fmt.Errorf("unsupported command %q", request.Command)
	}
}

func commandTimeout(defaultTimeout time.Duration, requestTimeoutMs int, params map[string]any) time.Duration {
	timeout := defaultTimeout
	if requestTimeoutMs > 0 {
		timeout = time.Duration(requestTimeoutMs) * time.Millisecond
	}
	if paramsTimeout := durationFromAny(params["timeoutMs"]); paramsTimeout > timeout {
		timeout = paramsTimeout
	}
	return timeout
}

func decodeParams(paramsJSON string) (map[string]any, error) {
	params := map[string]any{}
	if strings.TrimSpace(paramsJSON) == "" {
		return params, nil
	}
	if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
		return nil, err
	}
	return params, nil
}

func systemWhich(params map[string]any) (map[string]any, error) {
	result := map[string]string{}
	if name, _ := params["name"].(string); strings.TrimSpace(name) != "" {
		if path, err := exec.LookPath(name); err == nil {
			result[name] = path
		}
	}
	for _, candidate := range stringSlice(params["bins"]) {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if filepath.IsAbs(candidate) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				result[candidate] = candidate
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			result[candidate] = path
		}
	}
	return map[string]any{"bins": result}, nil
}

func systemRunPrepare(params map[string]any) (map[string]any, error) {
	argv := stringSlice(params["command"])
	if len(argv) == 0 {
		return nil, errors.New("command is required")
	}
	rawCommand, _ := params["rawCommand"].(string)
	if strings.TrimSpace(rawCommand) == "" {
		rawCommand = shellQuote(argv)
	}
	cwd, _ := params["cwd"].(string)
	cwd = runWorkingDirectory(cwd)
	return map[string]any{
		"cmdText": rawCommand,
		"plan": map[string]any{
			"argv":        argv,
			"cwd":         cwd,
			"commandText": rawCommand,
			"rawCommand":  rawCommand,
			"agentId":     "fleetn",
			"sessionKey":  randomID("run"),
		},
	}, nil
}

func systemRun(ctx context.Context, cfg Config, params map[string]any, timeout time.Duration) (map[string]any, error) {
	argv := stringSlice(params["command"])
	if len(argv) == 0 {
		return nil, errors.New("command is required")
	}
	if err := requireRunApproval(cfg, argv); err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	if cwd := runWorkingDirectory(stringFromAny(params["cwd"])); cwd != "" {
		cmd.Dir = cwd
	}
	if env := stringMap(params["env"]); len(env) > 0 {
		cmd.Env = os.Environ()
		for key, value := range env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	timedOut := runCtx.Err() == context.DeadlineExceeded
	exitCode := 0
	success := err == nil && !timedOut
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if timedOut {
			exitCode = -1
		} else {
			return nil, err
		}
	}
	return map[string]any{
		"stdout":   stdout.String(),
		"stderr":   stderr.String(),
		"exitCode": exitCode,
		"success":  success,
		"timedOut": timedOut,
	}, nil
}

func runWorkingDirectory(requested string) string {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		return requested
	}
	if executable, err := os.Executable(); err == nil {
		if dir := strings.TrimSpace(filepath.Dir(executable)); dir != "" {
			return dir
		}
	}
	if dir, err := os.Getwd(); err == nil {
		return strings.TrimSpace(dir)
	}
	return ""
}

func websocketURL(serverURL string) (string, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported server url scheme %q", parsed.Scheme)
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String(), nil
}

func mustRawJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if ok && strings.TrimSpace(text) != "" {
				items = append(items, text)
			}
		}
		return items
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{typed}
	default:
		return nil
	}
}

func stringMap(value any) map[string]string {
	typed, ok := value.(map[string]any)
	if !ok {
		if direct, ok := value.(map[string]string); ok {
			return direct
		}
		return nil
	}
	result := make(map[string]string, len(typed))
	for key, raw := range typed {
		if text, ok := raw.(string); ok {
			result[key] = text
		}
	}
	return result
}

func stringFromAny(value any) string {
	text, _ := value.(string)
	return text
}

func shellQuote(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, item := range argv {
		if item == "" {
			parts = append(parts, "''")
			continue
		}
		if strings.IndexFunc(item, func(r rune) bool {
			return !(r == '-' || r == '_' || r == '/' || r == '.' || r == ':' || r == '=' || r == '+' || r == ',' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
		}) < 0 {
			parts = append(parts, item)
			continue
		}
		parts = append(parts, "'"+strings.ReplaceAll(item, "'", `'"'"'`)+"'")
	}
	return strings.Join(parts, " ")
}

func randomID(prefix string) string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + base64.RawURLEncoding.EncodeToString(raw[:])
}
