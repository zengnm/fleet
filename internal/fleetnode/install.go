package fleetnode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type InstallOptions struct {
	ExecutablePath string
	ConfigPath     string
	HomeDir        string
}

func InstallUserService(ctx context.Context, options InstallOptions) error {
	exe := strings.TrimSpace(options.ExecutablePath)
	if exe == "" {
		current, err := os.Executable()
		if err != nil {
			return err
		}
		exe = current
	}
	absExe, err := filepath.Abs(exe)
	if err != nil {
		return err
	}
	configPath := strings.TrimSpace(options.ConfigPath)
	if configPath == "" {
		configPath = DefaultConfigPath()
	}
	absConfig, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	home := strings.TrimSpace(options.HomeDir)
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil {
			return err
		}
	}
	switch runtime.GOOS {
	case "darwin":
		return installLaunchAgent(ctx, home, absExe, absConfig)
	case "linux":
		return installSystemdUser(ctx, home, absExe, absConfig)
	default:
		return fmt.Errorf("user service install is not supported on %s", runtime.GOOS)
	}
}

func RenderLaunchAgent(executablePath, configPath string) (string, error) {
	const plist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.fleetn.agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{ .Executable }}</string>
    <string>run</string>
    <string>--config</string>
    <string>{{ .Config }}</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/tmp/fleetn.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/fleetn.err.log</string>
</dict>
</plist>
`
	return renderTemplate(plist, executablePath, configPath)
}

func RenderSystemdUserUnit(executablePath, configPath string) (string, error) {
	const unit = `[Unit]
Description=fleetn node agent
After=network-online.target

[Service]
ExecStart={{ .Executable }} run --config {{ .Config }}
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`
	return renderTemplate(unit, systemdQuote(executablePath), systemdQuote(configPath))
}

func installLaunchAgent(ctx context.Context, home, executablePath, configPath string) error {
	payload, err := RenderLaunchAgent(executablePath, configPath)
	if err != nil {
		return err
	}
	path := launchAgentPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		return err
	}
	if err := exec.CommandContext(ctx, "launchctl", "unload", path).Run(); err != nil {
		// Unload fails when the agent is not loaded yet; ignore that case.
	}
	if err := exec.CommandContext(ctx, "launchctl", "load", path).Run(); err != nil {
		return fmt.Errorf("launch agent written to %s, but launchctl load failed: %w; run: launchctl load %s", path, err, path)
	}
	return nil
}

func installSystemdUser(ctx context.Context, home, executablePath, configPath string) error {
	payload, err := RenderSystemdUserUnit(executablePath, configPath)
	if err != nil {
		return err
	}
	path := systemdUserUnitPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		return err
	}
	if err := exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("systemd unit written to %s, but daemon-reload failed: %w; run: systemctl --user daemon-reload", path, err)
	}
	if err := exec.CommandContext(ctx, "systemctl", "--user", "enable", "--now", "fleetn.service").Run(); err != nil {
		return fmt.Errorf("systemd unit written to %s, but enable/start failed: %w; run: systemctl --user enable --now fleetn.service", path, err)
	}
	return nil
}

func UserServiceStatus(ctx context.Context, options InstallOptions) (string, error) {
	home, err := installHome(options.HomeDir)
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.CommandContext(ctx, "launchctl", "list", "com.fleetn.agent")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "stopped", nil
		}
		return strings.TrimSpace(string(output)), nil
	case "linux":
		cmd := exec.CommandContext(ctx, "systemctl", "--user", "is-active", "fleetn.service")
		output, err := cmd.CombinedOutput()
		status := strings.TrimSpace(string(output))
		if status == "" && err != nil {
			status = "inactive"
		}
		return status, nil
	default:
		_ = home
		return "", fmt.Errorf("user service status is not supported on %s", runtime.GOOS)
	}
}

func StopUserService(ctx context.Context, options InstallOptions) error {
	home, err := installHome(options.HomeDir)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		path := launchAgentPath(home)
		if err := exec.CommandContext(ctx, "launchctl", "unload", path).Run(); err != nil {
			return fmt.Errorf("launchctl unload failed: %w; run: launchctl unload %s", err, path)
		}
		return nil
	case "linux":
		if err := exec.CommandContext(ctx, "systemctl", "--user", "stop", "fleetn.service").Run(); err != nil {
			return fmt.Errorf("systemctl stop failed: %w; run: systemctl --user stop fleetn.service", err)
		}
		return nil
	default:
		return fmt.Errorf("user service stop is not supported on %s", runtime.GOOS)
	}
}

func RestartUserService(ctx context.Context, options InstallOptions) error {
	home, err := installHome(options.HomeDir)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		path := launchAgentPath(home)
		_ = exec.CommandContext(ctx, "launchctl", "unload", path).Run()
		if err := exec.CommandContext(ctx, "launchctl", "load", path).Run(); err != nil {
			return fmt.Errorf("launchctl load failed: %w; run: launchctl load %s", err, path)
		}
		return nil
	case "linux":
		if err := exec.CommandContext(ctx, "systemctl", "--user", "restart", "fleetn.service").Run(); err != nil {
			return fmt.Errorf("systemctl restart failed: %w; run: systemctl --user restart fleetn.service", err)
		}
		return nil
	default:
		return fmt.Errorf("user service restart is not supported on %s", runtime.GOOS)
	}
}

func UninstallUserService(ctx context.Context, options InstallOptions) error {
	home, err := installHome(options.HomeDir)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		path := launchAgentPath(home)
		_ = exec.CommandContext(ctx, "launchctl", "unload", path).Run()
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	case "linux":
		_ = exec.CommandContext(ctx, "systemctl", "--user", "disable", "--now", "fleetn.service").Run()
		path := systemdUserUnitPath(home)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		_ = exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run()
		return nil
	default:
		return fmt.Errorf("user service uninstall is not supported on %s", runtime.GOOS)
	}
}

func renderTemplate(text, executablePath, configPath string) (string, error) {
	if strings.TrimSpace(executablePath) == "" || strings.TrimSpace(configPath) == "" {
		return "", errors.New("executable path and config path are required")
	}
	tmpl, err := template.New("service").Parse(text)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, struct {
		Executable string
		Config     string
	}{Executable: executablePath, Config: configPath}); err != nil {
		return "", err
	}
	return out.String(), nil
}

func systemdQuote(value string) string {
	if strings.IndexFunc(value, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '"' || r == '\''
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func installHome(home string) (string, error) {
	home = strings.TrimSpace(home)
	if home != "" {
		return home, nil
	}
	return os.UserHomeDir()
}

func launchAgentPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", "com.fleetn.agent.plist")
}

func systemdUserUnitPath(home string) string {
	return filepath.Join(home, ".config", "systemd", "user", "fleetn.service")
}
