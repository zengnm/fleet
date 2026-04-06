package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	if normalizedClaimConfirmation(deviceIDConfirmation) != normalizedClaimConfirmation(claim.DeviceID) {
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
		alias, err := s.ensureNodeAlias(ctx, userID, nodes[index])
		if err != nil {
			return spec.FleetOwnedDevice{}, nil, err
		}
		nodes[index].NodeID = alias
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

func (s *Service) UnclaimNode(ctx context.Context, userID, nodeID string) error {
	if strings.TrimSpace(userID) == "" {
		return errors.New("user id is required")
	}
	node, err := s.store.GetFleetOwnedNode(ctx, userID, nodeID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNodeNotFound
		}
		return err
	}
	if err := s.backend.UnclaimDevice(ctx, node.DeviceID); err != nil && !errors.Is(err, store.ErrNotFound) && !errors.Is(err, ErrBackendUnavailable) {
		return err
	}
	if err := s.store.DeleteFleetOwnedNodesByDevice(ctx, userID, node.DeviceID); err != nil {
		return err
	}
	if err := s.store.DeleteFleetOwnedDevice(ctx, userID, node.DeviceID); err != nil && !errors.Is(err, store.ErrNotFound) {
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
			liveByID[backendNodeID(node)] = node
		}
	}
	now := time.Now().UTC()
	for index := range owned {
		if live, ok := liveByID[backendNodeID(owned[index])]; ok {
			owned[index] = normalizeOwnedNode(userID, owned[index].DeviceID, mergeOwnedNode(owned[index], live), now)
			alias, aliasErr := s.ensureNodeAlias(ctx, userID, owned[index])
			if aliasErr != nil {
				return nil, aliasErr
			}
			owned[index].NodeID = alias
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
	live, err := s.backend.DescribeNode(ctx, backendNodeID(*owned))
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
	alias, err := s.ensureNodeAlias(ctx, userID, merged)
	if err != nil {
		return spec.FleetOwnedNode{}, err
	}
	merged.NodeID = alias
	if err := s.store.UpsertFleetOwnedNode(ctx, merged); err != nil {
		return spec.FleetOwnedNode{}, err
	}
	return merged, nil
}

func (s *Service) InvokeNode(ctx context.Context, userID, nodeID, command string, params map[string]any) (spec.FleetInvokeResponse, error) {
	owned, err := s.store.GetFleetOwnedNode(ctx, userID, nodeID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return spec.FleetInvokeResponse{}, ErrNodeNotFound
		}
		return spec.FleetInvokeResponse{}, err
	}
	target := backendNodeID(*owned)
	result, err := s.backend.InvokeNode(ctx, target, command, params)
	if errors.Is(err, store.ErrNotFound) {
		return spec.FleetInvokeResponse{}, ErrNodeOffline
	}
	result.NodeID = owned.NodeID
	return result, err
}

func (s *Service) RunNode(ctx context.Context, userID, nodeID string, request spec.FleetRunRequest) (spec.FleetRunResponse, error) {
	owned, err := s.store.GetFleetOwnedNode(ctx, userID, nodeID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return spec.FleetRunResponse{}, ErrNodeNotFound
		}
		return spec.FleetRunResponse{}, err
	}
	target := backendNodeID(*owned)
	result, err := s.backend.RunNode(ctx, target, request)
	if errors.Is(err, store.ErrNotFound) {
		return spec.FleetRunResponse{}, ErrNodeOffline
	}
	result.NodeID = owned.NodeID
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
	if node.BackendNodeID == "" {
		node.BackendNodeID = firstNonEmpty(node.NodeID, deviceID)
	}
	if node.NodeID == "" {
		node.NodeID = node.BackendNodeID
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
		DeviceID:      claim.DeviceID,
		NodeID:        claim.DeviceID,
		BackendNodeID: claim.DeviceID,
		DisplayName:   claim.DisplayName,
		Platform:      claim.Platform,
		ClientID:      claim.ClientID,
		ClientMode:    claim.ClientMode,
		DeviceFamily:  claim.DeviceFamily,
		Status:        "online",
		Paired:        true,
		Connected:     true,
		ConnectedAt:   now,
		ApprovedAt:    now,
		LastSeenAt:    now,
		UpdatedAt:     now,
	}
}

func mergeOwnedNode(stored, live spec.FleetOwnedNode) spec.FleetOwnedNode {
	merged := stored
	if live.BackendNodeID != "" {
		merged.BackendNodeID = live.BackendNodeID
	} else if merged.BackendNodeID == "" {
		merged.BackendNodeID = live.NodeID
	}
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

func normalizedClaimConfirmation(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func ClaimDisplayPrefix(deviceID string) string {
	prefix, _ := claimDisplayParts(deviceID)
	return prefix
}

func backendNodeID(node spec.FleetOwnedNode) string {
	return firstNonEmpty(node.BackendNodeID, node.NodeID, node.DeviceID)
}

func (s *Service) ensureNodeAlias(ctx context.Context, userID string, node spec.FleetOwnedNode) (string, error) {
	existing, err := s.store.ListFleetOwnedNodes(ctx, userID)
	if err != nil {
		return "", err
	}
	currentTarget := backendNodeID(node)
	candidate := normalizedNodeAlias(node)
	for _, item := range existing {
		if item.NodeID == candidate && backendNodeID(item) == currentTarget {
			return candidate, nil
		}
	}
	base := candidate
	suffix := 2
	for {
		conflict := false
		for _, item := range existing {
			if item.NodeID != candidate {
				continue
			}
			if backendNodeID(item) == currentTarget {
				return candidate, nil
			}
			conflict = true
			break
		}
		if !conflict {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", trimAliasBase(base, 28), suffix)
		suffix++
	}
}

func normalizedNodeAlias(node spec.FleetOwnedNode) string {
	actual := backendNodeID(node)
	if isSimpleNodeID(actual) && len(actual) <= 24 {
		return actual
	}
	base := sanitizeNodeAlias(node.DisplayName)
	if base == "" {
		base = "node"
	}
	return trimAliasBase(base, 24) + "-" + shortNodeHash(actual)
}

func sanitizeNodeAlias(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		isLower := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if isLower || isDigit {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func trimAliasBase(value string, limit int) string {
	value = strings.Trim(strings.TrimSpace(value), "-")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return strings.TrimRight(value[:limit], "-")
}

func shortNodeHash(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])[:6]
}

func isSimpleNodeID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func claimDisplayParts(deviceID string) (string, string) {
	value := strings.ToUpper(strings.TrimSpace(deviceID))
	if value == "" {
		return "", ""
	}
	split := len(value) / 2
	if split == 0 {
		return "", value
	}
	return value[:split], value[split:]
}
