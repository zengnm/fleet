package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"fleetd/pkg/spec"
)

func TestGatewayBackendEndToEnd(t *testing.T) {
	t.Parallel()

	var (
		mu                  sync.Mutex
		connectDeviceTokens []string
	)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		if err := conn.WriteJSON(map[string]any{
			"type":  "event",
			"event": "connect.challenge",
			"payload": map[string]any{
				"nonce": "nonce-1",
			},
		}); err != nil {
			t.Errorf("write challenge failed: %v", err)
			return
		}

		for {
			var frame map[string]any
			if err := conn.ReadJSON(&frame); err != nil {
				return
			}
			reqID, _ := frame["id"].(string)
			method, _ := frame["method"].(string)
			params, _ := frame["params"].(map[string]any)
			switch method {
			case "connect":
				auth, _ := params["auth"].(map[string]any)
				mu.Lock()
				connectDeviceTokens = append(connectDeviceTokens, stringValue(auth["deviceToken"]))
				mu.Unlock()
				if err := conn.WriteJSON(map[string]any{
					"type": "res",
					"id":   reqID,
					"ok":   true,
					"payload": map[string]any{
						"type": "hello-ok",
						"auth": map[string]any{
							"deviceToken": "stored-device-token",
							"scopes":      []string{"operator.admin"},
						},
					},
				}); err != nil {
					t.Errorf("write connect response failed: %v", err)
					return
				}
			case "device.pair.list":
				_ = conn.WriteJSON(map[string]any{
					"type": "res",
					"id":   reqID,
					"ok":   true,
					"payload": map[string]any{
						"pending": []map[string]any{
							{
								"requestId":    "pair-node",
								"deviceId":     "node-1",
								"displayName":  "Node One",
								"publicKey":    "pub-1",
								"platform":     "darwin",
								"deviceFamily": "desktop",
								"clientId":     "node-host",
								"clientMode":   "node",
								"role":         "node",
								"remoteIp":     "127.0.0.1",
								"ts":           time.Now().UnixMilli(),
							},
							{
								"requestId": "pair-operator",
								"deviceId":  "operator-1",
								"role":      "operator",
								"ts":        time.Now().UnixMilli(),
							},
						},
					},
				})
			case "device.pair.approve":
				_ = conn.WriteJSON(map[string]any{
					"type": "res",
					"id":   reqID,
					"ok":   true,
					"payload": map[string]any{
						"requestId": "pair-node",
						"device": map[string]any{
							"deviceId":     "node-1",
							"displayName":  "Node One",
							"platform":     "darwin",
							"deviceFamily": "desktop",
							"clientId":     "node-host",
							"clientMode":   "node",
							"role":         "node",
							"remoteIp":     "127.0.0.1",
							"approvedAtMs": time.Now().UnixMilli(),
						},
					},
				})
			case "node.list":
				_ = conn.WriteJSON(map[string]any{
					"type": "res",
					"id":   reqID,
					"ok":   true,
					"payload": map[string]any{
						"nodes": []map[string]any{
							{
								"nodeId":        "node-1",
								"displayName":   "Node One",
								"platform":      "darwin",
								"clientId":      "node-host",
								"clientMode":    "node",
								"deviceFamily":  "desktop",
								"commands":      []string{"system.which", "system.run", "system.run.prepare"},
								"caps":          []string{"camera"},
								"permissions":   map[string]bool{"exec": true},
								"paired":        true,
								"connected":     true,
								"connectedAtMs": time.Now().UnixMilli(),
								"approvedAtMs":  time.Now().UnixMilli(),
							},
							{
								"nodeId":      "node-2",
								"displayName": "Node Two",
								"paired":      true,
								"connected":   false,
							},
						},
					},
				})
			case "node.describe":
				_ = conn.WriteJSON(map[string]any{
					"type": "res",
					"id":   reqID,
					"ok":   true,
					"payload": map[string]any{
						"nodeId":        "node-1",
						"displayName":   "Node One",
						"platform":      "darwin",
						"clientId":      "node-host",
						"clientMode":    "node",
						"deviceFamily":  "desktop",
						"commands":      []string{"system.which", "system.run", "system.run.prepare"},
						"caps":          []string{"camera"},
						"permissions":   map[string]bool{"exec": true},
						"paired":        true,
						"connected":     true,
						"connectedAtMs": time.Now().UnixMilli(),
						"approvedAtMs":  time.Now().UnixMilli(),
					},
				})
			case "node.invoke":
				command := stringValue(params["command"])
				switch command {
				case "system.run.prepare":
					_ = conn.WriteJSON(map[string]any{
						"type": "res",
						"id":   reqID,
						"ok":   true,
						"payload": map[string]any{
							"ok":      true,
							"nodeId":  "node-1",
							"command": "system.run.prepare",
							"payload": map[string]any{
								"plan": map[string]any{
									"argv":        []string{"/bin/sh", "-lc", "echo hi"},
									"cwd":         "/tmp",
									"commandText": "echo hi",
								},
							},
						},
					})
				case "system.run":
					_ = conn.WriteJSON(map[string]any{
						"type": "res",
						"id":   reqID,
						"ok":   true,
						"payload": map[string]any{
							"ok":      true,
							"nodeId":  "node-1",
							"command": "system.run",
							"payload": map[string]any{
								"stdout":   "hi\n",
								"stderr":   "",
								"exitCode": 0,
							},
						},
					})
				default:
					_ = conn.WriteJSON(map[string]any{
						"type": "res",
						"id":   reqID,
						"ok":   true,
						"payload": map[string]any{
							"ok":      true,
							"nodeId":  "node-1",
							"command": command,
							"payload": map[string]any{
								"resolved": true,
							},
						},
					})
				}
			default:
				_ = conn.WriteJSON(map[string]any{
					"type": "res",
					"id":   reqID,
					"ok":   false,
					"error": map[string]any{
						"code":    "INVALID_REQUEST",
						"message": "unknown method",
					},
				})
			}
		}
	}))
	defer server.Close()

	backend, err := NewGatewayBackend(GatewayBackendConfig{
		URL:                "ws" + strings.TrimPrefix(server.URL, "http"),
		DeviceIdentityFile: filepath.Join(t.TempDir(), "device.json"),
		RequestTimeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewGatewayBackend failed: %v", err)
	}

	claims, err := backend.ListPendingClaims(context.Background())
	if err != nil {
		t.Fatalf("ListPendingClaims failed: %v", err)
	}
	if len(claims) != 1 || claims[0].PairingID != "pair-node" {
		t.Fatalf("unexpected claims: %+v", claims)
	}

	_, err = backend.ListPendingClaims(context.Background())
	if err != nil {
		t.Fatalf("second ListPendingClaims failed: %v", err)
	}

	device, nodes, err := backend.ApproveClaim(context.Background(), "pair-node")
	if err != nil {
		t.Fatalf("ApproveClaim failed: %v", err)
	}
	if device.DeviceID != "node-1" || len(nodes) != 1 || nodes[0].NodeID != "node-1" {
		t.Fatalf("unexpected approve result: device=%+v nodes=%+v", device, nodes)
	}

	listed, err := backend.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes failed: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("unexpected node list length: %+v", listed)
	}

	node, err := backend.DescribeNode(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("DescribeNode failed: %v", err)
	}
	if node.NodeID != "node-1" || !node.Connected {
		t.Fatalf("unexpected described node: %+v", node)
	}

	invoked, err := backend.InvokeNode(context.Background(), "node-1", "system.which", map[string]any{"name": "git"})
	if err != nil {
		t.Fatalf("InvokeNode failed: %v", err)
	}
	if !invoked.OK {
		t.Fatalf("unexpected invoke response: %+v", invoked)
	}

	run, err := backend.RunNode(context.Background(), "node-1", spec.FleetRunRequest{
		Command: []string{"echo", "hi"},
		CWD:     "/tmp",
		Env:     map[string]string{"FOO": "bar"},
	})
	if err != nil {
		t.Fatalf("RunNode failed: %v", err)
	}
	if !run.Accepted || run.Result["stdout"] != "hi\n" {
		t.Fatalf("unexpected run response: %+v", run)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(connectDeviceTokens) < 2 {
		t.Fatalf("expected multiple gateway connects, got %d", len(connectDeviceTokens))
	}
	if connectDeviceTokens[0] != "" {
		t.Fatalf("first connect should not use stored device token: %+v", connectDeviceTokens)
	}
	if connectDeviceTokens[1] != "stored-device-token" {
		t.Fatalf("second connect should reuse stored device token: %+v", connectDeviceTokens)
	}
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}
