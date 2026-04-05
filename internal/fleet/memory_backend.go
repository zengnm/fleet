package fleet

import (
	"context"
	"sort"
	"sync"
	"time"

	"fleetd/internal/store"
	"fleetd/pkg/spec"
)

type MemoryBackend struct {
	mu      sync.Mutex
	claims  map[string]spec.FleetPendingClaim
	devices map[string]spec.FleetOwnedDevice
	nodes   map[string]spec.FleetOwnedNode
}

func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{
		claims:  map[string]spec.FleetPendingClaim{},
		devices: map[string]spec.FleetOwnedDevice{},
		nodes:   map[string]spec.FleetOwnedNode{},
	}
}

func (b *MemoryBackend) SeedPendingClaim(claim spec.FleetPendingClaim) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if claim.UpdatedAt.IsZero() {
		claim.UpdatedAt = time.Now().UTC()
	}
	if claim.RequestedAt.IsZero() {
		claim.RequestedAt = claim.UpdatedAt
	}
	if claim.Status == "" {
		claim.Status = "pending"
	}
	b.claims[claim.PairingID] = claim
}

func (b *MemoryBackend) SeedNode(node spec.FleetOwnedNode) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if node.UpdatedAt.IsZero() {
		node.UpdatedAt = time.Now().UTC()
	}
	if node.Status == "" {
		node.Status = "online"
	}
	b.nodes[node.NodeID] = node
}

func (b *MemoryBackend) ListPendingClaims(context.Context) ([]spec.FleetPendingClaim, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	claims := make([]spec.FleetPendingClaim, 0, len(b.claims))
	for _, claim := range b.claims {
		claims = append(claims, claim)
	}
	sort.Slice(claims, func(i, j int) bool {
		return claims[i].RequestedAt.After(claims[j].RequestedAt)
	})
	return claims, nil
}

func (b *MemoryBackend) ApproveClaim(_ context.Context, pairingID string) (spec.FleetOwnedDevice, []spec.FleetOwnedNode, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	claim, ok := b.claims[pairingID]
	if !ok {
		return spec.FleetOwnedDevice{}, nil, store.ErrNotFound
	}
	delete(b.claims, pairingID)

	now := time.Now().UTC()
	device := spec.FleetOwnedDevice{
		DeviceID:     claim.DeviceID,
		DisplayName:  claim.DisplayName,
		Platform:     claim.Platform,
		DeviceFamily: claim.DeviceFamily,
		ClientID:     claim.ClientID,
		ClientMode:   claim.ClientMode,
		Role:         claim.Role,
		RemoteIP:     claim.RemoteIP,
		TokenState:   "paired",
		ApprovedAt:   now,
		LastSeenAt:   now,
		UpdatedAt:    now,
	}
	b.devices[device.DeviceID] = device

	nodes := make([]spec.FleetOwnedNode, 0)
	for _, node := range b.nodes {
		if node.DeviceID == claim.DeviceID || node.NodeID == claim.DeviceID {
			node.DeviceID = claim.DeviceID
			node.ApprovedAt = now
			node.LastSeenAt = now
			node.UpdatedAt = now
			if node.Status == "" {
				node.Status = "online"
			}
			if !node.Connected {
				node.Connected = true
			}
			b.nodes[node.NodeID] = node
			nodes = append(nodes, node)
		}
	}
	if len(nodes) == 0 {
		node := spec.FleetOwnedNode{
			DeviceID:     claim.DeviceID,
			NodeID:       claim.DeviceID,
			DisplayName:  claim.DisplayName,
			Platform:     claim.Platform,
			ClientID:     claim.ClientID,
			ClientMode:   claim.ClientMode,
			DeviceFamily: claim.DeviceFamily,
			Status:       "online",
			Paired:       true,
			Connected:    true,
			ConnectedAt:  now,
			ApprovedAt:   now,
			LastSeenAt:   now,
			UpdatedAt:    now,
		}
		b.nodes[node.NodeID] = node
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].NodeID < nodes[j].NodeID
	})
	return device, nodes, nil
}

func (b *MemoryBackend) RejectClaim(_ context.Context, pairingID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.claims[pairingID]; !ok {
		return store.ErrNotFound
	}
	delete(b.claims, pairingID)
	return nil
}

func (b *MemoryBackend) ListNodes(context.Context) ([]spec.FleetOwnedNode, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	nodes := make([]spec.FleetOwnedNode, 0, len(b.nodes))
	for _, node := range b.nodes {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].NodeID < nodes[j].NodeID
	})
	return nodes, nil
}

func (b *MemoryBackend) DescribeNode(_ context.Context, nodeID string) (spec.FleetOwnedNode, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	node, ok := b.nodes[nodeID]
	if !ok {
		return spec.FleetOwnedNode{}, store.ErrNotFound
	}
	return node, nil
}

func (b *MemoryBackend) InvokeNode(_ context.Context, nodeID, command string, params map[string]any) (spec.FleetInvokeResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.nodes[nodeID]; !ok {
		return spec.FleetInvokeResponse{}, store.ErrNotFound
	}
	return spec.FleetInvokeResponse{
		NodeID:  nodeID,
		Command: command,
		OK:      true,
		Payload: map[string]any{
			"echo": params,
		},
	}, nil
}

func (b *MemoryBackend) RunNode(_ context.Context, nodeID string, request spec.FleetRunRequest) (spec.FleetRunResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.nodes[nodeID]; !ok {
		return spec.FleetRunResponse{}, store.ErrNotFound
	}
	return spec.FleetRunResponse{
		NodeID:   nodeID,
		Accepted: true,
		Result: map[string]any{
			"command": request.Command,
			"cwd":     request.CWD,
			"env":     request.Env,
		},
	}, nil
}
