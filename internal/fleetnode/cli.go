package fleetnode

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
)

func RunCLI(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		renderHelp(stdout)
		return nil
	}
	switch args[0] {
	case "register":
		return runRegister(ctx, args[1:], stdout, stderr)
	case "run":
		return runAgentCommand(ctx, args[1:], stdout)
	case "approvals":
		return runApprovalsCommand(args[1:], stdout)
	case "status":
		status, err := UserServiceStatus(ctx, InstallOptions{})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, status)
		return nil
	case "stop":
		if err := StopUserService(ctx, InstallOptions{}); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, "stopped")
		return nil
	case "restart":
		if err := RestartUserService(ctx, InstallOptions{}); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, "restarted")
		return nil
	case "uninstall":
		if err := UninstallUserService(ctx, InstallOptions{}); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, "uninstalled")
		return nil
	default:
		return fmt.Errorf("unsupported subcommand %q", args[0])
	}
}

type registerOptions struct {
	serverURL             string
	token                 string
	password              string
	displayName           string
	configPath            string
	identityPath          string
	approvalsPath         string
	browserProxyURL       string
	browserExecutablePath string
	browserHeadless       string
	install               bool
}

func runRegister(ctx context.Context, args []string, stdout, _ io.Writer) error {
	envCfg := ConfigFromEnv(os.Getenv)
	options := registerOptions{configPath: DefaultConfigPath()}

	fs := newFlagSet("register")
	fs.StringVar(&options.serverURL, "server", "", "")
	fs.StringVar(&options.token, "token", "", "")
	fs.StringVar(&options.password, "password", "", "")
	fs.StringVar(&options.displayName, "name", "", "")
	fs.StringVar(&options.configPath, "config", options.configPath, "")
	fs.StringVar(&options.identityPath, "identity", options.identityPath, "")
	fs.StringVar(&options.approvalsPath, "approvals", options.approvalsPath, "")
	fs.StringVar(&options.browserProxyURL, "browser-proxy", "", "")
	fs.StringVar(&options.browserExecutablePath, "browser-executable", "", "")
	fs.StringVar(&options.browserHeadless, "browser-headless", "", "")
	fs.BoolVar(&options.install, "install", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: fleetn register --server <url> (--token <token>|--password <password>) --name <display-name> [--install]")
	}
	cfg, err := registerConfig(options, envCfg)
	if err != nil {
		return err
	}
	if err := SaveConfigFile(options.configPath, cfg); err != nil {
		return err
	}
	identity, err := LoadOrCreateIdentity(cfg.IdentityPath)
	if err != nil {
		return err
	}
	if options.install {
		printRegisterSuccess(stdout, cfg, identity)
		if err := InstallUserService(ctx, InstallOptions{ConfigPath: options.configPath}); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, "installed: user service")
		return nil
	}
	var once sync.Once
	return Run(ctx, cfg, func(info ConnectedInfo) {
		once.Do(func() {
			printRegisterSuccess(stdout, cfg, identity)
			_, _ = fmt.Fprintf(stdout, "connected: %s\n", info.DeviceID)
		})
	})
}

func registerConfig(options registerOptions, envCfg Config) (Config, error) {
	fileCfg, err := loadConfigFileIfExists(options.configPath)
	if err != nil {
		return Config{}, err
	}
	cliCfg := Config{
		ServerURL:             options.serverURL,
		GatewayToken:          options.token,
		GatewayPassword:       options.password,
		DisplayName:           options.displayName,
		IdentityPath:          options.identityPath,
		ApprovalsPath:         options.approvalsPath,
		BrowserProxyURL:       options.browserProxyURL,
		BrowserExecutablePath: options.browserExecutablePath,
	}
	if strings.TrimSpace(options.browserHeadless) != "" {
		value, ok := parseOptionalBool(options.browserHeadless)
		if !ok {
			return Config{}, errors.New("usage: --browser-headless must be true or false")
		}
		cliCfg.BrowserHeadless = value
		cliCfg.BrowserHeadlessSet = true
	}
	return normalizeConfig(MergeConfig(MergeConfig(fileCfg, envCfg), cliCfg))
}

func loadConfigFileIfExists(path string) (Config, error) {
	cfg, err := LoadConfigFile(path)
	if err == nil {
		return cfg, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	return Config{}, err
}

func runAgentCommand(ctx context.Context, args []string, stdout io.Writer) error {
	configPath := DefaultConfigPath()
	fs := newFlagSet("run")
	fs.StringVar(&configPath, "config", configPath, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: fleetn run [--config <path>]")
	}
	var once sync.Once
	return RunConfigFile(ctx, configPath, func(info ConnectedInfo) {
		once.Do(func() {
			_, _ = fmt.Fprintf(stdout, "connected: %s\n", info.DeviceID)
		})
	})
}

func runApprovalsCommand(args []string, stdout io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "add":
			return runApprovalsAdd(args[1:], stdout)
		case "clear":
			return runApprovalsClear(args[1:], stdout)
		}
	}
	configPath := DefaultConfigPath()
	fs := newFlagSet("approvals")
	fs.StringVar(&configPath, "config", configPath, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: fleetn approvals [--config <path>]")
	}
	cfg, err := approvalsConfig(configPath)
	if err != nil {
		return err
	}
	payload, err := execApprovalsGet(cfg)
	if err != nil {
		return err
	}
	return renderApprovals(stdout, payload)
}

