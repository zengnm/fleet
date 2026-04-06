package fleet

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fleetd/internal/store"
	"fleetd/pkg/spec"
)

func TestApproveClaimUsesFullDeviceIDCaseInsensitive(t *testing.T) {
	t.Parallel()

	const deviceID = "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"

	testCases := []struct {
		name         string
		confirmation string
		wantErr      error
	}{
		{name: "exact", confirmation: deviceID},
		{name: "case insensitive", confirmation: " " + strings.ToUpper(deviceID) + " "},
		{name: "prefix only", confirmation: "abcdef12", wantErr: ErrClaimConfirmation},
		{name: "suffix only", confirmation: "34567890", wantErr: ErrClaimConfirmation},
		{name: "empty", confirmation: "", wantErr: ErrClaimConfirmation},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			backing, err := store.NewSQLite(ctx, "file:"+filepath.Join(t.TempDir(), "fleet.db")+"?_pragma=busy_timeout(5000)")
			if err != nil {
				t.Fatalf("new sqlite store: %v", err)
			}
			backend := NewMemoryBackend()
			backend.SeedPendingClaim(spec.FleetPendingClaim{
				PairingID:    "pair-1",
				DeviceID:     deviceID,
				DisplayName:  "Build Node",
				Status:       "pending",
				RequestedAt:  time.Now().UTC(),
				UpdatedAt:    time.Now().UTC(),
				Platform:     "darwin",
				DeviceFamily: "desktop",
				ClientID:     "openclaw-node",
				ClientMode:   "headless",
				Role:         "node",
			})
			service := NewService(backing, backend)

			_, _, err = service.ApproveClaim(ctx, "user-a", "pair-1", testCase.confirmation)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("ApproveClaim error = %v, want %v", err, testCase.wantErr)
			}
			if testCase.wantErr == nil {
				device, err := backing.GetFleetOwnedDevice(ctx, "user-a", deviceID)
				if err != nil {
					t.Fatalf("get owned device: %v", err)
				}
				if device.DeviceID != deviceID {
					t.Fatalf("unexpected device id: %s", device.DeviceID)
				}
				nodes, err := backing.ListFleetOwnedNodes(ctx, "user-a")
				if err != nil {
					t.Fatalf("list owned nodes: %v", err)
				}
				if len(nodes) != 1 {
					t.Fatalf("expected 1 owned node, got %d", len(nodes))
				}
				if nodes[0].NodeID == nodes[0].DeviceID || len(nodes[0].NodeID) >= len(nodes[0].DeviceID) {
					t.Fatalf("expected shortened node id, got node_id=%q device_id=%q", nodes[0].NodeID, nodes[0].DeviceID)
				}
			}
		})
	}
}

func TestApproveClaimGeneratesUniqueShortNodeIDsPerUser(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backing, err := store.NewSQLite(ctx, "file:"+filepath.Join(t.TempDir(), "fleet.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	backend := NewMemoryBackend()
	service := NewService(backing, backend)

	now := time.Now().UTC()
	for index, deviceID := range []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	} {
		backend.SeedPendingClaim(spec.FleetPendingClaim{
			PairingID:    fmt.Sprintf("pair-%d", index),
			DeviceID:     deviceID,
			DisplayName:  "Build Node",
			Status:       "pending",
			RequestedAt:  now,
			UpdatedAt:    now,
			Platform:     "darwin",
			DeviceFamily: "desktop",
			ClientID:     "openclaw-node",
			ClientMode:   "headless",
			Role:         "node",
		})
		if _, _, err := service.ApproveClaim(ctx, "user-a", fmt.Sprintf("pair-%d", index), deviceID); err != nil {
			t.Fatalf("ApproveClaim %d: %v", index, err)
		}
	}

	nodes, err := backing.ListFleetOwnedNodes(ctx, "user-a")
	if err != nil {
		t.Fatalf("ListFleetOwnedNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	if nodes[0].NodeID == nodes[1].NodeID {
		t.Fatalf("expected unique node ids, got %+v", nodes)
	}
}

func TestUnclaimNodeRemovesDeviceOwnership(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backing, err := store.NewSQLite(ctx, "file:"+filepath.Join(t.TempDir(), "fleet.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	backend := NewMemoryBackend()
	service := NewService(backing, backend)

	now := time.Now().UTC()
	deviceID := "abcdef1234567890"
	if err := backing.UpsertFleetOwnedDevice(ctx, spec.FleetOwnedDevice{
		UserID:       "user-a",
		DeviceID:     deviceID,
		DisplayName:  "Build Node",
		Platform:     "darwin",
		DeviceFamily: "desktop",
		ClientID:     "openclaw-node",
		ClientMode:   "headless",
		Role:         "node",
		UpdatedAt:    now,
		ApprovedAt:   now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert owned device: %v", err)
	}
	for _, nodeID := range []string{"node-1", "node-2"} {
		if err := backing.UpsertFleetOwnedNode(ctx, spec.FleetOwnedNode{
			UserID:       "user-a",
			DeviceID:     deviceID,
			NodeID:       nodeID,
			DisplayName:  "Build Node",
			Platform:     "darwin",
			DeviceFamily: "desktop",
			Status:       "online",
			Connected:    true,
			UpdatedAt:    now,
			ApprovedAt:   now,
			LastSeenAt:   now,
		}); err != nil {
			t.Fatalf("upsert owned node %s: %v", nodeID, err)
		}
		backend.SeedNode(spec.FleetOwnedNode{
			DeviceID:     deviceID,
			NodeID:       nodeID,
			DisplayName:  "Build Node",
			Platform:     "darwin",
			DeviceFamily: "desktop",
			Status:       "online",
			Connected:    true,
			UpdatedAt:    now,
			LastSeenAt:   now,
		})
	}

	if err := service.UnclaimNode(ctx, "user-a", "node-1"); err != nil {
		t.Fatalf("UnclaimNode: %v", err)
	}

	if _, err := backing.GetFleetOwnedDevice(ctx, "user-a", deviceID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetFleetOwnedDevice error = %v, want not found", err)
	}
	nodes, err := backing.ListFleetOwnedNodes(ctx, "user-a")
	if err != nil {
		t.Fatalf("ListFleetOwnedNodes: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected owned nodes to be deleted, got %+v", nodes)
	}

	claims, err := service.ListClaims(ctx)
	if err != nil {
		t.Fatalf("ListClaims: %v", err)
	}
	if len(claims) != 1 || claims[0].DeviceID != deviceID {
		t.Fatalf("unexpected claims after unclaim: %+v", claims)
	}
}
