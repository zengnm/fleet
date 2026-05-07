package fleetnode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultReconnectDelay = time.Second
	defaultCommandTimeout = 30 * time.Second
)

type Config struct {
	ServerURL             string        `json:"serverUrl"`
	GatewayToken          string        `json:"gatewayToken,omitempty"`
	GatewayPassword       string        `json:"gatewayPassword,omitempty"`
	DisplayName           string        `json:"displayName"`
	IdentityPath          string        `json:"identityPath"`
	ApprovalsPath         string        `json:"approvalsPath"`
	BrowserProxyURL       string        `json:"browserProxyUrl,omitempty"`
	BrowserExecutablePath string        `json:"browserExecutablePath,omitempty"`
	BrowserHeadless       bool          `json:"browserHeadless"`
	BrowserHeadlessSet    bool          `json:"-"`
	ReconnectDelay        time.Duration `json:"-"`
	CommandTimeout        time.Duration `json:"-"`
}

type configFile struct {
	ServerURL             string `json:"serverUrl"`
	GatewayToken          string `json:"gatewayToken,omitempty"`
	GatewayPassword       string `json:"gatewayPassword,omitempty"`
	DisplayName           string `json:"displayName"`
	IdentityPath          string `json:"identityPath"`
	ApprovalsPath         string `json:"approvalsPath"`
	BrowserProxyURL       string `json:"browserProxyUrl,omitempty"`
	BrowserExecutablePath string `json:"browserExecutablePath,omitempty"`
	BrowserHeadless       *bool  `json:"browserHeadless,omitempty"`
}

func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(os.TempDir(), "fleetn", "config.json")
	}
	return filepath.Join(home, ".fleetn", "config.json")
}

func DefaultIdentityPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(os.TempDir(), "fleetn", "identity.json")
	}
	return filepath.Join(home, ".fleetn", "identity.json")
}

func DefaultApprovalsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(os.TempDir(), "fleetn", "exec-approvals.json")
	}
	return filepath.Join(home, ".fleetn", "exec-approvals.json")
}

func LoadConfigFile(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var file configFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return Config{}, err
	}
	cfg := Config{
		ServerURL:             file.ServerURL,
		GatewayToken:          file.GatewayToken,
		GatewayPassword:       file.GatewayPassword,
		DisplayName:           file.DisplayName,
		IdentityPath:          file.IdentityPath,
		ApprovalsPath:         file.ApprovalsPath,
		BrowserProxyURL:       file.BrowserProxyURL,
		BrowserExecutablePath: file.BrowserExecutablePath,
	}
	if file.BrowserHeadless != nil {
		cfg.BrowserHeadless = *file.BrowserHeadless
		cfg.BrowserHeadlessSet = true
	}
	return normalizeConfig(cfg)
}

