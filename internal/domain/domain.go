// Package domain defines the stable, shared building blocks used across the
// granary phosphine fumigation closure system: error codes, ordered reasons,
// logical time, task status, and the canonical ordering comparators required
// by the documented failure boundaries.
package domain

import "sort"

// ErrorCode is a stable, machine-readable failure identifier. Every business
// rejection produced by the system maps to one of the codes listed in the
// approved project document's failure boundaries.
type ErrorCode string

const (
	ErrNone                         ErrorCode = ""
	ErrWarehouseCapacityMismatch    ErrorCode = "WAREHOUSE_CAPACITY_MISMATCH"
	ErrGrainTypeMismatch            ErrorCode = "GRAIN_TYPE_MISMATCH"
	ErrRuleSnapshotStale            ErrorCode = "RULE_SNAPSHOT_STALE"
	ErrResourceLeaseConflict        ErrorCode = "RESOURCE_LEASE_CONFLICT"
	ErrFanCircuitConflict           ErrorCode = "FAN_CIRCUIT_CONFLICT"
	ErrDoseMassImbalance            ErrorCode = "DOSE_MASS_IMBALANCE"
	ErrMeasurementMissing           ErrorCode = "MEASUREMENT_MISSING"
	ErrMeasurementConflict          ErrorCode = "MEASUREMENT_CONFLICT"
	ErrMeasurementOutOfWindow       ErrorCode = "MEASUREMENT_OUT_OF_WINDOW"
	ErrMeasurementGenerationStale   ErrorCode = "MEASUREMENT_GENERATION_STALE"
	ErrFixedPointOverflow           ErrorCode = "FIXED_POINT_OVERFLOW"
	ErrDeviceRetryExhausted         ErrorCode = "DEVICE_RETRY_EXHAUSTED"
	ErrLeakPropagationActive        ErrorCode = "LEAK_PROPAGATION_ACTIVE"
	ErrSupplementGenerationConflict ErrorCode = "SUPPLEMENT_GENERATION_CONFLICT"
	ErrOperationContentConflict     ErrorCode = "OPERATION_CONTENT_CONFLICT"
	ErrTerminalAlreadyDecided       ErrorCode = "TERMINAL_ALREADY_DECIDED"
)

// Error is a stable business error. It carries the primary code, the
// operation id that triggered it, the aggregate version observed, and a
// deterministically sorted list of reasons.
type Error struct {
	Code             ErrorCode
	OperationID      string
	AggregateVersion int64
	Reasons          []Reason
}

func (e *Error) Error() string {
	return string(e.Code)
}

// Reason is a single ordered explanation for a rejection. Its sort key is
// derived from warehouse code, zone code, logical slot, sampling point and
// reason code, in that order.
type Reason struct {
	WarehouseCode string
	ZoneCode      string
	LogicalSlot   int64
	PointCode     string
	Code          ErrorCode
	Message       string
}

// SortReasons orders reasons by the stable ascending comparator mandated by
// the failure boundaries. The order is: warehouse code, zone code, logical
// slot, sampling point code, then reason code.
func SortReasons(rs []Reason) {
	sort.Slice(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if a.WarehouseCode != b.WarehouseCode {
			return a.WarehouseCode < b.WarehouseCode
		}
		if a.ZoneCode != b.ZoneCode {
			return a.ZoneCode < b.ZoneCode
		}
		if a.LogicalSlot != b.LogicalSlot {
			return a.LogicalSlot < b.LogicalSlot
		}
		if a.PointCode != b.PointCode {
			return a.PointCode < b.PointCode
		}
		return a.Code < b.Code
	})
}

// LogicalTime is the monotonic clock used for leases, sampling windows and
// device retries. It never depends on wall-clock races.
type LogicalTime int64

// TaskStatus enumerates the states of the fumigation task aggregate.
type TaskStatus string

const (
	StatusPendingLock      TaskStatus = "PENDING_LOCK"
	StatusAirtightChecking TaskStatus = "AIRTIGHT_CHECKING"
	StatusApplying         TaskStatus = "APPLYING"
	StatusExposureMaintain TaskStatus = "EXPOSURE_MAINTAINING"
	StatusSupplementing    TaskStatus = "SUPPLEMENTING"
	StatusLeakContaining   TaskStatus = "LEAK_CONTAINING"
	StatusVentilating      TaskStatus = "VENTILATING"
	StatusReentryReady     TaskStatus = "REENTRY_READY"
	StatusCompleted        TaskStatus = "COMPLETED"
	StatusRiskIsolated     TaskStatus = "RISK_ISOLATED"
	StatusCancelled        TaskStatus = "CANCELLED"
)

// IsTerminal reports whether the status is a final state that can never be
// changed again.
func (s TaskStatus) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusRiskIsolated, StatusCancelled:
		return true
	default:
		return false
	}
}
