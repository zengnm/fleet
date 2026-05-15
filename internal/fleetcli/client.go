package fleetcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"fleetd/pkg/spec"
)

type Config struct {
	BaseURL string
	APIKey  string
	UserID  string
	Stdout  io.Writer
	Stderr  io.Writer
}

type Client struct {
	baseURL string
	apiKey  string
	userID  string
	http    *http.Client
	stdout  io.Writer
	stderr  io.Writer
}

func New(cfg Config) *Client {
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	return &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		userID:  cfg.UserID,
		http:    &http.Client{},
		stdout:  cfg.Stdout,
		stderr:  cfg.Stderr,
	}
}

func (c *Client) Run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		c.renderHelp()
		return nil
	}
	switch args[0] {
	case "status":
		return c.statusNodes(ctx, args[1:])
	case "describe":
		return c.describeNode(ctx, args[1:])
	case "run":
		return c.runNode(ctx, args[1:])
	case "invoke":
		return c.invokeNode(ctx, args[1:])
	default:
		return fmt.Errorf("unsupported subcommand %q", args[0])
	}
}

func (c *Client) statusNodes(ctx context.Context, args []string) error {
	options, err := parseListOptions(args)
	if err != nil {
		return errors.New("usage: fleet status [--connected] [--last-connected <duration>]")
	}
	nodes, err := c.fetchNodes(ctx)
	if err != nil {
		return err
	}
	nodes = filterNodes(nodes, options, time.Now().UTC())
	if len(nodes) == 0 {
		_, _ = fmt.Fprintln(c.stdout, "no nodes")
		return nil
	}
	for _, node := range nodes {
		name := node.DisplayName
		if name == "" {
			name = node.NodeID
		}
		_, _ = fmt.Fprintf(c.stdout, "%s\t%s\t%s\n", node.NodeID, node.Status, name)
	}
	return nil
}

func (c *Client) fetchNodes(ctx context.Context) ([]spec.FleetOwnedNode, error) {
	var response struct {
		Status string                `json:"status"`
		Nodes  []spec.FleetOwnedNode `json:"nodes"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/runtime/fleet/nodes", nil, &response); err != nil {
		return nil, err
	}
	return response.Nodes, nil
}

func (c *Client) describeNode(ctx context.Context, args []string) error {
	selector, err := parseNodeSelector("describe", args)
	if err != nil {
		return err
	}
	node, err := c.resolveNode(ctx, selector)
	if err != nil {
		return err
	}
	var response struct {
		Status string              `json:"status"`
		Node   spec.FleetOwnedNode `json:"node"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/runtime/fleet/nodes/"+node.NodeID, nil, &response); err != nil {
		return err
	}
	return c.renderNode(response.Node)
}

func (c *Client) invokeNode(ctx context.Context, args []string) error {
	options, err := parseInvokeOptions(args)
	if err != nil {
		return err
	}
	node, err := c.resolveNode(ctx, options.node)
	if err != nil {
		return err
	}
	payload := map[string]any{}
	if strings.TrimSpace(options.params) != "" {
		if err := json.Unmarshal([]byte(options.params), &payload); err != nil {
			return fmt.Errorf("invalid --params payload: %w", err)
		}
	}
	var response struct {
		Status string                   `json:"status"`
		Result spec.FleetInvokeResponse `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/runtime/fleet/nodes/"+node.NodeID+"/invoke", spec.FleetInvokeRequest{
		Command: options.command,
		Params:  payload,
	}, &response); err != nil {
		return err
	}
	return c.renderInvokeResult(response.Result)
}

func (c *Client) runNode(ctx context.Context, args []string) error {
	options, err := parseRunOptions(args)
	if err != nil {
		return err
	}
	node, err := c.resolveNode(ctx, options.node)
	if err != nil {
		return err
	}
	var response struct {
		Status string                `json:"status"`
		Result spec.FleetRunResponse `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/runtime/fleet/nodes/"+node.NodeID+"/run", spec.FleetRunRequest{
		Command: shellCommandForNode(node, options.command),
	}, &response); err != nil {
		return err
	}
	return c.renderRunPayload(response.Result.Result)
}

