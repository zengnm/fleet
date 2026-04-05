package fleet

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"fleetd/internal/store"
	"fleetd/pkg/spec"
)

type Service struct {
	store   store.Store
	backend Backend
}

func NewService(backing store.Store, backend Backend) *Service {
	if backend == nil {
		backend = NoopBackend{}
	}
	return &Service{store: backing, backend: backend}
}

func (s *Service) SetBackend(backend Backend) {
	if backend == nil {
		backend = NoopBackend{}
	}
	s.backend = backend
}

func (s *Service) ListClaims(ctx context.Context) ([]spec.FleetPendingClaim, error) {
	claims, err := s.backend.ListPendingClaims(ctx)
	if err != nil {
		if errors.Is(err, ErrBackendUnavailable) {
			return s.store.ListFleetPendingClaims(ctx)
		}
		return nil, err
	}
	if err := s.replacePendingClaims(ctx, claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func (s *Service) ApproveClaim(ctx context.Context, userID, pairingID, deviceIDConfirmation string) (spec.FleetOwnedDevice, []spec.FleetOwnedNode, error) {
	if strings.TrimSpace(userID) == "" {
		return spec.FleetOwnedDevice{}, nil, errors.New("user id is required")
	}
	claim, err := s.getPendingClaim(ctx, pairingID)
	if err != nil {
		return spec.FleetOwnedDevice{}, nil, err
	}
	if normalizedClaimConfirmation(deviceIDConfirmation) != claimConfirmationSuffix(claim.DeviceID) {
		return spec.FleetOwnedDevice{}, nil, ErrClaimConfirmation
	}
	if existing, err := s.store.FindFleetOwnedDeviceByDeviceID(ctx, claim.DeviceID); err == nil && existing.UserID != userID {
		return spec.FleetOwnedDevice{}, nil, ErrForbidden
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return spec.FleetOwnedDevice{}, nil, err
	}

	device, nodes, err := s.backend.ApproveClaim(ctx, pairingID)
	if err != nil {
		return spec.FleetOwnedDevice{}, nil, err
	}
	now := time.Now().UTC()
	device.UserID = userID
	if device.DeviceID == "" {
		device.DeviceID = claim.DeviceID
	}
	if device.DisplayName == "" {
		device.DisplayName = claim.DisplayName
	}
	if device.ApprovedAt.IsZero() {
		device.ApprovedAt = now
	}
	if device.LastSeenAt.IsZero() {
		device.LastSeenAt = now
	}
	device.UpdatedAt = now
	if err := s.store.UpsertFleetOwnedDevice(ctx, device); err != nil {
		return spec.FleetOwnedDevice{}, nil, err
	}
	if err := s.store.DeleteFleetPendingClaim(ctx, pairingID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return spec.FleetOwnedDevice{}, nil, err
	}

	if len(nodes) == 0 {
		nodes = []spec.FleetOwnedNode{placeholderNodeFromClaim(claim)}
	}
	for index := range nodes {
		nodes[index] = normalizeOwnedNode(userID, device.DeviceID, nodes[index], now)
		if err := s.store.UpsertFleetOwnedNode(ctx, nodes[index]); err != nil {
			return spec.FleetOwnedDevice{}, nil, err
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	return device, nodes, nil
}

func (s *Service) RejectClaim(ctx context.Context, pairingID string) error {
	if err := s.backend.RejectClaim(ctx, pairingID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrClaimNotFound
		}
		return err
	}
	if err := s.store.DeleteFleetPendingClaim(ctx, pairingID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return nil
}

func (s *Service) ListNodes(ctx context.Context, userID string) ([]spec.FleetOwnedNode, error) {
	owned, err := s.store.ListFleetOwnedNodes(ctx, userID)
	if err != nil {
		return nil, err
	}
	liveNodes, liveErr := s.backend.ListNodes(ctx)
	liveByID := map[string]spec.FleetOwnedNode{}
	if liveErr == nil {
		for _, node := range liveNodes {
			liveByID[node.NodeID] = node
		}
	}
	now := time.Now().UTC()
	for index := range owned {
		if live, ok := liveByID[owned[index].NodeID]; ok {
			owned[index] = normalizeOwnedNode(userID, owned[index].DeviceID, mergeOwnedNode(owned[index], live), now)
			_ = s.store.UpsertFleetOwnedNode(ctx, owned[index])
			continue
		}
		owned[index] = markNodeOffline(userID, owned[index], now)
		_ = s.store.UpsertFleetOwnedNode(ctx, owned[index])
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].NodeID < owned[j].NodeID })
	if len(owned) == 0 && liveErr != nil && !errors.Is(liveErr, ErrBackendUnavailable) {
		return nil, liveErr
	}
	return owned, nil
}

func (s *Service) GetNode(ctx context.Context, userID, nodeID string) (spec.FleetOwnedNode, error) {
	owned, err := s.store.GetFleetOwnedNode(ctx, userID, nodeID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return spec.FleetOwnedNode{}, ErrNodeNotFound
		}
		return spec.FleetOwnedNode{}, err
	}
	live, err := s.backend.DescribeNode(ctx, nodeID)
	if err != nil {
		if errors.Is(err, ErrBackendUnavailable) || errors.Is(err, store.ErrNotFound) {
			offline := markNodeOffline(userID, *owned, time.Now().UTC())
			if upsertErr := s.store.UpsertFleetOwnedNode(ctx, offline); upsertErr != nil {
				return spec.FleetOwnedNode{}, upsertErr
			}
			return offline, nil
		}
		return spec.FleetOwnedNode{}, err
	}
	merged := normalizeOwnedNode(userID, owned.DeviceID, mergeOwnedNode(*owned, live), time.Now().UTC())
	if err := s.store.UpsertFleetOwnedNode(ctx, merged); err != nil {
		return spec.FleetOwnedNode{}, err
	}
	return merged, nil
}

func (s *Service) InvokeNode(ctx context.Context, userID, nodeID, command string, params map[string]any) (spec.FleetInvokeResponse, error) {
	if _, err := s.store.GetFleetOwnedNode(ctx, userID, nodeID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return spec.FleetInvokeResponse{}, ErrNodeNotFound
		}
		return spec.FleetInvokeResponse{}, err
	}
	result, err := s.backend.InvokeNode(ctx, nodeID, command, params)
	if errors.Is(err, store.ErrNotFound) {
		return spec.FleetInvokeResponse{}, ErrNodeOffline
	}
	return result, err
}