func runApprovalsAdd(args []string, stdout io.Writer) error {
	configPath := DefaultConfigPath()
	fs := newFlagSet("approvals add")
	fs.StringVar(&configPath, "config", configPath, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("usage: fleetn approvals add [--config <path>] <pattern>...")
	}
	cfg, err := approvalsConfig(configPath)
	if err != nil {
		return err
	}
	payload, err := execApprovalsAdd(cfg, fs.Args())
	if err != nil {
		return err
	}
	return renderApprovals(stdout, payload)
}

func runApprovalsClear(args []string, stdout io.Writer) error {
	configPath := DefaultConfigPath()
	fs := newFlagSet("approvals clear")
	fs.StringVar(&configPath, "config", configPath, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: fleetn approvals clear [--config <path>]")
	}
	cfg, err := approvalsConfig(configPath)
	if err != nil {
		return err
	}
	payload, err := execApprovalsClear(cfg)
	if err != nil {
		return err
	}
	return renderApprovals(stdout, payload)
}

func approvalsConfig(configPath string) (Config, error) {
	cfg, err := LoadConfigFile(configPath)
	if err == nil {
		return cfg, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}
	return Config{ApprovalsPath: DefaultApprovalsPath()}, nil
}

func renderApprovals(w io.Writer, payload map[string]any) error {
	if path, _ := payload["path"].(string); path != "" {
		_, _ = fmt.Fprintf(w, "Path: %s\n", path)
	}
	if exists, ok := payload["exists"].(bool); ok {
		_, _ = fmt.Fprintf(w, "Exists: %s\n", boolWord(exists))
	}
	if hash, _ := payload["hash"].(string); hash != "" {
		_, _ = fmt.Fprintf(w, "Hash: %s\n", hash)
	}
	file, _ := payload["file"].(map[string]any)
	agents, _ := file["agents"].(map[string]any)
	lines := []string{}
	for agentID, rawAgent := range agents {
		agent, _ := rawAgent.(map[string]any)
		allowlist, _ := agent["allowlist"].([]any)
		for _, item := range allowlist {
			entry, _ := item.(map[string]any)
			pattern, _ := entry["pattern"].(string)
			if strings.TrimSpace(pattern) != "" {
				lines = append(lines, fmt.Sprintf("%s\t%s", agentID, pattern))
			}
		}
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		_, _ = fmt.Fprintln(w, "Allowlist: (empty)")
		return nil
	}
	_, _ = fmt.Fprintln(w, "Allowlist:")
	for _, line := range lines {
		_, _ = fmt.Fprintf(w, "- %s\n", line)
	}
	return nil
}

func boolWord(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func renderHelp(w io.Writer) {
	_, _ = fmt.Fprintln(w, "fleetn connects this machine to fleetd as a node.")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Commands:")
	_, _ = fmt.Fprintln(w, "  fleetn register --server <url> --token <token> --name <display-name> [--install] [--browser-headless true|false]")
	_, _ = fmt.Fprintln(w, "  fleetn register --server <url> --password <password> --name <display-name> [--install] [--browser-headless true|false]")
	_, _ = fmt.Fprintln(w, "  fleetn run [--config <path>]")
	_, _ = fmt.Fprintln(w, "  fleetn approvals [--config <path>]")
	_, _ = fmt.Fprintln(w, "  fleetn approvals add [--config <path>] <pattern>...")
	_, _ = fmt.Fprintln(w, "  fleetn approvals clear [--config <path>]")
	_, _ = fmt.Fprintln(w, "  fleetn status")
	_, _ = fmt.Fprintln(w, "  fleetn stop")
	_, _ = fmt.Fprintln(w, "  fleetn restart")
	_, _ = fmt.Fprintln(w, "  fleetn uninstall")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Environment:")
	for _, item := range []string{"FLEETN_SERVER_URL", "FLEETN_GATEWAY_TOKEN", "FLEETN_GATEWAY_PASSWORD", "FLEETN_DISPLAY_NAME", "FLEETN_BROWSER_PROXY_URL", "FLEETN_BROWSER_EXECUTABLE_PATH", "FLEETN_BROWSER_HEADLESS"} {
		_, _ = fmt.Fprintf(w, "  %s\n", item)
	}
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Notes:")
	_, _ = fmt.Fprintln(w, "  register without --install stays in the foreground.")
	_, _ = fmt.Fprintln(w, "  register --install writes a user-level service and starts it.")
	_, _ = fmt.Fprintln(w, "  after connecting, claim the device from /fleet/claims.")
}
