package fleetd

import (
	"testing"
	"time"

	"fleetd/pkg/spec"
)

func TestDecodeInvokePlanAcceptsCurrentNodePayload(t *testing.T) {
	t.Parallel()

	plan, err := decodeInvokePlan(spec.FleetInvokeResponse{
		PayloadJSON: `{"cmdText":"/usr/bin/uname -a","plan":{"argv":["/usr/bin/uname","-a"],"cwd":null,"rawCommand":"/usr/bin/uname -a","agentId":null,"sessionKey":null}}`,
	})
	if err != nil {
		t.Fatalf("decodeInvokePlan failed: %v", err)
	}
	if len(plan.Argv) != 2 || plan.Argv[0] != "/usr/bin/uname" || plan.Argv[1] != "-a" {
		t.Fatalf("unexpected argv: %#v", plan.Argv)
	}
	if plan.CommandText != "/usr/bin/uname -a" {
		t.Fatalf("unexpected command text: %q", plan.CommandText)
	}
}

func TestNodeInvokeTimeoutUsesParamsTimeout(t *testing.T) {
	t.Parallel()

	got := nodeInvokeTimeout(2*time.Second, map[string]any{"timeoutMs": float64(20000)})
	if got != 20*time.Second {
		t.Fatalf("timeout = %s, want 20s", got)
	}
}
