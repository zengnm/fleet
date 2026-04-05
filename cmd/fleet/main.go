package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fleetd/internal/fleetcli"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	values := loadEnv()
	client := fleetcli.New(fleetcli.Config{
		BaseURL: envOrDefault(values, "FLEET_BASE_URL", "http://127.0.0.1:8090"),
		APIKey:  envOrDefault(values, "FLEET_API_KEY", ""),
		UserID:  envOrDefault(values, "USER_ID", ""),
	})
	if err := client.Run(ctx, os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func loadEnv() map[string]string {
	values := map[string]string{}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		mergeEnvFile(values, filepath.Join(home, ".fleet", ".env"))
	}
	if wd, err := os.Getwd(); err == nil && strings.TrimSpace(wd) != "" {
		mergeEnvFile(values, filepath.Join(wd, ".env"))
	}
	for _, item := range os.Environ() {
		key, current, ok := strings.Cut(item, "=")
		if ok && strings.TrimSpace(key) != "" {
			values[key] = current
		}
	}
	return values
}

func mergeEnvFile(values map[string]string, path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, rawValue, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = parseEnvValue(strings.TrimSpace(rawValue))
	}
}

func parseEnvValue(value string) string {
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

func envOrDefault(values map[string]string, key, defaultValue string) string {
	if current := strings.TrimSpace(values[key]); current != "" {
		return current
	}
	return defaultValue
}
