package fleetcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientStatusNodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runtime/fleet/nodes" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("API_KEY") != "runtime-key" {
			t.Fatalf("missing API key header")
		}
		if r.Header.Get("X-API-Key") != "runtime-key" {
			t.Fatalf("missing x-api-key header")
		}
		if r.Header.Get("USER_ID") != "user-a" {
			t.Fatalf("missing user id header")
		}
		if r.Header.Get("X-User-Id") != "user-a" {
			t.Fatalf("missing x-user-id header")
		}
		_, _ = w.Write([]byte(`{"status":"ok","nodes":[{"node_id":"node-1","display_name":"Build Node","status":"online"}]}`))
	}))
	defer server.Close()

	var stdout strings.Builder
	client := New(Config{
		BaseURL: server.URL,
		APIKey:  "runtime-key",
		UserID:  "user-a",
		Stdout:  &stdout,
	})
	if err := client.Run(context.Background(), []string{"status"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "node-1") || !strings.Contains(stdout.String(), "online") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestClientStatusFiltersNodes(t *testing.T) {
	now := time.Now().UTC()
	payload := fmt.Sprintf(`{"status":"ok","nodes":[
		{"node_id":"node-1","display_name":"Build Node","status":"online","connected":true,"last_seen_at":"%s"},
		{"node_id":"node-2","display_name":"Old Node","status":"offline","connected":false,"last_seen_at":"%s"}
	]}`, now.Format(time.RFC3339Nano), now.Add(-48*time.Hour).Format(time.RFC3339Nano))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runtime/fleet/nodes" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()

	var stdout strings.Builder
	client := New(Config{
		BaseURL: server.URL,
		Stdout:  &stdout,
	})
	if err := client.Run(context.Background(), []string{"status", "--connected", "--last-connected", "24h"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "node-1") {
		t.Fatalf("expected connected node in output: %s", output)
	}
	if strings.Contains(output, "node-2") {
		t.Fatalf("unexpected stale node in output: %s", output)
	}
}

func TestClientDescribeResolvesNodeSelectors(t *testing.T) {
	testCases := []struct {
		name     string
		selector string
	}{
		{name: "node id", selector: "node-1"},
		{name: "display name", selector: "Build Node"},
		{name: "remote ip", selector: "10.0.0.8"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/runtime/fleet/nodes":
					_, _ = w.Write([]byte(`{"status":"ok","nodes":[{"node_id":"node-1","display_name":"Build Node","remote_ip":"10.0.0.8","status":"online"}]}`))
				case "/runtime/fleet/nodes/node-1":
					_, _ = w.Write([]byte(`{"status":"ok","node":{"node_id":"node-1","display_name":"Build Node","remote_ip":"10.0.0.8","status":"online","connected":true,"paired":true}}`))
				default:
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
			}))
			defer server.Close()

			var stdout strings.Builder
			client := New(Config{
				BaseURL: server.URL,
				Stdout:  &stdout,
			})
			if err := client.Run(context.Background(), []string{"describe", "--node", testCase.selector}); err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			output := stdout.String()
			if !strings.Contains(output, "ID: node-1") || !strings.Contains(output, "Name: Build Node") {
				t.Fatalf("unexpected output: %s", output)
			}
		})
	}
}

