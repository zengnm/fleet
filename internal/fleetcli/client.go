package fleetcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

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
	if args[0] == "nodes" {
		return errors.New("`fleet nodes ...` has been removed; use `fleet <subcommand>`")
	}
	switch args[0] {
	case "list":
		return c.listNodes(ctx)
	case "status", "describe":
		if len(args) < 2 {
			return errors.New("missing node id")
		}
		return c.describeNode(ctx, args[1])
	case "invoke":
		return c.invokeNode(ctx, args[1:])
	case "run":
		return c.runNode(ctx, args[1:])
	default:
		return fmt.Errorf("unsupported subcommand %q", args[0])
	}
}

func (c *Client) listNodes(ctx context.Context) error {
	var response struct {
		Status string                `json:"status"`
		Nodes  []spec.FleetOwnedNode `json:"nodes"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/runtime/fleet/nodes", nil, &response); err != nil {
		return err
	}
	if len(response.Nodes) == 0 {
		_, _ = fmt.Fprintln(c.stdout, "no nodes")
		return nil
	}
	for _, node := range response.Nodes {
		name := node.DisplayName
		if name == "" {
			name = node.NodeID
		}
		_, _ = fmt.Fprintf(c.stdout, "%s\t%s\t%s\n", node.NodeID, node.Status, name)
	}
	return nil
}

func (c *Client) describeNode(ctx context.Context, nodeID string) error {
	var response struct {
		Status string              `json:"status"`
		Node   spec.FleetOwnedNode `json:"node"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/runtime/fleet/nodes/"+nodeID, nil, &response); err != nil {
		return err
	}
	return c.renderNode(response.Node)
}

func (c *Client) invokeNode(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: fleet invoke <node-id> <command> [--json '{...}']")
	}
	nodeID := args[0]
	command := args[1]
	payload := map[string]any{}
	if len(args) > 2 {
		for index := 2; index < len(args); index++ {
			if args[index] != "--json" || index+1 >= len(args) {
				return errors.New("usage: fleet invoke <node-id> <command> [--json '{...}']")
			}
			index++
			if err := json.Unmarshal([]byte(args[index]), &payload); err != nil {
				return fmt.Errorf("invalid --json payload: %w", err)
			}
		}
	}
	var response struct {
		Status string                   `json:"status"`
		Result spec.FleetInvokeResponse `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/runtime/fleet/nodes/"+nodeID+"/invoke", spec.FleetInvokeRequest{
		Command: command,
		Params:  payload,
	}, &response); err != nil {
		return err
	}
	return c.renderInvokeResult(response.Result)
}

func (c *Client) runNode(ctx context.Context, args []string) error {
	if len(args) < 3 {
		return errors.New("usage: fleet run <node-id> [--json] -- <command> [args...]")
	}
	nodeID := args[0]
	outputJSON := false
	separator := -1
	for index, value := range args[1:] {
		switch value {
		case "--json":
			outputJSON = true
		case "--":
			separator = index + 1
		default:
			if separator == -1 {
				return errors.New("usage: fleet run <node-id> [--json] -- <command> [args...]")
			}
		}
		if separator != -1 {
			break
		}
	}
	if separator == -1 || separator+1 >= len(args) {
		return errors.New("usage: fleet run <node-id> [--json] -- <command> [args...]")
	}
	command := args[separator+1:]
	var response struct {
		Status string                `json:"status"`
		Result spec.FleetRunResponse `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/runtime/fleet/nodes/"+nodeID+"/run", spec.FleetRunRequest{
		Command: command,
	}, &response); err != nil {
		return err
	}
	if outputJSON {
		return json.NewEncoder(c.stdout).Encode(response.Result)
	}
	return c.renderRunResult(response.Result)
}

func (c *Client) renderRunResult(result spec.FleetRunResponse) error {
	stdout, _ := result.Result["stdout"].(string)
	stderr, _ := result.Result["stderr"].(string)
	exitCode, hasExitCode := intValue(result.Result["exitCode"])
	timedOut, _ := result.Result["timedOut"].(bool)
	success, _ := result.Result["success"].(bool)

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
	if !result.OK {
		if invokeErr := invokeErrorMessage(result); invokeErr != "" {
			return errors.New(invokeErr)
		}
		return errors.New("invoke failed")
	}
	payload, err := decodeInvokePayload(result)
	if err != nil {
		return err
	}
	switch result.Command {
	case "system.execApprovals.get":
		return c.renderApprovalsPayload(payload)
	case "system.run.prepare":
		return c.renderPreparedRunPayload(payload)
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

func invokeErrorMessage(result spec.FleetInvokeResponse) string {
	object, ok := result.Payload.(map[string]any)
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
	}
	if c.userID != "" {
		req.Header.Set("USER_ID", c.userID)
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
	_, _ = fmt.Fprintln(c.stdout, "fleet list")
	_, _ = fmt.Fprintln(c.stdout, "fleet status <node-id>")
	_, _ = fmt.Fprintln(c.stdout, "fleet describe <node-id>")
	_, _ = fmt.Fprintln(c.stdout, "fleet invoke <node-id> <command> --json '{...}'")
	_, _ = fmt.Fprintln(c.stdout, "fleet run <node-id> [--json] -- <command> [args...]")
}