func (s *Service) RunNode(ctx context.Context, userID, nodeID string, request spec.FleetRunRequest) (spec.FleetRunResponse, error) {
	if _, err := s.store.GetFleetOwnedNode(ctx, userID, nodeID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return spec.FleetRunResponse{}, ErrNodeNotFound
		}
		return spec.FleetRunResponse{}, err
	}
	result, err := s.backend.RunNode(ctx, nodeID, request)
	if errors.Is(err, store.ErrNotFound) {
		return spec.FleetRunResponse{}, ErrNodeOffline
	}
	return result, err
}

func (s *Service) replacePendingClaims(ctx context.Context, claims []spec.FleetPendingClaim) error {
	existing, err := s.store.ListFleetPendingClaims(ctx)
	if err != nil {
		return err
	}
	seen := map[string]struct{}{}
	now := time.Now().UTC()
	for _, claim := range claims {
		if claim.UpdatedAt.IsZero() {
			claim.UpdatedAt = now
		}
		if claim.RequestedAt.IsZero() {
			claim.RequestedAt = claim.UpdatedAt
		}
		if claim.Status == "" {
			claim.Status = "pending"
		}
		seen[claim.PairingID] = struct{}{}
		if err := s.store.UpsertFleetPendingClaim(ctx, claim); err != nil {
			return err
		}
	}
	for _, claim := range existing {
		if _, ok := seen[claim.PairingID]; ok {
			continue
		}
		if err := s.store.DeleteFleetPendingClaim(ctx, claim.PairingID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	return nil
}

func (s *Service) getPendingClaim(ctx context.Context, pairingID string) (*spec.FleetPendingClaim, error) {
	claims, err := s.backend.ListPendingClaims(ctx)
	if err == nil {
		if syncErr := s.replacePendingClaims(ctx, claims); syncErr != nil {
			return nil, syncErr
		}
	}
	claim, err := s.store.GetFleetPendingClaim(ctx, pairingID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrClaimNotFound
		}
		return nil, err
	}
	return claim, nil
}

func normalizeOwnedNode(userID, deviceID string, node spec.FleetOwnedNode, now time.Time) spec.FleetOwnedNode {
	node.UserID = userID
	if node.DeviceID == "" {
		node.DeviceID = deviceID
	}
	if node.NodeID == "" {
		node.NodeID = deviceID
	}
	if node.Status == "" {
		if node.Connected {
			node.Status = "online"
		} else {
			node.Status = "offline"
		}
	}
	if node.LastSeenAt.IsZero() {
		node.LastSeenAt = now
	}
	if node.UpdatedAt.IsZero() {
		node.UpdatedAt = now
	} else {
		node.UpdatedAt = now
	}
	return node
}

func placeholderNodeFromClaim(claim *spec.FleetPendingClaim) spec.FleetOwnedNode {
	now := time.Now().UTC()
	return spec.FleetOwnedNode{
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
}

func mergeOwnedNode(stored, live spec.FleetOwnedNode) spec.FleetOwnedNode {
	merged := stored
	if live.DisplayName != "" {
		merged.DisplayName = live.DisplayName
	}
	if live.Platform != "" {
		merged.Platform = live.Platform
	}
	if live.Version != "" {
		merged.Version = live.Version
	}
	if live.CoreVersion != "" {
		merged.CoreVersion = live.CoreVersion
	}
	if live.UIVersion != "" {
		merged.UIVersion = live.UIVersion
	}
	if live.ClientID != "" {
		merged.ClientID = live.ClientID
	}
	if live.ClientMode != "" {
		merged.ClientMode = live.ClientMode
	}
	if live.RemoteIP != "" {
		merged.RemoteIP = live.RemoteIP
	}
	if live.DeviceFamily != "" {
		merged.DeviceFamily = live.DeviceFamily
	}
	if live.ModelID != "" {
		merged.ModelID = live.ModelID
	}
	if live.PathEnv != "" {
		merged.PathEnv = live.PathEnv
	}
	if len(live.Caps) > 0 {
		merged.Caps = live.Caps
	}
	if len(live.Commands) > 0 {
		merged.Commands = live.Commands
	}
	if len(live.Permissions) > 0 {
		merged.Permissions = live.Permissions
	}
	merged.Status = live.Status
	merged.Paired = live.Paired || stored.Paired
	merged.Connected = live.Connected
	if !live.ConnectedAt.IsZero() {
		merged.ConnectedAt = live.ConnectedAt
	}
	if !live.ApprovedAt.IsZero() {
		merged.ApprovedAt = live.ApprovedAt
	}
	if !live.LastSeenAt.IsZero() {
		merged.LastSeenAt = live.LastSeenAt
	}
	return merged
}

func markNodeOffline(userID string, node spec.FleetOwnedNode, now time.Time) spec.FleetOwnedNode {
	node = normalizeOwnedNode(userID, node.DeviceID, node, now)
	node.Connected = false
	node.Status = "offline"
	node.UpdatedAt = now
	return node
}

func claimConfirmationSuffix(deviceID string) string {
	value := strings.ToUpper(strings.TrimSpace(deviceID))
	if len(value) > 6 {
		return value[len(value)-6:]
	}
	return value
}

func normalizedClaimConfirmation(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}
