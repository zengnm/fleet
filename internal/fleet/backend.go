package fleet

import (
	"context"
	"errors"

	"fleetd/pkg/spec"
)

var (
	ErrBackendUnavailable = errors.New("fleet backend is not configured")
	ErrClaimNotFound      = errors.New("claim not found")
	ErrClaimConfirmation  = errors.New("device id does not match")
	ErrForbidden          = errors.New("node is not owned by the current user")
	ErrNodeNotFound       = errors.New("node not found")
	ErrNodeOffline        = errors.New("node is offline")
	ErrApprovalRequired   = errors.New("command requires approval")
)

type Backend interface {
	ListPendingClaims(context.Context) ([]spec.FleetPendingClaim, error)
	ApproveClaim(context.Context, string) (spec.FleetOwnedDevice, []spec.FleetOwnedNode, error)
	RejectClaim(context.Context, string) error
	UnclaimDevice(context.Context, string) error
	ListNodes(context.Context) ([]spec.FleetOwnedNode, error)
	DescribeNode(context.Context, string) (spec.FleetOwnedNode, error)
	InvokeNode(context.Context, string, string, map[string]any) (spec.FleetInvokeResponse, error)
	RunNode(context.Context, string, spec.FleetRunRequest) (spec.FleetRunResponse, error)
}

type NoopBackend struct{}

func (NoopBackend) ListPendingClaims(context.Context) ([]spec.FleetPendingClaim, error) {
	return nil, ErrBackendUnavailable
}

func (NoopBackend) ApproveClaim(context.Context, string) (spec.FleetOwnedDevice, []spec.FleetOwnedNode, error) {
	return spec.FleetOwnedDevice{}, nil, ErrBackendUnavailable
}

func (NoopBackend) RejectClaim(context.Context, string) error {
	return ErrBackendUnavailable
}

func (NoopBackend) UnclaimDevice(context.Context, string) error {
	return ErrBackendUnavailable
}

func (NoopBackend) ListNodes(context.Context) ([]spec.FleetOwnedNode, error) {
	return nil, ErrBackendUnavailable
}

func (NoopBackend) DescribeNode(context.Context, string) (spec.FleetOwnedNode, error) {
	return spec.FleetOwnedNode{}, ErrBackendUnavailable
}

func (NoopBackend) InvokeNode(context.Context, string, string, map[string]any) (spec.FleetInvokeResponse, error) {
	return spec.FleetInvokeResponse{}, ErrBackendUnavailable
}

func (NoopBackend) RunNode(context.Context, string, spec.FleetRunRequest) (spec.FleetRunResponse, error) {
	return spec.FleetRunResponse{}, ErrBackendUnavailable
}