func (c *Client) resolveNode(ctx context.Context, selector string) (spec.FleetOwnedNode, error) {
	nodes, err := c.fetchNodes(ctx)
	if err != nil {
		return spec.FleetOwnedNode{}, err
	}
	selector = strings.TrimSpace(selector)
	matches := make([]spec.FleetOwnedNode, 0, 1)
	for _, node := range nodes {
		if selector == node.NodeID || selector == node.DisplayName || selector == node.RemoteIP {
			matches = append(matches, node)
		}
	}
	switch len(matches) {
	case 0:
		return spec.FleetOwnedNode{}, errors.New("node not found")
	case 1:
		return matches[0], nil
	default:
		candidates := make([]string, 0, len(matches))
		for _, node := range matches {
			name := node.DisplayName
			if name == "" {
				name = "-"
			}
			ip := node.RemoteIP
			if ip == "" {
				ip = "-"
			}
			candidates = append(candidates, fmt.Sprintf("%s(name=%s, ip=%s)", node.NodeID, name, ip))
		}
		sort.Strings(candidates)
		return spec.FleetOwnedNode{}, fmt.Errorf("ambiguous node selector %q: %s", selector, strings.Join(candidates, ", "))
	}
}

type listOptions struct {
	connected     bool
	lastConnected time.Duration
}

func parseListOptions(args []string) (listOptions, error) {
	var options listOptions
	var lastConnected string
	fs := newFlagSet("list")
	fs.BoolVar(&options.connected, "connected", false, "")
	fs.StringVar(&lastConnected, "last-connected", "", "")
	if err := fs.Parse(args); err != nil {
		return listOptions{}, err
	}
	if fs.NArg() != 0 {
		return listOptions{}, errors.New("unexpected arguments")
	}
	if strings.TrimSpace(lastConnected) != "" {
		duration, err := time.ParseDuration(lastConnected)
		if err != nil {
			return listOptions{}, err
		}
		options.lastConnected = duration
	}
	return options, nil
}

func parseNodeSelector(command string, args []string) (string, error) {
	fs := newFlagSet(command)
	var node string
	fs.StringVar(&node, "node", "", "")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 0 || strings.TrimSpace(node) == "" {
		return "", fmt.Errorf("usage: fleet %s --node <id|name|ip>", command)
	}
	return node, nil
}

type invokeOptions struct {
	node    string
	command string
	params  string
}

func parseInvokeOptions(args []string) (invokeOptions, error) {
	var options invokeOptions
	fs := newFlagSet("invoke")
	fs.StringVar(&options.node, "node", "", "")
	fs.StringVar(&options.command, "command", "", "")
	fs.StringVar(&options.params, "params", "", "")
	if err := fs.Parse(args); err != nil {
		return invokeOptions{}, err
	}
	if fs.NArg() != 0 || strings.TrimSpace(options.node) == "" || strings.TrimSpace(options.command) == "" {
		return invokeOptions{}, errors.New("usage: fleet invoke --node <id|name|ip> --command <command> [--params <json>]")
	}
	return options, nil
}

type runOptions struct {
	node    string
	command string
}

func parseRunOptions(args []string) (runOptions, error) {
	var options runOptions
	fs := newFlagSet("run")
	fs.StringVar(&options.node, "node", "", "")
	if err := fs.Parse(args); err != nil {
		return runOptions{}, err
	}
	commandArgs := fs.Args()
	if strings.TrimSpace(options.node) == "" || len(commandArgs) == 0 {
		return runOptions{}, errors.New("usage: fleet run --node <id|name|ip> -- <shell-command>")
	}
	options.command = strings.TrimSpace(strings.Join(commandArgs, " "))
	if options.command == "" {
		return runOptions{}, errors.New("usage: fleet run --node <id|name|ip> -- <shell-command>")
	}
	return options, nil
}

func shellCommandForNode(node spec.FleetOwnedNode, command string) []string {
	if isWindowsPlatform(node.Platform) {
		return []string{"cmd", "/C", command}
	}
	return []string{"sh", "-lc", command}
}

