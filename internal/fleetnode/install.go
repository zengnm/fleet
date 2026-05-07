package fleetnode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
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
	case "windows":
		return installWindowsScheduledTask(ctx, absExe, absConfig)
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
  <key>WorkingDirectory</key>
  <string>{{ .WorkingDirectory }}</string>
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
	return renderTemplate(plist, executablePath, configPath, filepath.Dir(executablePath))
}

func RenderSystemdUserUnit(executablePath, configPath string) (string, error) {
	const unit = `[Unit]
Description=fleetn node agent
After=network-online.target

[Service]
WorkingDirectory={{ .WorkingDirectory }}
ExecStart={{ .Executable }} run --config {{ .Config }}
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`
	return renderTemplate(unit, systemdQuote(executablePath), systemdQuote(configPath), systemdQuote(filepath.Dir(executablePath)))
}

func RenderWindowsScheduledTaskCommand(executablePath, configPath string) (string, error) {
	if strings.TrimSpace(executablePath) == "" || strings.TrimSpace(configPath) == "" {
		return "", errors.New("executable path and config path are required")
	}
	return windowsCommandLine([]string{executablePath, "run", "--config", configPath}), nil
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
	domain := launchdUserDomain()
	_ = exec.CommandContext(ctx, "launchctl", "bootout", domain, path).Run()
	if output, err := exec.CommandContext(ctx, "launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
		return fmt.Errorf("launch agent written to %s, but launchctl bootstrap failed: %w: %s", path, err, strings.TrimSpace(string(output)))
	}
	if output, err := exec.CommandContext(ctx, "launchctl", "kickstart", "-k", domain+"/com.fleetn.agent").CombinedOutput(); err != nil {
		return fmt.Errorf("launch agent loaded from %s, but launchctl kickstart failed: %w: %s", path, err, strings.TrimSpace(string(output)))
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

func installWindowsScheduledTask(ctx context.Context, executablePath, configPath string) error {
	taskCommand, err := RenderWindowsScheduledTaskCommand(executablePath, configPath)
	if err != nil {
		return err
	}
	args := []string{"/Create", "/TN", windowsTaskName, "/TR", taskCommand, "/SC", "ONLOGON", "/RL", "LIMITED", "/IT", "/F"}
	if output, err := exec.CommandContext(ctx, "schtasks.exe", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("scheduled task create failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if output, err := exec.CommandContext(ctx, "schtasks.exe", "/Run", "/TN", windowsTaskName).CombinedOutput(); err != nil {
		return fmt.Errorf("scheduled task created, but start failed: %w: %s", err, strings.TrimSpace(string(output)))
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
		cmd := exec.CommandContext(ctx, "launchctl", "print", launchdUserDomain()+"/com.fleetn.agent")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "stopped", nil
		}
		return parseLaunchdStatus(string(output)), nil
	case "linux":
		cmd := exec.CommandContext(ctx, "systemctl", "--user", "is-active", "fleetn.service")
		output, err := cmd.CombinedOutput()
		status := strings.TrimSpace(string(output))
		if status == "" && err != nil {
			status = "inactive"
		}
		return normalizeSystemdStatus(status), nil
	case "windows":
		output, err := exec.CommandContext(ctx, "schtasks.exe", "/Query", "/TN", windowsTaskName, "/FO", "LIST", "/V").CombinedOutput()
		if err != nil {
			return "stopped", nil
		}
		return parseWindowsTaskStatus(string(output)), nil
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
		if output, err := exec.CommandContext(ctx, "launchctl", "bootout", launchdUserDomain(), path).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl bootout failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	case "linux":
		if err := exec.CommandContext(ctx, "systemctl", "--user", "stop", "fleetn.service").Run(); err != nil {
			return fmt.Errorf("systemctl stop failed: %w; run: systemctl --user stop fleetn.service", err)
		}
		return nil
	case "windows":
		if output, err := exec.CommandContext(ctx, "schtasks.exe", "/End", "/TN", windowsTaskName).CombinedOutput(); err != nil {
			return fmt.Errorf("scheduled task stop failed: %w: %s", err, strings.TrimSpace(string(output)))
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
		_ = exec.CommandContext(ctx, "launchctl", "bootout", launchdUserDomain(), path).Run()
		if output, err := exec.CommandContext(ctx, "launchctl", "bootstrap", launchdUserDomain(), path).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl bootstrap failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
		if output, err := exec.CommandContext(ctx, "launchctl", "kickstart", "-k", launchdUserDomain()+"/com.fleetn.agent").CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl kickstart failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	case "linux":
		if err := exec.CommandContext(ctx, "systemctl", "--user", "restart", "fleetn.service").Run(); err != nil {
			return fmt.Errorf("systemctl restart failed: %w; run: systemctl --user restart fleetn.service", err)
		}
		return nil
	case "windows":
		_ = exec.CommandContext(ctx, "schtasks.exe", "/End", "/TN", windowsTaskName).Run()
		if output, err := exec.CommandContext(ctx, "schtasks.exe", "/Run", "/TN", windowsTaskName).CombinedOutput(); err != nil {
			return fmt.Errorf("scheduled task start failed: %w: %s", err, strings.TrimSpace(string(output)))
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
		_ = exec.CommandContext(ctx, "launchctl", "bootout", launchdUserDomain(), path).Run()
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
	case "windows":
		_ = exec.CommandContext(ctx, "schtasks.exe", "/End", "/TN", windowsTaskName).Run()
		if output, err := exec.CommandContext(ctx, "schtasks.exe", "/Delete", "/TN", windowsTaskName, "/F").CombinedOutput(); err != nil {
			return fmt.Errorf("scheduled task delete failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	default:
		return fmt.Errorf("user service uninstall is not supported on %s", runtime.GOOS)
	}
}

func renderTemplate(text, executablePath, configPath, workingDirectory string) (string, error) {
	if strings.TrimSpace(executablePath) == "" || strings.TrimSpace(configPath) == "" || strings.TrimSpace(workingDirectory) == "" {
		return "", errors.New("executable path, config path, and working directory are required")
	}
	tmpl, err := template.New("service").Parse(text)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, struct {
		Executable       string
		Config           string
		WorkingDirectory string
	}{
		Executable:       executablePath,
		Config:           configPath,
		WorkingDirectory: workingDirectory,
	}); err != nil {
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

func windowsCommandLine(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, windowsQuoteArg(arg))
	}
	return strings.Join(quoted, " ")
}

func windowsQuoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if strings.IndexFunc(arg, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '"' || r == '\\'
	}) < 0 {
		return arg
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range arg {
		switch r {
		case '\\':
			backslashes++
		case '"':
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteRune(r)
			backslashes = 0
		default:
			if backslashes > 0 {
				b.WriteString(strings.Repeat(`\`, backslashes))
				backslashes = 0
			}
			b.WriteRune(r)
		}
	}
	if backslashes > 0 {
		b.WriteString(strings.Repeat(`\`, backslashes*2))
	}
	b.WriteByte('"')
	return b.String()
}

func parseLaunchdStatus(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "state = ") {
			continue
		}
		state := strings.TrimSpace(strings.TrimPrefix(line, "state = "))
		switch state {
		case "running":
			return "running"
		case "not running", "spawn scheduled", "waiting":
			return "stopped"
		default:
			if state != "" {
				return state
			}
		}
	}
	return "running"
}

func normalizeSystemdStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "active":
		return "running"
	case "inactive", "deactivating":
		return "stopped"
	case "":
		return "stopped"
	default:
		return strings.TrimSpace(status)
	}
}

func parseWindowsTaskStatus(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(line), "status:") {
			continue
		}
		status := strings.TrimSpace(line[len("Status:"):])
		switch strings.ToLower(status) {
		case "running":
			return "running"
		case "ready", "queued", "could not start":
			return "stopped"
		default:
			if status != "" {
				return strings.ToLower(status)
			}
		}
	}
	return "running"
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

func launchdUserDomain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

const windowsTaskName = `fleetn-agent`
