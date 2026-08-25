// Package task implements the fumigation task aggregate: the full status
// state machine, the immutable lock snapshot, operation-id idempotency
// receipts, audit events and the single terminal decision point.
package task

import (
	"granary-phosphine-fumigation-closure/internal/catalog"
	"granary-phosphine-fumigation-closure/internal/domain"
)

// Snapshot is the immutable set of facts fixed at lock time. It captures the
// catalogue summary, grain, stack height, capacity conversion parameters,
// zone graph, device topology, pesticide batches, sampling times, thresholds,
// target dose and task generation. Once written it is never modified, so
// later catalogue changes cannot silently affect a locked task.
type Snapshot struct {
	Summary          catalog.Summary          `json:"summary"`
	GrainType        catalog.GrainType        `json:"grain_type"`
	StackHeightDm    int64                    `json:"stack_height_dm"`
	CapacityDm3      int64                    `json:"capacity_dm3"`
	Zones            []catalog.Zone           `json:"zones"`
	Edges            []catalog.NeighborEdge   `json:"edges"`
	Devices          []catalog.Device         `json:"devices"`
	SamplingPoints   []catalog.SamplingPoint  `json:"sampling_points"`
	Batches          []catalog.PesticideBatch `json:"batches"`
	TargetDoseCT     int64                    `json:"target_dose_ct"`
	SamplingSlots    int64                    `json:"sampling_slots"`
	SlotDurationSec  int64                    `json:"slot_duration_sec"`
	LeakThreshold    int64                    `json:"leak_threshold"`
	ReentryThreshold int64                    `json:"reentry_threshold"`
	SafeSlots        int64                    `json:"safe_slots"`
	RetryMaxAttempts int64                    `json:"retry_max_attempts"`
	RetryBaseDelay   int64                    `json:"retry_base_delay"`
	Generation       int64                    `json:"generation"`
	LockedAt         domain.LogicalTime       `json:"locked_at"`
}

// FumigationTask is the consistency boundary for a single warehouse
// fumigation closure. All mutations to status, dose, leases, coverage, risk
// and terminal decisions happen within one transaction over this aggregate.
type FumigationTask struct {
	Number               string             `json:"number"`
	WarehouseCode        string             `json:"warehouse_code"`
	Version              int64              `json:"version"`
	Status               domain.TaskStatus  `json:"status"`
	Snapshot             *Snapshot          `json:"snapshot"`
	Generation           int64              `json:"generation"`
	SupplementGeneration int64              `json:"supplement_generation"`
	LogicalClock         domain.LogicalTime `json:"logical_clock"`
	CreatedAt            domain.LogicalTime `json:"created_at"`
	Terminal             *TerminalDecision  `json:"terminal"`
}

// TaskEvent is an append-only audit record used for startup recovery checks.
type TaskEvent struct {
	Sequence       int64             `json:"sequence"`
	TaskNumber     string            `json:"task_number"`
	CommandSummary string            `json:"command_summary"`
	FromStatus     domain.TaskStatus `json:"from_status"`
	ToStatus       domain.TaskStatus `json:"to_status"`
	Payload        string            `json:"payload"`
}

// TerminalKind identifies the winning terminal decision.
type TerminalKind string

const (
	TerminalCompleted    TerminalKind = "COMPLETED"
	TerminalRiskIsolated TerminalKind = "RISK_ISOLATED"
	TerminalCancelled    TerminalKind = "CANCELLED"
)

// TerminalDecision is the single, compare-and-swap guarded terminal record.
type TerminalDecision struct {
	TaskNumber string             `json:"task_number"`
	Kind       TerminalKind       `json:"kind"`
	Version    int64              `json:"version"`
	Reason     domain.ErrorCode   `json:"reason"`
	Evidence   string             `json:"evidence"`
	DecidedAt  domain.LogicalTime `json:"decided_at"`
}

// Command is the normalised input for a state-changing operation. Every write
// carries an operation id and an expected aggregate version so idempotent
// retries and optimistic concurrency can be resolved deterministically.
type Command struct {
	OperationID      string
	AggregateVersion int64
	TaskNumber       string
	Payload          string
}

// OperationReceipt records the idempotent result of a command so that a
// retried operation returns the existing outcome instead of double-applying.
// It is scoped to a task: the same operation id on two different tasks are two
// independent operations.
type OperationReceipt struct {
	TaskNumber    string
	OperationID   string
	CommandDigest string
	Result        string
}
