package fleetcli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientListNodes(t *testing.T) {
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
	if err := client.Run(context.Background(), []string{"list"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "node-1") || !strings.Contains(stdout.String(), "online") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestClientRejectsRemovedNodesPrefix(t *testing.T) {
	client := New(Config{})
	err := client.Run(context.Background(), []string{"nodes", "list"})
	if err == nil || !strings.Contains(err.Error(), "has been removed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientRunNodeDefaultsToPlainText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runtime/fleet/nodes/node-1/run" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"ok","result":{"node_id":"node-1","accepted":true,"result":{"stdout":"hello\n","stderr":"warn\n","exitCode":0,"success":true,"timedOut":false}}}`))
	}))
	defer server.Close()

	var stdout strings.Builder
	var stderr strings.Builder
	client := New(Config{
		BaseURL: server.URL,
		Stdout:  &stdout,
		Stderr:  &stderr,
	})
	if err := client.Run(context.Background(), []string{"run", "node-1", "--", "echo", "hello"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if stdout.String() != "hello\n" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if stderr.String() != "warn\n" {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestClientRunNodeJSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","result":{"node_id":"node-1","accepted":true,"result":{"stdout":"hello\n","exitCode":0,"success":true,"timedOut":false}}}`))
	}))
	defer server.Close()

	var stdout strings.Builder
	client := New(Config{
		BaseURL: server.URL,
		Stdout:  &stdout,
	})
	if err := client.Run(context.Background(), []string{"run", "node-1", "--json", "--", "echo", "hello"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !strings.Contains(stdout.String(), `"node_id":"node-1"`) {
		t.Fatalf("unexpected json output: %s", stdout.String())
	}
}

func TestClientInvokeDefaultsToPlainText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","result":{"node_id":"node-1","command":"custom.echo","ok":true,"payload":{"message":"hello","count":2,"items":["a","b"]}}}`))
	}))
	defer server.Close()

	var stdout strings.Builder
	client := New(Config{
		BaseURL: server.URL,
		Stdout:  &stdout,
	})
	if err := client.Run(context.Background(), []string{"invoke", "node-1", "custom.echo", "--json", "{}"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	output := stdout.String()
	if strings.Contains(output, "{") || strings.Contains(output, "\"message\"") {
		t.Fatalf("unexpected json output: %s", output)
	}
	if !strings.Contains(output, "message: hello") || !strings.Contains(output, "count: 2") || !strings.Contains(output, "- a") {
		t.Fatalf("unexpected plain-text output: %s", output)
	}
}
