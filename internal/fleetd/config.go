package fleetd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr      string
	BaseURL         string
	StoreDSN        string
	MasterKey       string
	AuthMode        string
	RuntimeAuthMode string
	APIKey          string

	JWTHS256Secret    string
	JWTRS256PublicKey string
	JWTIssuer         string
	JWTAudience       string

	GatewayToken    string
	GatewayPassword string
	TickInterval    time.Duration
	RequestTimeout  time.Duration
}

var (
	fleetUserHomeDir = os.UserHomeDir
	fleetCurrentWD   = os.Getwd
)

func LoadConfig() Config {
	values := loadFleetEnvFiles()
	mergeFleetProcessEnv(values)
	return Config{
		ListenAddr:        fleetValue(values, "FLEETD_LISTEN_ADDR", ":8090"),
		BaseURL:           fleetValue(values, "FLEETD_BASE_URL", "http://127.0.0.1:8090"),
		StoreDSN:          fleetValue(values, "FLEETD_STORE_DSN", "file:fleetd.db?_pragma=busy_timeout(5000)"),
		MasterKey:         fleetValue(values, "FLEETD_MASTER_KEY", "development-master-key-change-me"),
		AuthMode:          fleetValue(values, "FLEETD_AUTH_MODE", "disabled"),
		RuntimeAuthMode:   fleetValue(values, "FLEETD_RUNTIME_AUTH_MODE", "disabled"),
		APIKey:            fleetValue(values, "FLEETD_API_KEY", ""),
		JWTHS256Secret:    fleetValue(values, "FLEETD_JWT_HS256_SECRET", ""),
		JWTRS256PublicKey: fleetValue(values, "FLEETD_JWT_RS256_PUBLIC_KEY", ""),
		JWTIssuer:         fleetValue(values, "FLEETD_JWT_ISSUER", ""),
		JWTAudience:       fleetValue(values, "FLEETD_JWT_AUDIENCE", ""),
		GatewayToken:      fleetValue(values, "FLEETD_GATEWAY_TOKEN", ""),
		GatewayPassword:   fleetValue(values, "FLEETD_GATEWAY_PASSWORD", ""),
		TickInterval:      time.Duration(fleetValueInt(values, "FLEETD_TICK_INTERVAL_MS", 15000)) * time.Millisecond,
		RequestTimeout:    time.Duration(fleetValueInt(values, "FLEETD_REQUEST_TIMEOUT_MS", 30000)) * time.Millisecond,
	}
}

func loadFleetEnvFiles() map[string]string {
	values := map[string]string{}
	for _, path := range fleetEnvPaths() {
		fileValues, err := parseFleetEnvFile(path)
		if err != nil {
			panic(err)
		}
		for key, value := range fileValues {
			values[key] = value
		}
	}
	return values
}

func fleetEnvPaths() []string {
	var paths []string
	if home, err := fleetUserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		paths = append(paths, filepath.Join(home, ".fleet", ".env"))
	}
	if wd, err := fleetCurrentWD(); err == nil && strings.TrimSpace(wd) != "" {
		cwdPath := filepath.Join(wd, ".env")
		if len(paths) == 0 || paths[len(paths)-1] != cwdPath {
			paths = append(paths, cwdPath)
		}
	}
	return paths
}

func parseFleetEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("open env file %s: %w", path, err)
	}
	defer file.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, rawValue, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("invalid env line %s:%d", path, lineNumber)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("empty env key %s:%d", path, lineNumber)
		}
		values[key] = parseFleetEnvValue(strings.TrimSpace(rawValue))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan env file %s: %w", path, err)
	}
	return values, nil
}

func parseFleetEnvValue(value string) string {
	if len(value) >= 2 {
		if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
			return value[1 : len(value)-1]
		}
		if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func mergeFleetProcessEnv(values map[string]string) {
	for _, item := range os.Environ() {
		key, current, ok := strings.Cut(item, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		values[key] = current
	}
}

func fleetValue(values map[string]string, key, fallback string) string {
	if current, ok := values[key]; ok && current != "" {
		return current
	}
	return fallback
}

func fleetValueInt(values map[string]string, key string, fallback int) int {
	current, ok := values[key]
	if !ok || current == "" {
		return fallback
	}
	number, err := strconv.Atoi(current)
	if err != nil {
		panic(fmt.Sprintf("invalid integer for %s: %v", key, err))
	}
	return number
}
