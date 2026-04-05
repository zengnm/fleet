package store

import (
	"context"
	"errors"
	"strings"

	"fleetd/pkg/spec"
)

var ErrNotFound = errors.New("not found")

type Store interface {
	UpsertTool(context.Context, spec.ToolSpec) error
	DeleteTool(context.Context, string, string) error
	GetTool(context.Context, string, string) (*spec.ToolSpec, error)
	ListTools(context.Context) ([]spec.ToolSpec, error)
	SearchTools(context.Context, string, int) ([]spec.ToolSpec, error)

	UpsertSecret(context.Context, spec.SecretSpec) error
	GetSecret(context.Context, string) (*spec.SecretSpec, error)

	UpsertMCPServer(context.Context, spec.MCPServerSpec) error
	GetMCPServer(context.Context, string) (*spec.MCPServerSpec, error)
	ListMCPServers(context.Context) ([]spec.MCPServerSpec, error)
	DeleteMCPServer(context.Context, string) error

	SaveInvocation(context.Context, spec.InvocationSession) error
	GetInvocation(context.Context, string) (*spec.InvocationSession, error)

	UpsertFleetPendingClaim(context.Context, spec.FleetPendingClaim) error
	GetFleetPendingClaim(context.Context, string) (*spec.FleetPendingClaim, error)
	ListFleetPendingClaims(context.Context) ([]spec.FleetPendingClaim, error)
	DeleteFleetPendingClaim(context.Context, string) error

	UpsertFleetOwnedDevice(context.Context, spec.FleetOwnedDevice) error
	GetFleetOwnedDevice(context.Context, string, string) (*spec.FleetOwnedDevice, error)
	FindFleetOwnedDeviceByDeviceID(context.Context, string) (*spec.FleetOwnedDevice, error)
	ListFleetOwnedDevices(context.Context, string) ([]spec.FleetOwnedDevice, error)
	DeleteFleetOwnedDevice(context.Context, string, string) error

	UpsertFleetOwnedNode(context.Context, spec.FleetOwnedNode) error
	GetFleetOwnedNode(context.Context, string, string) (*spec.FleetOwnedNode, error)
	FindFleetOwnedNodeByNodeID(context.Context, string) (*spec.FleetOwnedNode, error)
	ListFleetOwnedNodes(context.Context, string) ([]spec.FleetOwnedNode, error)
	ListFleetOwnedNodesByDevice(context.Context, string, string) ([]spec.FleetOwnedNode, error)
	DeleteFleetOwnedNode(context.Context, string, string) error
	DeleteFleetOwnedNodesByDevice(context.Context, string, string) error

	UpsertFleetNodeAuthState(context.Context, spec.FleetNodeAuthState) error
	GetFleetNodeAuthState(context.Context, string) (*spec.FleetNodeAuthState, error)
	DeleteFleetNodeAuthState(context.Context, string) error
}

func NormalizeTool(module, tool string) (string, string, string) {
	module = strings.TrimSpace(module)
	tool = strings.TrimSpace(tool)
	return module, tool, module + "/" + tool
}