func isWindowsPlatform(platform string) bool {
	platform = strings.ToLower(strings.TrimSpace(platform))
	return platform == "windows" || platform == "win32" || platform == "win64"
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func filterNodes(nodes []spec.FleetOwnedNode, options listOptions, now time.Time) []spec.FleetOwnedNode {
	filtered := make([]spec.FleetOwnedNode, 0, len(nodes))
	for _, node := range nodes {
		if options.connected && !node.Connected {
			continue
		}
		if options.lastConnected > 0 {
			lastConnected := node.LastSeenAt
			if lastConnected.IsZero() {
				lastConnected = node.ConnectedAt
			}
			if lastConnected.IsZero() || now.Sub(lastConnected) > options.lastConnected {
				continue
			}
		}
		filtered = append(filtered, node)
	}
	return filtered
}

func (c *Client) renderRunPayload(payload any) error {
	result, ok := payload.(map[string]any)
	if !ok {
		return c.renderGenericPayload(payload)
	}
	stdout, _ := result["stdout"].(string)
	stderr, _ := result["stderr"].(string)
	exitCode, hasExitCode := intValue(result["exitCode"])
	timedOut, _ := result["timedOut"].(bool)
	success, _ := result["success"].(bool)

	if stdout != "" {
		_, _ = io.WriteString(c.stdout, stdout)
	}
	if stderr != "" {
		_, _ = io.WriteString(c.stderr, stderr)
	}
	if timedOut {
		return errors.New("run timed out")
	}
	if hasExitCode && exitCode != 0 && !success {
		return fmt.Errorf("run exit %d", exitCode)
	}
	return nil
}

func (c *Client) renderNode(node spec.FleetOwnedNode) error {
	lines := []string{
		fmt.Sprintf("ID: %s", node.NodeID),
		fmt.Sprintf("Name: %s", firstNonEmpty(node.DisplayName, node.NodeID)),
		fmt.Sprintf("Status: %s", firstNonEmpty(node.Status, "unknown")),
		fmt.Sprintf("Connected: %s", yesNo(node.Connected)),
		fmt.Sprintf("Paired: %s", yesNo(node.Paired)),
	}
	if node.Platform != "" {
		lines = append(lines, fmt.Sprintf("Platform: %s", node.Platform))
	}
	if node.Version != "" {
		lines = append(lines, fmt.Sprintf("Version: %s", node.Version))
	}
	if node.DeviceFamily != "" {
		lines = append(lines, fmt.Sprintf("Device: %s", node.DeviceFamily))
	}
	if node.RemoteIP != "" {
		lines = append(lines, fmt.Sprintf("IP: %s", node.RemoteIP))
	}
	if node.ClientID != "" || node.ClientMode != "" {
		lines = append(lines, fmt.Sprintf("Client: %s / %s", firstNonEmpty(node.ClientID, "-"), firstNonEmpty(node.ClientMode, "-")))
	}
	if node.PathEnv != "" {
		lines = append(lines, fmt.Sprintf("PATH: %s", node.PathEnv))
	}
	if _, err := fmt.Fprintln(c.stdout, strings.Join(lines, "\n")); err != nil {
		return err
	}
	if len(node.Caps) > 0 {
		_, _ = fmt.Fprintln(c.stdout, "")
		_, _ = fmt.Fprintln(c.stdout, "Caps:")
		for _, item := range node.Caps {
			_, _ = fmt.Fprintf(c.stdout, "- %s\n", item)
		}
	}
	if len(node.Commands) > 0 {
		_, _ = fmt.Fprintln(c.stdout, "")
		_, _ = fmt.Fprintln(c.stdout, "Commands:")
		for _, item := range node.Commands {
			_, _ = fmt.Fprintf(c.stdout, "- %s\n", item)
		}
	}
	return nil
}

func (c *Client) renderInvokeResult(result spec.FleetInvokeResponse) error {
	payload, err := decodeInvokePayload(result)
	if err != nil {
		return err
	}
	if !result.OK {
		if invokeErr := invokeErrorMessage(payload); invokeErr != "" {
			return errors.New(invokeErr)
		}
		return errors.New("invoke failed")
	}
	switch result.Command {
	case "system.execApprovals.get":
		return c.renderApprovalsPayload(payload)
	case "system.run.prepare":
		return c.renderPreparedRunPayload(payload)
	case "system.run":
		return c.renderRunPayload(payload)
	case "system.which":
		return c.renderWhichPayload(payload)
	default:
		return c.renderGenericPayload(payload)
	}
}

func (c *Client) renderApprovalsPayload(payload any) error {
	object, ok := payload.(map[string]any)
	if !ok {
		return c.renderGenericPayload(payload)
	}
	lines := []string{}
	if path, _ := object["path"].(string); path != "" {
		lines = append(lines, fmt.Sprintf("Path: %s", path))
	}
	if exists, ok := object["exists"].(bool); ok {
		lines = append(lines, fmt.Sprintf("Exists: %s", yesNo(exists)))
	}
	if hash, _ := object["hash"].(string); hash != "" {
		lines = append(lines, fmt.Sprintf("Hash: %s", hash))
	}
	if len(lines) > 0 {
		_, _ = fmt.Fprintln(c.stdout, strings.Join(lines, "\n"))
	}
	file, _ := object["file"].(map[string]any)
	agents, _ := file["agents"].(map[string]any)
	patterns := []string{}
	for agentID, rawAgent := range agents {
		agent, _ := rawAgent.(map[string]any)
		allowlist, _ := agent["allowlist"].([]any)
		for _, item := range allowlist {
			entry, _ := item.(map[string]any)
			pattern, _ := entry["pattern"].(string)
			if strings.TrimSpace(pattern) != "" {
				patterns = append(patterns, fmt.Sprintf("%s\t%s", agentID, pattern))
			}
		}
	}
	if len(patterns) == 0 {
		if len(lines) == 0 {
			_, _ = fmt.Fprintln(c.stdout, "(empty)")
		}
		return nil
	}
	_, _ = fmt.Fprintln(c.stdout, "")
	_, _ = fmt.Fprintln(c.stdout, "Allowlist:")
	for _, item := range patterns {
		_, _ = fmt.Fprintf(c.stdout, "- %s\n", item)
	}
	return nil
}

func (c *Client) renderPreparedRunPayload(payload any) error {
	object, ok := payload.(map[string]any)
	if !ok {
		return c.renderGenericPayload(payload)
	}
	cmdText, _ := object["cmdText"].(string)
	plan, _ := object["plan"].(map[string]any)
	rawCommand, _ := plan["rawCommand"].(string)
	cwd, _ := plan["cwd"].(string)
	if cmdText == "" {
		cmdText = rawCommand
	}
	if cmdText != "" {
		_, _ = fmt.Fprintf(c.stdout, "Command: %s\n", cmdText)
	}
	if cwd != "" {
		_, _ = fmt.Fprintf(c.stdout, "CWD: %s\n", cwd)
	}
	argv, _ := plan["argv"].([]any)
	if len(argv) > 0 {
		_, _ = fmt.Fprintln(c.stdout, "Argv:")
		for _, item := range argv {
			_, _ = fmt.Fprintf(c.stdout, "- %v\n", item)
		}
	}
	return nil
}

func (c *Client) renderWhichPayload(payload any) error {
	object, ok := payload.(map[string]any)
	if !ok {
		return c.renderGenericPayload(payload)
	}
	bins, _ := object["bins"].(map[string]any)
	if len(bins) == 0 {
		_, _ = fmt.Fprintln(c.stdout, "no bins found")
		return nil
	}
	for name, value := range bins {
		_, _ = fmt.Fprintf(c.stdout, "%s\t%v\n", name, value)
	}
	return nil
}

func (c *Client) renderGenericPayload(payload any) error {
	lines := renderValueLines("", payload, 0)
	if len(lines) == 0 {
		_, _ = fmt.Fprintln(c.stdout, "ok")
		return nil
	}
	_, err := fmt.Fprintln(c.stdout, strings.Join(lines, "\n"))
	return err
}

func decodeInvokePayload(result spec.FleetInvokeResponse) (any, error) {
	if result.Payload != nil {
		return result.Payload, nil
	}
	if strings.TrimSpace(result.PayloadJSON) == "" {
		return nil, nil
	}
	var payload any
	if err := json.Unmarshal([]byte(result.PayloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func invokeErrorMessage(payload any) string {
	object, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	inner, _ := object["error"].(map[string]any)
	message, _ := inner["message"].(string)
	code, _ := inner["code"].(string)
	switch {
	case code != "" && message != "":
		return fmt.Sprintf("%s: %s", code, message)
	case message != "":
		return message
	case code != "":
		return code
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func intValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

func renderValueLines(label string, value any, indent int) []string {
	prefix := strings.Repeat("  ", indent)
	switch typed := value.(type) {
	case nil:
		if label == "" {
			return nil
		}
		return []string{fmt.Sprintf("%s%s: null", prefix, label)}
	case string:
		if label == "" {
			return []string{prefix + typed}
		}
		return []string{fmt.Sprintf("%s%s: %s", prefix, label, typed)}
	case bool, int, int32, int64, float64:
		if label == "" {
			return []string{fmt.Sprintf("%s%v", prefix, typed)}
		}
		return []string{fmt.Sprintf("%s%s: %v", prefix, label, typed)}
	case []any:
		return renderSliceLines(label, typed, indent)
	case map[string]any:
		return renderMapLines(label, typed, indent)
	default:
		if label == "" {
			return []string{fmt.Sprintf("%s%v", prefix, typed)}
		}
		return []string{fmt.Sprintf("%s%s: %v", prefix, label, typed)}
	}
}

func renderMapLines(label string, value map[string]any, indent int) []string {
	prefix := strings.Repeat("  ", indent)
	lines := []string{}
	if label != "" {
		lines = append(lines, fmt.Sprintf("%s%s:", prefix, label))
		indent++
		prefix = strings.Repeat("  ", indent)
	}
	if len(value) == 0 {
		if label == "" {
			return nil
		}
		return append(lines, prefix+"(empty)")
	}
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		lines = append(lines, renderValueLines(key, value[key], indent)...)
	}
	return lines
}

func renderSliceLines(label string, value []any, indent int) []string {
	prefix := strings.Repeat("  ", indent)
	lines := []string{}
	if label != "" {
		lines = append(lines, fmt.Sprintf("%s%s:", prefix, label))
		indent++
		prefix = strings.Repeat("  ", indent)
	}
	if len(value) == 0 {
		if label == "" {
			return nil
		}
		return append(lines, prefix+"(empty)")
	}
	for _, item := range value {
		switch typed := item.(type) {
		case nil:
			lines = append(lines, prefix+"- null")
		case string, bool, int, int32, int64, float64:
			lines = append(lines, fmt.Sprintf("%s- %v", prefix, typed))
		case map[string]any:
			lines = append(lines, prefix+"-")
			lines = append(lines, renderMapLines("", typed, indent+1)...)
		case []any:
			lines = append(lines, prefix+"-")
			lines = append(lines, renderSliceLines("", typed, indent+1)...)
		default:
			lines = append(lines, fmt.Sprintf("%s- %v", prefix, typed))
		}
	}
	return lines
}

func (c *Client) doJSON(ctx context.Context, method, path string, requestBody any, responseBody any) error {
	var body io.Reader
	if requestBody != nil {
		data, err := json.Marshal(requestBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("API_KEY", c.apiKey)
		req.Header.Set("X-API-Key", c.apiKey)
	}
	if c.userID != "" {
		req.Header.Set("USER_ID", c.userID)
		req.Header.Set("X-User-Id", c.userID)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		var envelope spec.Envelope
		if decodeErr := json.NewDecoder(res.Body).Decode(&envelope); decodeErr == nil && envelope.Error != nil {
			return errors.New(envelope.Error.Message)
		}
		payload, _ := io.ReadAll(res.Body)
		return fmt.Errorf("request failed: %s", strings.TrimSpace(string(payload)))
	}
	if responseBody == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(responseBody)
}

func (c *Client) renderHelp() {
	_, _ = fmt.Fprintln(c.stdout, "fleet controls connected nodes through status, describe, and invoke.")
	_, _ = fmt.Fprintln(c.stdout, "")
	_, _ = fmt.Fprintln(c.stdout, "Commands:")
	_, _ = fmt.Fprintln(c.stdout, "  fleet status [--connected] [--last-connected <duration>]")
	_, _ = fmt.Fprintln(c.stdout, "  fleet describe --node <id|name|ip>")
	_, _ = fmt.Fprintln(c.stdout, "  fleet run --node <id|name|ip> -- <shell-command>")
	_, _ = fmt.Fprintln(c.stdout, "  fleet invoke --node <id|name|ip> --command <command> [--params <json>]")
	_, _ = fmt.Fprintln(c.stdout, "")
	_, _ = fmt.Fprintln(c.stdout, "Examples:")
	_, _ = fmt.Fprintln(c.stdout, `  fleet status --connected`)
	_, _ = fmt.Fprintln(c.stdout, `  fleet describe --node "Build Node"`)
	_, _ = fmt.Fprintln(c.stdout, `  fleet run --node "Build Node" -- 'uname -a'`)
	_, _ = fmt.Fprintln(c.stdout, `  fleet invoke --node "Build Node" --command system.which --params '{"name":"git","bins":["/usr/bin/git"]}'`)
	_, _ = fmt.Fprintln(c.stdout, `  fleet invoke --node "Build Node" --command system.run.prepare --params '{"command":["uname","-a"],"rawCommand":"uname -a"}'`)
	_, _ = fmt.Fprintln(c.stdout, `  fleet invoke --node "Build Node" --command system.run --params '{"command":["uname","-a"]}'`)
	_, _ = fmt.Fprintln(c.stdout, "")
	_, _ = fmt.Fprintln(c.stdout, "Notes:")
	_, _ = fmt.Fprintln(c.stdout, "  Use describe to inspect commands before invoke.")
	_, _ = fmt.Fprintln(c.stdout, "  Use run for concise shell execution on a node.")
	_, _ = fmt.Fprintln(c.stdout, "  Use invoke with system.run.prepare to preview execution.")
	_, _ = fmt.Fprintln(c.stdout, "  Use invoke with system.run for remote execution.")
}
