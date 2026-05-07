package fleetnode

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fleetdserver "fleetd/internal/fleetd"
	"fleetd/pkg/spec"
)

func TestConfigEnvAndFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := MergeConfig(ConfigFromEnv(func(key string) string {
		values := map[string]string{
			"FLEETN_SERVER_URL":              "http://127.0.0.1:8090",
			"FLEETN_GATEWAY_TOKEN":           "token-a",
			"FLEETN_DISPLAY_NAME":            "Env Node",
			"FLEETN_GATEWAY_PASSWORD":        "",
			"FLEETN_BROWSER_PROXY_URL":       "http://127.0.0.1:9222",
			"FLEETN_BROWSER_EXECUTABLE_PATH": filepath.Join(dir, "chrome"),
			"FLEETN_BROWSER_HEADLESS":        "false",
		}
		return values[key]
	}), Config{DisplayName: "Flag Node", IdentityPath: filepath.Join(dir, "identity.json")})
	if err := SaveConfigFile(path, cfg); err != nil {
		t.Fatalf("SaveConfigFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("config mode = %o, want 600", mode)
	}
	loaded, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("LoadConfigFile: %v", err)
	}
	if loaded.ServerURL != "http://127.0.0.1:8090" || loaded.GatewayToken != "token-a" || loaded.DisplayName != "Flag Node" || loaded.BrowserProxyURL != "http://127.0.0.1:9222" || loaded.BrowserExecutablePath != filepath.Join(dir, "chrome") || loaded.BrowserHeadless {
		t.Fatalf("unexpected loaded config: %+v", loaded)
	}
}

func TestConfigDefaultsBrowserHeadless(t *testing.T) {
	t.Parallel()

	cfg, err := normalizeConfig(Config{ServerURL: "http://127.0.0.1:8090", DisplayName: "Node"})
	if err != nil {
		t.Fatalf("normalizeConfig: %v", err)
	}
	if !cfg.BrowserHeadless || !cfg.BrowserHeadlessSet {
		t.Fatalf("expected browser headless default true: %+v", cfg)
	}
}

func TestRegisterConfigPreservesExistingBrowserHeadless(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	existing := Config{
		ServerURL:             "http://127.0.0.1:8090",
		GatewayToken:          "token-a",
		DisplayName:           "Old Node",
		IdentityPath:          filepath.Join(dir, "identity.json"),
		ApprovalsPath:         filepath.Join(dir, "approvals.json"),
		BrowserProxyURL:       "http://127.0.0.1:9222",
		BrowserExecutablePath: filepath.Join(dir, "chrome"),
		BrowserHeadless:       false,
		BrowserHeadlessSet:    true,
	}
	if err := SaveConfigFile(path, existing); err != nil {
		t.Fatalf("SaveConfigFile: %v", err)
	}

	cfg, err := registerConfig(registerOptions{
		configPath:  path,
		serverURL:   "http://127.0.0.1:8091",
		token:       "token-b",
		displayName: "Build Node",
	}, Config{})
	if err != nil {
		t.Fatalf("registerConfig: %v", err)
	}
	if cfg.BrowserHeadless {
		t.Fatalf("registerConfig reset browser headless: %+v", cfg)
	}
	if cfg.ServerURL != "http://127.0.0.1:8091" || cfg.GatewayToken != "token-b" || cfg.DisplayName != "Build Node" {
		t.Fatalf("registerConfig did not apply CLI fields: %+v", cfg)
	}
	if cfg.IdentityPath != existing.IdentityPath || cfg.ApprovalsPath != existing.ApprovalsPath || cfg.BrowserProxyURL != existing.BrowserProxyURL || cfg.BrowserExecutablePath != existing.BrowserExecutablePath {
		t.Fatalf("registerConfig did not preserve existing fields: %+v", cfg)
	}

	envOverride, err := registerConfig(registerOptions{configPath: path}, Config{BrowserHeadless: true, BrowserHeadlessSet: true})
	if err != nil {
		t.Fatalf("registerConfig env override: %v", err)
	}
	if !envOverride.BrowserHeadless {
		t.Fatalf("expected env to override browser headless: %+v", envOverride)
	}

	cliOverride, err := registerConfig(registerOptions{configPath: path, browserHeadless: "false"}, Config{BrowserHeadless: true, BrowserHeadlessSet: true})
	if err != nil {
		t.Fatalf("registerConfig CLI override: %v", err)
	}
	if cliOverride.BrowserHeadless {
		t.Fatalf("expected CLI to override browser headless: %+v", cliOverride)
	}
}

