// Package bistrack is the M9 BisTrack live-sync module (DIB-53). It keeps BisTrack and HardscapeOS
// reconciled across the fork so the deployment guarantee "rollback is free until November" holds: during
// the 60-day parallel run staff work in HardscapeOS, a PUSH keeps BisTrack current, and a PULL catches
// net-new BisTrack rows.
//
// PHASE 0 (this scaffold) is SCHEMA-FIRST and STRUCTURALLY UNABLE TO SYNC. It freezes the module's data
// shapes (model.go, shapes.go), the connector seam (connector.go, pull + push, both frozen), the pure
// mapping layer (mapping.go), the per-phase/per-entity direction-of-truth as data (direction.go), and the
// 0079 persistence (repository.go / migration 0079). It ships ONE connector, unconfiguredConnector -
// whose every method fails closed with ErrUnconfigured, and it wires that connector to NOTHING: no route,
// no worker, no scheduler. The only HTTP surface is a staff-gated GET status (handler.go). "Config-gated"
// here means the reported status flips to "unconfigured" without credentials; it does NOT mean a
// configured deploy would sync, phase 0 cannot, by construction. The structural guarantee is enforced by
// structural_test.go, not by a checklist.
//
// This follows the GL scaffold idiom (internal/gl/module.go): freeze signatures, bodies are stubs; the
// W10 worker fills the connector + adds a scheduler behind these same signatures and never changes one.
// GableLBM is an ARCHITECTURAL reference only, no code is copied from it (repo.md).
//
// The mapping source is the A-18 recorded BisTrack schema (docs/legacy/bistrack_map.json): per-entity
// dbo.* tables, domain_mappings (transformation rules + M9 direction-of-truth), and anomalies.
package bistrack
