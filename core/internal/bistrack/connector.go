package bistrack

import (
	"context"
	"errors"
)

// connector.go, the FROZEN BisTrack connector seam (DIB-53 phase 0). This is the whole boundary to the
// live BisTrack system. Phase 0 defines the interface and ships ONE implementation, unconfiguredConnector
//, whose every method fails closed. The W10 worker adds the real transport behind this same interface
// and never changes a signature (GL scaffold idiom).
//
// PULL + PUSH, both frozen now (supervisor C1). Push is not optional: the deployment strategy sells
// "rollback is free until November", which requires BisTrack to stay current through the 60-day parallel
// run while staff work in HardscapeOS. Without push that guarantee dies. Direction-of-truth per entity
// and per migration phase is recorded as typed data in direction.go.
//
// PAYLOAD SHAPES ARE PROVISIONAL. The BT* (pull) and Mapped* (push) shapes carry the fields the A-18
// recorded schema calls out today, but the WIRE FORMAT, REST envelope vs SQL Server result set, int vs
// string ids, field names, is not settled until the first live connection. Freeze the INTERFACE, expect
// the payload structs to gain fields. Adding a field is not a signature change.

// ErrUnconfigured is returned by every connector method until real BisTrack credentials + transport are
// wired (a later ticket). It is the fail-closed guarantee: no method can accidentally do a partial or
// live thing in phase 0.
var ErrUnconfigured = errors.New("bistrack: connector unconfigured, no live connection in phase 0")

// Connector is the seam to BisTrack. Fetch* pulls a page of rows STRICTLY AFTER `since` on the composite
// cursor (see Cursor) and returns the advanced cursor; Push* writes one mapped native entity back so
// BisTrack stays current during the parallel run.
//
// The four Fetch/Push pairs mirror the four reconciled entities. Signatures are FROZEN; only bodies (and,
// provisionally, the payload struct fields) change.
type Connector interface {
	// Name identifies the connector for logs + the status surface (e.g. "unconfigured", later "sqlserver").
	Name() string

	// -- pull (BisTrack → HardscapeOS): net-new + the initial mirror --
	FetchCustomers(ctx context.Context, since Cursor) ([]BTCustomer, Cursor, error)
	FetchProducts(ctx context.Context, since Cursor) ([]BTProduct, Cursor, error)
	FetchInventory(ctx context.Context, since Cursor) ([]BTInventory, Cursor, error)
	FetchOrders(ctx context.Context, since Cursor) ([]BTOrder, Cursor, error)

	// -- push (HardscapeOS → BisTrack): keep BisTrack current while staff work native --
	PushCustomer(ctx context.Context, c MappedCustomer) error
	PushProduct(ctx context.Context, p MappedProduct) error
	PushInventory(ctx context.Context, i MappedInventory) error
	PushOrder(ctx context.Context, o MappedOrder) error
}

// unconfiguredConnector is the only Connector phase 0 wires. Every method fails closed with
// ErrUnconfigured, there is no field it could be given to make it connect, which is the point: the
// module cannot sync until the real connector replaces it.
type unconfiguredConnector struct{}

// newUnconfiguredConnector is the phase-0 connector. The service holds it; nothing calls its methods
// (no route, no worker), it exists so the seam is real and its fail-closed behaviour is testable.
func newUnconfiguredConnector() Connector { return unconfiguredConnector{} }

func (unconfiguredConnector) Name() string { return "unconfigured" }

func (unconfiguredConnector) FetchCustomers(context.Context, Cursor) ([]BTCustomer, Cursor, error) {
	return nil, Cursor{}, ErrUnconfigured
}
func (unconfiguredConnector) FetchProducts(context.Context, Cursor) ([]BTProduct, Cursor, error) {
	return nil, Cursor{}, ErrUnconfigured
}
func (unconfiguredConnector) FetchInventory(context.Context, Cursor) ([]BTInventory, Cursor, error) {
	return nil, Cursor{}, ErrUnconfigured
}
func (unconfiguredConnector) FetchOrders(context.Context, Cursor) ([]BTOrder, Cursor, error) {
	return nil, Cursor{}, ErrUnconfigured
}
func (unconfiguredConnector) PushCustomer(context.Context, MappedCustomer) error {
	return ErrUnconfigured
}
func (unconfiguredConnector) PushProduct(context.Context, MappedProduct) error {
	return ErrUnconfigured
}
func (unconfiguredConnector) PushInventory(context.Context, MappedInventory) error {
	return ErrUnconfigured
}
func (unconfiguredConnector) PushOrder(context.Context, MappedOrder) error { return ErrUnconfigured }