func SaveConfigFile(path string, cfg Config) error {
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	browserHeadless := cfg.BrowserHeadless
	payload, err := json.MarshalIndent(configFile{
		ServerURL:             cfg.ServerURL,
		GatewayToken:          cfg.GatewayToken,
		GatewayPassword:       cfg.GatewayPassword,
		DisplayName:           cfg.DisplayName,
		IdentityPath:          cfg.IdentityPath,
		ApprovalsPath:         cfg.ApprovalsPath,
		BrowserProxyURL:       cfg.BrowserProxyURL,
		BrowserExecutablePath: cfg.BrowserExecutablePath,
		BrowserHeadless:       &browserHeadless,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(payload, '\n'), 0o600)
}

func ConfigFromEnv(getenv func(string) string) Config {
	if getenv == nil {
		getenv = os.Getenv
	}
	cfg := Config{
		ServerURL:             getenv("FLEETN_SERVER_URL"),
		GatewayToken:          getenv("FLEETN_GATEWAY_TOKEN"),
		GatewayPassword:       getenv("FLEETN_GATEWAY_PASSWORD"),
		DisplayName:           getenv("FLEETN_DISPLAY_NAME"),
		BrowserProxyURL:       getenv("FLEETN_BROWSER_PROXY_URL"),
		BrowserExecutablePath: getenv("FLEETN_BROWSER_EXECUTABLE_PATH"),
	}
	if value, ok := parseOptionalBool(getenv("FLEETN_BROWSER_HEADLESS")); ok {
		cfg.BrowserHeadless = value
		cfg.BrowserHeadlessSet = true
	}
	return cfg
}

func MergeConfig(base, override Config) Config {
	if strings.TrimSpace(override.ServerURL) != "" {
		base.ServerURL = override.ServerURL
	}
	if strings.TrimSpace(override.GatewayToken) != "" {
		base.GatewayToken = override.GatewayToken
	}
	if strings.TrimSpace(override.GatewayPassword) != "" {
		base.GatewayPassword = override.GatewayPassword
	}
	if strings.TrimSpace(override.DisplayName) != "" {
		base.DisplayName = override.DisplayName
	}
	if strings.TrimSpace(override.IdentityPath) != "" {
		base.IdentityPath = override.IdentityPath
	}
	if strings.TrimSpace(override.ApprovalsPath) != "" {
		base.ApprovalsPath = override.ApprovalsPath
	}
	if strings.TrimSpace(override.BrowserProxyURL) != "" {
		base.BrowserProxyURL = override.BrowserProxyURL
	}
	if strings.TrimSpace(override.BrowserExecutablePath) != "" {
		base.BrowserExecutablePath = override.BrowserExecutablePath
	}
	if override.BrowserHeadlessSet {
		base.BrowserHeadless = override.BrowserHeadless
		base.BrowserHeadlessSet = true
	}
	if override.ReconnectDelay > 0 {
		base.ReconnectDelay = override.ReconnectDelay
	}
	if override.CommandTimeout > 0 {
		base.CommandTimeout = override.CommandTimeout
	}
	return base
}

func normalizeConfig(cfg Config) (Config, error) {
	cfg.ServerURL = strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/")
	cfg.GatewayToken = strings.TrimSpace(cfg.GatewayToken)
	cfg.GatewayPassword = strings.TrimSpace(cfg.GatewayPassword)
	cfg.DisplayName = strings.TrimSpace(cfg.DisplayName)
	cfg.IdentityPath = strings.TrimSpace(cfg.IdentityPath)
	cfg.ApprovalsPath = strings.TrimSpace(cfg.ApprovalsPath)
	cfg.BrowserProxyURL = strings.TrimRight(strings.TrimSpace(cfg.BrowserProxyURL), "/")
	cfg.BrowserExecutablePath = strings.TrimSpace(cfg.BrowserExecutablePath)
	if !cfg.BrowserHeadlessSet {
		cfg.BrowserHeadless = true
		cfg.BrowserHeadlessSet = true
	}
	if cfg.IdentityPath == "" {
		cfg.IdentityPath = DefaultIdentityPath()
	}
	if cfg.ApprovalsPath == "" {
		cfg.ApprovalsPath = DefaultApprovalsPath()
	}
	if cfg.ReconnectDelay <= 0 {
		cfg.ReconnectDelay = defaultReconnectDelay
	}
	if cfg.CommandTimeout <= 0 {
		cfg.CommandTimeout = defaultCommandTimeout
	}
	if cfg.ServerURL == "" {
		return Config{}, errors.New("server url is required")
	}
	if cfg.DisplayName == "" {
		return Config{}, errors.New("display name is required")
	}
	if cfg.GatewayToken != "" && cfg.GatewayPassword != "" {
		return Config{}, errors.New("use either token or password, not both")
	}
	return cfg, nil
}

func parseOptionalBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return false, false
	case "1", "t", "true", "y", "yes", "on":
		return true, true
	case "0", "f", "false", "n", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func printRegisterSuccess(w io.Writer, cfg Config, identity *Identity) {
	claimsURL := strings.TrimRight(cfg.ServerURL, "/") + "/fleet/claims"
	_, _ = fmt.Fprintf(w, "device_id: %s\n", identity.DeviceID)
	_, _ = fmt.Fprintf(w, "claims_url: %s\n", claimsURL)
}