func TestIdentityGenerateSignAndPermissions(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "identity.json")
	identity, err := LoadOrCreateIdentity(path)
	if err != nil {
		t.Fatalf("LoadOrCreateIdentity: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat identity: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("identity mode = %o, want 600", mode)
	}
	publicKey, err := parsePublicKeyPEM(identity.PublicKeyPEM)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	sum := sha256.Sum256(publicKey)
	if got := hex.EncodeToString(sum[:]); got != identity.DeviceID {
		t.Fatalf("device id = %s, want %s", identity.DeviceID, got)
	}
	signature, encodedPublic, err := identity.Sign("payload")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	rawSig, _ := base64.RawURLEncoding.DecodeString(signature)
	rawPub, _ := base64.RawURLEncoding.DecodeString(encodedPublic)
	if !ed25519.Verify(ed25519.PublicKey(rawPub), []byte("payload"), rawSig) {
		t.Fatalf("signature did not verify")
	}
}

func TestSystemCommands(t *testing.T) {
	t.Parallel()

	which, err := systemWhich(map[string]any{"name": "sh"})
	if err != nil {
		t.Fatalf("systemWhich: %v", err)
	}
	bins := which["bins"].(map[string]string)
	if strings.TrimSpace(bins["sh"]) == "" {
		t.Fatalf("expected sh in bins: %+v", bins)
	}

	prepare, err := systemRunPrepare(map[string]any{"command": []any{"echo", "hello"}, "cwd": "/tmp"})
	if err != nil {
		t.Fatalf("systemRunPrepare: %v", err)
	}
	plan := prepare["plan"].(map[string]any)
	if got := strings.Join(plan["argv"].([]string), " "); got != "echo hello" {
		t.Fatalf("argv = %q", got)
	}

	dir := t.TempDir()
	cfg := Config{ApprovalsPath: filepath.Join(dir, "approvals.json")}
	shPath, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("LookPath sh: %v", err)
	}
	if err := writeApprovalsFile(cfg.ApprovalsPath, approvalsFile{Agents: map[string]approvalAgent{
		defaultApprovalAgent: {Allowlist: []approvalEntry{{Pattern: shPath}}},
	}}); err != nil {
		t.Fatalf("writeApprovalsFile: %v", err)
	}
	run, err := systemRun(context.Background(), cfg, map[string]any{
		"command": []any{"sh", "-c", "printf '%s:%s' \"$FLEETN_TEST\" \"$PWD\""},
		"cwd":     dir,
		"env":     map[string]any{"FLEETN_TEST": "ok"},
	}, 5*time.Second)
	if err != nil {
		t.Fatalf("systemRun: %v", err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if run["stdout"] != "ok:"+realDir || run["exitCode"] != 0 || run["success"] != true {
		t.Fatalf("unexpected run payload: %+v", run)
	}

	failed, err := systemRun(context.Background(), cfg, map[string]any{"command": []any{"sh", "-c", "exit 7"}}, 5*time.Second)
	if err != nil {
		t.Fatalf("systemRun nonzero: %v", err)
	}
	if failed["exitCode"] != 7 || failed["success"] != false {
		t.Fatalf("unexpected failed payload: %+v", failed)
	}

	timedOut, err := systemRun(context.Background(), cfg, map[string]any{"command": []any{"sh", "-c", "sleep 1"}}, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("systemRun timeout: %v", err)
	}
	if timedOut["timedOut"] != true || timedOut["success"] != false {
		t.Fatalf("unexpected timeout payload: %+v", timedOut)
	}

	_, err = systemRun(context.Background(), Config{ApprovalsPath: filepath.Join(dir, "missing.json")}, map[string]any{"command": []any{"sh", "-c", "true"}}, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "approval required") {
		t.Fatalf("expected approval required error, got %v", err)
	}
}

func TestExecApprovalsGetSet(t *testing.T) {
	t.Parallel()

	cfg := Config{ApprovalsPath: filepath.Join(t.TempDir(), "exec-approvals.json")}
	initial, err := execApprovalsGet(cfg)
	if err != nil {
		t.Fatalf("execApprovalsGet: %v", err)
	}
	if initial["exists"] != false {
		t.Fatalf("expected missing approvals file: %+v", initial)
	}
	updated, err := execApprovalsSet(cfg, map[string]any{"patterns": []any{"/bin/sh", "/usr/bin/env"}})
	if err != nil {
		t.Fatalf("execApprovalsSet: %v", err)
	}
	if updated["exists"] != true || strings.TrimSpace(updated["hash"].(string)) == "" {
		t.Fatalf("unexpected updated approvals: %+v", updated)
	}
	file := updated["file"].(map[string]any)
	agents := file["agents"].(map[string]any)
	if _, ok := agents[defaultApprovalAgent]; !ok {
		t.Fatalf("missing default agent in approvals: %+v", updated)
	}
}

func TestCommandTimeoutUsesParamsTimeout(t *testing.T) {
	t.Parallel()

	got := commandTimeout(2*time.Second, 1000, map[string]any{"timeoutMs": float64(20000)})
	if got != 20*time.Second {
		t.Fatalf("timeout = %s, want 20s", got)
	}
}

func TestServiceRenderers(t *testing.T) {
	t.Parallel()

	plist, err := RenderLaunchAgent("/usr/local/bin/fleetn", "/Users/me/.fleetn/config.json")
	if err != nil {
		t.Fatalf("RenderLaunchAgent: %v", err)
	}
	for _, expected := range []string{"com.fleetn.agent", "<string>run</string>", "<string>--config</string>"} {
		if !strings.Contains(plist, expected) {
			t.Fatalf("plist missing %q:\n%s", expected, plist)
		}
	}
	unit, err := RenderSystemdUserUnit("/usr/local/bin/fleetn", "/home/me/.fleetn/config.json")
	if err != nil {
		t.Fatalf("RenderSystemdUserUnit: %v", err)
	}
	if !strings.Contains(unit, "ExecStart=/usr/local/bin/fleetn run --config /home/me/.fleetn/config.json") {
		t.Fatalf("unexpected unit:\n%s", unit)
	}
}

func TestBrowserProxyForwardsRequests(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/tabs/open" || r.URL.Query().Get("targetId") != "tab-1" {
			t.Fatalf("unexpected URL: %s", r.URL.String())
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["url"] != "https://example.com" {
			t.Fatalf("unexpected body: %+v", body)
		}
		_, _ = w.Write([]byte(`{"ok":true,"targetId":"tab-1"}`))
	}))
	defer server.Close()

	payload, err := browserProxy(context.Background(), Config{BrowserProxyURL: server.URL}, map[string]any{
		"method": "POST",
		"path":   "/tabs/open",
		"query":  map[string]any{"targetId": "tab-1"},
		"body":   map[string]any{"url": "https://example.com"},
	}, 2*time.Second)
	if err != nil {
		t.Fatalf("browserProxy: %v", err)
	}
	object := payload.(map[string]any)
	result := object["result"].(map[string]any)
	if result["ok"] != true || result["targetId"] != "tab-1" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestResolveBrowserExecutablePath(t *testing.T) {
	t.Parallel()

	executablePath := filepath.Join(t.TempDir(), "chrome")
	if err := os.WriteFile(executablePath, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	resolved, err := resolveBrowserExecutablePath(Config{BrowserExecutablePath: executablePath})
	if err != nil {
		t.Fatalf("resolveBrowserExecutablePath: %v", err)
	}
	if resolved != executablePath {
		t.Fatalf("resolved = %q, want %q", resolved, executablePath)
	}
}

func TestBuildConnectParamsAdvertisesNativeBrowser(t *testing.T) {
	t.Parallel()

	executablePath := filepath.Join(t.TempDir(), "chrome")
	if err := os.WriteFile(executablePath, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	identity, err := LoadOrCreateIdentity(filepath.Join(t.TempDir(), "identity.json"))
	if err != nil {
		t.Fatalf("LoadOrCreateIdentity: %v", err)
	}
	params, err := buildConnectParams(Config{
		DisplayName:           "Browser Node",
		BrowserExecutablePath: executablePath,
	}, identity, "nonce")
	if err != nil {
		t.Fatalf("buildConnectParams: %v", err)
	}
	if !containsString(params.Caps, "browser") || !containsString(params.Commands, "browser.proxy") {
		t.Fatalf("browser proxy not advertised: caps=%v commands=%v", params.Caps, params.Commands)
	}
}

func TestFleetnEndToEndWithFleetd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	server, err := fleetdserver.NewServer(ctx, fleetdserver.Config{
		StoreDSN:        "file:" + filepath.Join(t.TempDir(), "fleetd.db") + "?_pragma=busy_timeout(5000)",
		MasterKey:       "fleetd-master-key",
		GatewayToken:    "node-bootstrap-token",
		RuntimeAuthMode: "api_key",
		APIKey:          "runtime-key",
		TickInterval:    20 * time.Millisecond,
		RequestTimeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	dir := t.TempDir()
	cfg := Config{
		ServerURL:      httpServer.URL,
		GatewayToken:   "node-bootstrap-token",
		DisplayName:    "fleetn test node",
		IdentityPath:   filepath.Join(dir, "identity.json"),
		ApprovalsPath:  filepath.Join(dir, "exec-approvals.json"),
		ReconnectDelay: 20 * time.Millisecond,
		CommandTimeout: 2 * time.Second,
	}
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connected := make(chan ConnectedInfo, 1)
	go func() {
		_ = Run(runCtx, cfg, func(info ConnectedInfo) {
			select {
			case connected <- info:
			default:
			}
		})
	}()
	var info ConnectedInfo
	select {
	case info = <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("fleetn did not connect")
	}
	claimID := "claim-" + sha256Hex(info.DeviceID)
	approveForm := url.Values{"device_id_suffix": {strings.ToUpper(info.DeviceID)}}
	approveReq, err := http.NewRequest(http.MethodPost, httpServer.URL+"/fleet/claims/"+claimID+"/approve", strings.NewReader(approveForm.Encode()))
	if err != nil {
		t.Fatalf("new approve request: %v", err)
	}
	approveReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := newNoRedirectClient().Do(approveReq)
	if err != nil {
		t.Fatalf("approve request: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("approve status = %d", res.StatusCode)
	}

	nodes := runtimeListNodes(t, httpServer.URL, "runtime-key", "anonymous")
	if len(nodes) != 1 {
		t.Fatalf("expected one node, got %+v", nodes)
	}
	nodeID := nodes[0].NodeID
	invoke := runtimeInvokeNode(t, httpServer.URL, "runtime-key", "anonymous", nodeID, map[string]any{
		"command": "system.which",
		"params":  map[string]any{"name": "sh"},
	})
	if !invoke.OK {
		t.Fatalf("system.which failed: %+v", invoke)
	}
	setApprovals := runtimeInvokeNode(t, httpServer.URL, "runtime-key", "anonymous", nodeID, map[string]any{
		"command": "system.execApprovals.set",
		"params":  map[string]any{"patterns": []string{"sh"}},
	})
	if !setApprovals.OK {
		t.Fatalf("system.execApprovals.set failed: %+v", setApprovals)
	}
	run := runtimeRunNode(t, httpServer.URL, "runtime-key", "anonymous", nodeID, map[string]any{
		"command": []string{"sh", "-c", "printf fleetn"},
	})
	if run.Result["stdout"] != "fleetn" {
		t.Fatalf("unexpected run result: %+v", run.Result)
	}

	cancel()
	identity, err := LoadOrCreateIdentity(cfg.IdentityPath)
	if err != nil {
		t.Fatalf("reload identity: %v", err)
	}
	if strings.TrimSpace(identity.DeviceToken) == "" {
		t.Fatalf("expected stored device token")
	}

	reconnectCtx, reconnectCancel := context.WithCancel(context.Background())
	defer reconnectCancel()
	reconnected := make(chan ConnectedInfo, 1)
	cfg.GatewayToken = ""
	go func() {
		_ = Run(reconnectCtx, cfg, func(info ConnectedInfo) {
			select {
			case reconnected <- info:
			default:
			}
		})
	}()
	select {
	case next := <-reconnected:
		if next.DeviceID != info.DeviceID {
			t.Fatalf("reconnected device = %s, want %s", next.DeviceID, info.DeviceID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fleetn did not reconnect with stored device token")
	}
}

func newNoRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func runtimeListNodes(t *testing.T, baseURL, apiKey, userID string) []spec.FleetOwnedNode {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/runtime/fleet/nodes", nil)
	if err != nil {
		t.Fatalf("new list request: %v", err)
	}
	req.Header.Set("API_KEY", apiKey)
	req.Header.Set("USER_ID", userID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", res.StatusCode)
	}
	var payload struct {
		Nodes []spec.FleetOwnedNode `json:"nodes"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return payload.Nodes
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
		t.Fatalf("decode invoke: %v", err)
	}
	return payload.Result
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
		t.Fatalf("decode run: %v", err)
	}
	return payload.Result
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func TestRunCLIHelp(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	if err := RunCLI(context.Background(), []string{"help"}, &stdout, ioDiscard{}); err != nil {
		t.Fatalf("RunCLI help: %v", err)
	}
	output := stdout.String()
	for _, expected := range []string{"fleetn register", "fleetn run", "FLEETN_SERVER_URL"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("help missing %q:\n%s", expected, output)
		}
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