func TestClientDescribeRejectsAmbiguousSelector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runtime/fleet/nodes" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"ok","nodes":[
			{"node_id":"node-1","display_name":"Build Node","remote_ip":"10.0.0.8","status":"online"},
			{"node_id":"node-2","display_name":"Build Node","remote_ip":"10.0.0.9","status":"online"}
		]}`))
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL})
	err := client.Run(context.Background(), []string{"describe", "--node", "Build Node"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous node selector") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientInvokePassesParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/runtime/fleet/nodes":
			_, _ = w.Write([]byte(`{"status":"ok","nodes":[{"node_id":"node-1","display_name":"Build Node","status":"online"}]}`))
		case "/runtime/fleet/nodes/node-1/invoke":
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			var request struct {
				Command string         `json:"command"`
				Params  map[string]any `json:"params"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if request.Command != "custom.echo" {
				t.Fatalf("unexpected command: %s", request.Command)
			}
			if request.Params["message"] != "hello" {
				t.Fatalf("unexpected params: %+v", request.Params)
			}
			_, _ = w.Write([]byte(`{"status":"ok","result":{"node_id":"node-1","command":"custom.echo","ok":true,"payload":{"message":"hello","count":2,"items":["a","b"]}}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	var stdout strings.Builder
	client := New(Config{
		BaseURL: server.URL,
		Stdout:  &stdout,
	})
	err := client.Run(context.Background(), []string{
		"invoke",
		"--node", "Build Node",
		"--command", "custom.echo",
		"--params", `{"message":"hello"}`,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	output := stdout.String()
	if strings.Contains(output, "{") || strings.Contains(output, `"message"`) {
		t.Fatalf("unexpected json output: %s", output)
	}
	if !strings.Contains(output, "message: hello") || !strings.Contains(output, "count: 2") || !strings.Contains(output, "- a") {
		t.Fatalf("unexpected plain-text output: %s", output)
	}
}

func TestClientInvokeSystemRunWritesStreams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/runtime/fleet/nodes":
			_, _ = w.Write([]byte(`{"status":"ok","nodes":[{"node_id":"node-1","display_name":"Build Node","status":"online"}]}`))
		case "/runtime/fleet/nodes/node-1/invoke":
			_, _ = w.Write([]byte(`{"status":"ok","result":{"node_id":"node-1","command":"system.run","ok":true,"payload":{"stdout":"hello\n","stderr":"warn\n","exitCode":0,"success":true,"timedOut":false}}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	var stdout strings.Builder
	var stderr strings.Builder
	client := New(Config{
		BaseURL: server.URL,
		Stdout:  &stdout,
		Stderr:  &stderr,
	})
	err := client.Run(context.Background(), []string{
		"invoke",
		"--node", "Build Node",
		"--command", "system.run",
		"--params", `{"command":["echo","hello"]}`,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if stdout.String() != "hello\n" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if stderr.String() != "warn\n" {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestClientInvokeSystemRunReturnsExitError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/runtime/fleet/nodes":
			_, _ = w.Write([]byte(`{"status":"ok","nodes":[{"node_id":"node-1","display_name":"Build Node","status":"online"}]}`))
		case "/runtime/fleet/nodes/node-1/invoke":
			_, _ = w.Write([]byte(`{"status":"ok","result":{"node_id":"node-1","command":"system.run","ok":true,"payload":{"stdout":"","stderr":"boom\n","exitCode":7,"success":false,"timedOut":false}}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	var stderr strings.Builder
	client := New(Config{
		BaseURL: server.URL,
		Stderr:  &stderr,
	})
	err := client.Run(context.Background(), []string{
		"invoke",
		"--node", "Build Node",
		"--command", "system.run",
		"--params", `{"command":["false"]}`,
	})
	if err == nil || !strings.Contains(err.Error(), "run exit 7") {
		t.Fatalf("unexpected error: %v", err)
	}
	if stderr.String() != "boom\n" {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestClientRunUsesShellCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/runtime/fleet/nodes":
			_, _ = w.Write([]byte(`{"status":"ok","nodes":[{"node_id":"node-1","display_name":"Build Node","platform":"darwin","status":"online"}]}`))
		case "/runtime/fleet/nodes/node-1/run":
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			var request struct {
				Command []string `json:"command"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			want := []string{"sh", "-lc", "echo hello && uname"}
			if strings.Join(request.Command, "\x00") != strings.Join(want, "\x00") {
				t.Fatalf("command = %#v, want %#v", request.Command, want)
			}
			_, _ = w.Write([]byte(`{"status":"ok","result":{"node_id":"node-1","accepted":true,"result":{"stdout":"hello\n","stderr":"","exitCode":0,"success":true,"timedOut":false}}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	var stdout strings.Builder
	client := New(Config{
		BaseURL: server.URL,
		Stdout:  &stdout,
	})
	err := client.Run(context.Background(), []string{
		"run",
		"--node", "Build Node",
		"--", "echo", "hello", "&&", "uname",
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if stdout.String() != "hello\n" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestClientRunUsesWindowsShell(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/runtime/fleet/nodes":
			_, _ = w.Write([]byte(`{"status":"ok","nodes":[{"node_id":"node-1","display_name":"Win Node","platform":"windows","status":"online"}]}`))
		case "/runtime/fleet/nodes/node-1/run":
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			var request struct {
				Command []string `json:"command"`
			}
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			want := []string{"cmd", "/C", "dir"}
			if strings.Join(request.Command, "\x00") != strings.Join(want, "\x00") {
				t.Fatalf("command = %#v, want %#v", request.Command, want)
			}
			_, _ = w.Write([]byte(`{"status":"ok","result":{"node_id":"node-1","accepted":true,"result":{"stdout":"","stderr":"","exitCode":0,"success":true,"timedOut":false}}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL})
	if err := client.Run(context.Background(), []string{"run", "--node", "Win Node", "--", "dir"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
}

func TestClientRejectsRemovedCLIForms(t *testing.T) {
	client := New(Config{})

	err := client.Run(context.Background(), []string{"run", "--node", "node-1"})
	if err == nil || !strings.Contains(err.Error(), "usage: fleet run --node <id|name|ip> -- <shell-command>") {
		t.Fatalf("unexpected run usage error: %v", err)
	}

	err = client.Run(context.Background(), []string{"list"})
	if err == nil || !strings.Contains(err.Error(), `unsupported subcommand "list"`) {
		t.Fatalf("unexpected list error: %v", err)
	}

	err = client.Run(context.Background(), []string{"invoke", "node-1", "custom.echo"})
	if err == nil || !strings.Contains(err.Error(), "usage: fleet invoke --node <id|name|ip> --command <command> [--params <json>]") {
		t.Fatalf("unexpected positional invoke error: %v", err)
	}
}

func TestClientHelpOnlyShowsNewCommands(t *testing.T) {
	var stdout strings.Builder
	client := New(Config{Stdout: &stdout})

	if err := client.Run(context.Background(), []string{"help"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	output := stdout.String()
	for _, expected := range []string{
		"fleet status [--connected] [--last-connected <duration>]",
		"fleet describe --node <id|name|ip>",
		"fleet run --node <id|name|ip> -- <shell-command>",
		"fleet invoke --node <id|name|ip> --command <command> [--params <json>]",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("missing help entry %q in %s", expected, output)
		}
	}
	for _, unexpected := range []string{
		"fleet list",
		"--json",
		"fleet describe <node-id>",
		"fleet invoke <node-id>",
	} {
		if strings.Contains(output, unexpected) {
			t.Fatalf("unexpected legacy help entry %q in %s", unexpected, output)
		}
	}
}
