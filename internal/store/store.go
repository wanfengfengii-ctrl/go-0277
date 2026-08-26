// Package store declares the persistence boundaries for the fumigation
// closure system. Every command that changes task, dose, lease, coverage,
// risk or terminal state executes inside a single transaction over these
// repositories, so partial state is impossible.
//
// The production implementation is an embedded bbolt database; tests may
// supply an in-memory implementation through the same interface.
package store

import (
	"context"

	"granary-phosphine-fumigation-closure/internal/arbitration"
	"granary-phosphine-fumigation-closure/internal/catalog"
	"granary-phosphine-fumigation-closure/internal/coverage"
	"granary-phosphine-fumigation-closure/internal/domain"
	"granary-phosphine-fumigation-closure/internal/task"
)

// Tx is a single atomic unit of work. Callers commit or roll back the tx and
// must not retain repository references across the boundary.
type Tx interface {
	Commit() error
	Rollback() error
	Catalog() CatalogRepo
	Tasks() TaskRepo
	Leases() LeaseRepo
	Coverage() CoverageRepo
	Arbitration() ArbitrationRepo
}

// RecoverReport summarises the startup migration and recovery scan so the
// entry point can log which tasks were resumed and which were quarantined.
type RecoverReport struct {
	Migrated         bool
	RecoveredTasks   []string
	QuarantinedTasks []string
}

// Store is the top-level persistence façade.
type Store interface {
	Begin(ctx context.Context, writable bool) (Tx, error)
	Recover(ctx context.Context) (RecoverReport, error)
	Close() error
}

// CatalogRepo persists warehouses, rules, pesticide batches and summaries.
type CatalogRepo interface {
	SaveWarehouse(ctx context.Context, w catalog.Warehouse) error
	GetWarehouse(ctx context.Context, code string) (catalog.Warehouse, error)
	ListWarehouses(ctx context.Context) ([]catalog.Warehouse, error)
	SaveRule(ctx context.Context, r catalog.FumigationRule) error
	GetRule(ctx context.Context, version int64) (catalog.FumigationRule, error)
	ListRules(ctx context.Context) ([]catalog.FumigationRule, error)
	SaveBatch(ctx context.Context, b catalog.PesticideBatch) error
	GetBatch(ctx context.Context, code string) (catalog.PesticideBatch, error)
	ListBatches(ctx context.Context) ([]catalog.PesticideBatch, error)
}

// TaskRepo persists the task aggregate, events and operation receipts.
type TaskRepo interface {
	Save(ctx context.Context, t task.FumigationTask) error
	Load(ctx context.Context, number string) (task.FumigationTask, error)
	List(ctx context.Context) ([]task.FumigationTask, error)
	AppendEvent(ctx context.Context, e task.TaskEvent) error
	ListEvents(ctx context.Context, taskNumber string) ([]task.TaskEvent, error)
	SaveReceipt(ctx context.Context, r task.OperationReceipt) error
	GetReceipt(ctx context.Context, taskNumber, operationID string) (task.OperationReceipt, error)
}

// ResourceLease is the persisted, mutually exclusive occupancy record. The
// resource key (kind + code) is unique, so a second acquisition for an
// already-leased resource fails at the storage layer.
type ResourceLease struct {
	ResourceKind string
	ResourceCode string
	TaskNumber   string
	Version      int64
	AcquiredAt   domain.LogicalTime
	ExpiresAt    domain.LogicalTime
	FrozenReason string
	// Running marks a fan circuit that is currently circulating. Within one
	// task, mutually exclusive circuits cannot both be running.
	Running bool
}

// DeviceCall is a persisted, retriable device invocation. Failed calls are
// kept as durable intent so they can be resumed after a restart.
type DeviceCall struct {
	ID          string
	DeviceCode  string
	Kind        string
	TaskNumber  string
	Attempts    int64
	NextAt      domain.LogicalTime
	MaxAttempts int64
	Result      string
	FailureKind string
	Completed   bool
}

// LeaseRepo persists leases and device-call retry queues.
type LeaseRepo interface {
	SaveLease(ctx context.Context, l ResourceLease) error
	GetLease(ctx context.Context, kind, code string) (ResourceLease, error)
	ListLeases(ctx context.Context) ([]ResourceLease, error)
	SaveDeviceCall(ctx context.Context, c DeviceCall) error
	GetDeviceCall(ctx context.Context, id string) (DeviceCall, error)
	ListDeviceCalls(ctx context.Context, taskNumber string) ([]DeviceCall, error)
	ListDueDeviceCalls(ctx context.Context, now domain.LogicalTime) ([]DeviceCall, error)
}

// CoverageRepo persists measurements, coverage cells, integrals, the dose
// ledger and application records.
type CoverageRepo interface {
	SaveMeasurement(ctx context.Context, m coverage.Measurement) error
	ListMeasurements(ctx context.Context, taskNumber string) ([]coverage.Measurement, error)
	SaveCell(ctx context.Context, c coverage.CoverageCell) error
	ListCells(ctx context.Context, taskNumber string) ([]coverage.CoverageCell, error)
	SaveIntegral(ctx context.Context, i coverage.ExposureIntegral) error
	ListIntegrals(ctx context.Context, taskNumber string) ([]coverage.ExposureIntegral, error)
	SaveLedger(ctx context.Context, e coverage.DoseLedgerEntry) error
	ListLedger(ctx context.Context, taskNumber string) ([]coverage.DoseLedgerEntry, error)
	SaveApplication(ctx context.Context, a coverage.ApplicationRecord) error
	ListApplications(ctx context.Context, taskNumber string) ([]coverage.ApplicationRecord, error)
}

// ArbitrationRepo persists leak, ventilation, reentry and terminal evidence.
type ArbitrationRepo interface {
	SaveLeak(ctx context.Context, e arbitration.LeakEvidence) error
	ListLeaks(ctx context.Context, taskNumber string) ([]arbitration.LeakEvidence, error)
	SaveClosure(ctx context.Context, c arbitration.RiskClosure) error
	GetClosure(ctx context.Context, taskNumber string) (arbitration.RiskClosure, error)
	SaveVentilation(ctx context.Context, v arbitration.VentilationEvidence) error
	ListVentilation(ctx context.Context, taskNumber string) ([]arbitration.VentilationEvidence, error)
	SaveReview(ctx context.Context, r arbitration.ReentryReview) error
	ListReviews(ctx context.Context, taskNumber string) ([]arbitration.ReentryReview, error)
	SaveTerminal(ctx context.Context, t task.TerminalDecision) error
	GetTerminal(ctx context.Context, taskNumber string) (task.TerminalDecision, error)
}
