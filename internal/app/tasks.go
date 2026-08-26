package app

import (
	"context"
	"fmt"

	"granary-phosphine-fumigation-closure/internal/catalog"
	"granary-phosphine-fumigation-closure/internal/coverage"
	"granary-phosphine-fumigation-closure/internal/domain"
	"granary-phosphine-fumigation-closure/internal/lease"
	"granary-phosphine-fumigation-closure/internal/store"
	"granary-phosphine-fumigation-closure/internal/task"
)

// LockRequest is the normalised input for locking a task.
type LockRequest struct {
	GrainType     catalog.GrainType
	StackHeightDm int64
	Summary       catalog.Summary
}

// CreateTask creates a PENDING_LOCK task for a warehouse.
func (a *App) CreateTask(ctx context.Context, warehouseCode string) (task.FumigationTask, error) {
	tx, err := a.beginWrite(ctx)
	if err != nil {
		return task.FumigationTask{}, err
	}
	defer tx.Rollback()

	if _, err := tx.Catalog().GetWarehouse(ctx, warehouseCode); err != nil {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, "", 0).
			AddReason(warehouseCode, "", 0, "", domain.ErrOperationContentConflict, "warehouse not found")
	}
	tasks, err := tx.Tasks().List(ctx)
	if err != nil {
		return task.FumigationTask{}, err
	}
	number := fmt.Sprintf("%s-%04d", warehouseCode, len(tasks)+1)

	t := task.FumigationTask{
		Number:        number,
		WarehouseCode: warehouseCode,
		Version:       1,
		Status:        domain.StatusPendingLock,
		Generation:    1,
		LogicalClock:  1,
		CreatedAt:     1,
	}
	if err := tx.Tasks().Save(ctx, t); err != nil {
		return task.FumigationTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return task.FumigationTask{}, err
	}
	return t, nil
}

// buildSnapshot freezes every lock-time fact into an immutable snapshot.
func buildSnapshot(ctx context.Context, repo store.CatalogRepo, w catalog.Warehouse, r catalog.FumigationRule, grain catalog.GrainType, heightDm int64, gen int64, now domain.LogicalTime) (task.Snapshot, error) {
	capacity, code := coverage.ConvertCapacity(w.RatedCapacityDm3, r.CapacityFactor)
	if code != domain.ErrNone {
		return task.Snapshot{}, domain.NewError(code, "", 0)
	}
	batches, err := repo.ListBatches(ctx)
	if err != nil {
		return task.Snapshot{}, err
	}
	return task.Snapshot{
		Summary:          catalog.Summarize(w, r),
		GrainType:        grain,
		StackHeightDm:    heightDm,
		CapacityDm3:      capacity,
		Zones:            w.Zones,
		Edges:            w.Edges,
		Devices:          w.Devices,
		SamplingPoints:   w.SamplingPoints,
		Batches:          batches,
		TargetDoseCT:     r.TargetDoseCT,
		SamplingSlots:    r.SamplingWindowSlots,
		SlotDurationSec:  r.SlotDurationSec,
		LeakThreshold:    r.LeakThreshold,
		ReentryThreshold: r.ReentryThreshold,
		SafeSlots:        r.SafeContinuousSlots,
		RetryMaxAttempts: r.RetryMaxAttempts,
		RetryBaseDelay:   r.RetryBaseDelaySlots,
		Generation:       gen,
		LockedAt:         now,
	}, nil
}

// LockTask validates grain, stack height and summary freshness, then freezes
// the immutable snapshot and advances the task to AIRTIGHT_CHECKING. Any
// mismatch leaves the task in PENDING_LOCK.
func (a *App) LockTask(ctx context.Context, opID string, expectedVersion int64, taskNumber string, req LockRequest) (task.FumigationTask, error) {
	dig := digest(req)
	tx, err := a.beginWrite(ctx)
	if err != nil {
		return task.FumigationTask{}, err
	}
	defer tx.Rollback()

	repo := tx.Tasks()
	if applied, err := checkReceipt(repo, taskNumber, opID, dig); err != nil {
		return task.FumigationTask{}, err
	} else if applied {
		return repo.Load(ctx, taskNumber)
	}

	t, err := loadTask(repo, taskNumber)
	if err != nil {
		return task.FumigationTask{}, err
	}
	if t.Status != domain.StatusPendingLock {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}
	if t.Version != expectedVersion {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}

	w, err := tx.Catalog().GetWarehouse(ctx, t.WarehouseCode)
	if err != nil {
		return task.FumigationTask{}, err
	}
	r, err := latestRule(ctx, tx.Catalog())
	if err != nil {
		return task.FumigationTask{}, err
	}
	if reasons := catalog.ValidateLock(w, r, req.GrainType, req.StackHeightDm, req.Summary); len(reasons) > 0 {
		e := domain.NewError(reasons[0].Code, opID, t.Version)
		e.Reasons = reasons
		return task.FumigationTask{}, e
	}

	snap, err := buildSnapshot(ctx, tx.Catalog(), w, r, req.GrainType, req.StackHeightDm, t.Generation, t.LogicalClock)
	if err != nil {
		return task.FumigationTask{}, err
	}

	t.Snapshot = &snap
	from := t.Status
	t.Status = domain.StatusAirtightChecking
	t.Version++
	_ = appendEvent(repo, t, "lock", string(snap.Summary.Digest))
	if err := repo.Save(ctx, t); err != nil {
		return task.FumigationTask{}, err
	}
	_ = recordReceipt(repo, taskNumber, opID, dig, versionString(t.Version))
	if err := tx.Commit(); err != nil {
		return task.FumigationTask{}, err
	}
	_ = from
	return t, nil
}

// ApplicationPlan is one zone's dosing plan for a batch.
type ApplicationPlan struct {
	ZoneCode  string
	BatchCode string
	MassMg    int64
}

// StartApplication acquires the dosing cage, fan circuit and sampling-line
// leases, reserves the requested mass and advances the task to APPLYING, all
// in one transaction.
func (a *App) StartApplication(ctx context.Context, opID string, expectedVersion int64, taskNumber string, plans []ApplicationPlan) (task.FumigationTask, error) {
	dig := digest(plans)
	tx, err := a.beginWrite(ctx)
	if err != nil {
		return task.FumigationTask{}, err
	}
	defer tx.Rollback()

	repo := tx.Tasks()
	if applied, err := checkReceipt(repo, taskNumber, opID, dig); err != nil {
		return task.FumigationTask{}, err
	} else if applied {
		return repo.Load(ctx, taskNumber)
	}

	t, err := loadTask(repo, taskNumber)
	if err != nil {
		return task.FumigationTask{}, err
	}
	if t.Status != domain.StatusAirtightChecking {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}
	if t.Version != expectedVersion {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}
	snap := t.Snapshot
	if snap == nil {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}

	w, err := tx.Catalog().GetWarehouse(ctx, t.WarehouseCode)
	if err != nil {
		return task.FumigationTask{}, err
	}
	lm := lease.NewManager(tx.Leases(), t.LogicalClock)
	// Acquire every dosing cage, fan circuit and sampling line.
	for _, d := range w.Devices {
		kind := string(d.Kind)
		if _, code := lm.Acquire(kind, d.Code, t.Number, 1000); code != domain.ErrNone {
			return task.FumigationTask{}, domain.NewError(domain.ErrResourceLeaseConflict, opID, t.Version).
				AddReason(t.WarehouseCode, "", 0, d.Code, domain.ErrResourceLeaseConflict, "resource lease conflict")
		}
	}

	// Reserve mass and record the application plan.
	batches := catalog.BatchLookup(snap.Batches)
	now := t.LogicalClock
	for _, p := range plans {
		b, ok := batches[p.BatchCode]
		if !ok {
			return task.FumigationTask{}, domain.NewError(domain.ErrDoseMassImbalance, opID, t.Version).
				AddReason(t.WarehouseCode, p.ZoneCode, 0, "", domain.ErrDoseMassImbalance, "batch not in snapshot")
		}
		if p.MassMg < 0 || b.AvailableMg < p.MassMg {
			return task.FumigationTask{}, domain.NewError(domain.ErrDoseMassImbalance, opID, t.Version).
				AddReason(t.WarehouseCode, p.ZoneCode, 0, "", domain.ErrDoseMassImbalance, "insufficient batch mass")
		}
		b.AvailableMg -= p.MassMg
		b.ReservedMg += p.MassMg
		if err := tx.Catalog().SaveBatch(ctx, b); err != nil {
			return task.FumigationTask{}, err
		}
		if err := tx.Coverage().SaveLedger(ctx, coverage.DoseLedgerEntry{
			TaskNumber: t.Number, BatchCode: p.BatchCode, ZoneCode: p.ZoneCode,
			Generation: t.Generation, ReservedMg: p.MassMg, OperationID: opID,
		}); err != nil {
			return task.FumigationTask{}, err
		}
		if err := tx.Coverage().SaveApplication(ctx, coverage.ApplicationRecord{
			TaskNumber: t.Number, BatchCode: p.BatchCode, ZoneCode: p.ZoneCode,
			Generation: t.Generation, MassMg: p.MassMg, Applied: false,
		}); err != nil {
			return task.FumigationTask{}, err
		}
	}

	t.Status = domain.StatusApplying
	t.Version++
	t.LogicalClock = now + 1
	_ = appendEvent(repo, t, "start-application", fmt.Sprintf("%d plans", len(plans)))
	if err := repo.Save(ctx, t); err != nil {
		return task.FumigationTask{}, err
	}
	_ = recordReceipt(repo, taskNumber, opID, dig, versionString(t.Version))
	if err := tx.Commit(); err != nil {
		return task.FumigationTask{}, err
	}
	return t, nil
}

// RecordApplication confirms one zone's application, moving reserved mass into
// applied mass. It is idempotent per plan key.
func (a *App) RecordApplication(ctx context.Context, opID string, expectedVersion int64, taskNumber, zoneCode, batchCode string, massMg int64) (task.FumigationTask, error) {
	req := struct {
		ZoneCode  string
		BatchCode string
		MassMg    int64
	}{zoneCode, batchCode, massMg}
	dig := digest(req)
	tx, err := a.beginWrite(ctx)
	if err != nil {
		return task.FumigationTask{}, err
	}
	defer tx.Rollback()

	repo := tx.Tasks()
	if applied, err := checkReceipt(repo, taskNumber, opID, dig); err != nil {
		return task.FumigationTask{}, err
	} else if applied {
		return repo.Load(ctx, taskNumber)
	}

	t, err := loadTask(repo, taskNumber)
	if err != nil {
		return task.FumigationTask{}, err
	}
	if t.Status != domain.StatusApplying {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}
	if t.Version != expectedVersion {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}

	b, err := tx.Catalog().GetBatch(ctx, batchCode)
	if err != nil {
		return task.FumigationTask{}, err
	}
	b.ReservedMg -= massMg
	b.AppliedMg += massMg
	if err := tx.Catalog().SaveBatch(ctx, b); err != nil {
		return task.FumigationTask{}, err
	}
	if err := tx.Coverage().SaveLedger(ctx, coverage.DoseLedgerEntry{
		TaskNumber: t.Number, BatchCode: batchCode, ZoneCode: zoneCode,
		Generation: t.Generation, AppliedMg: massMg, OperationID: opID,
	}); err != nil {
		return task.FumigationTask{}, err
	}
	t.Version++
	t.LogicalClock++
	_ = appendEvent(repo, t, "record-application", zoneCode)
	if err := repo.Save(ctx, t); err != nil {
		return task.FumigationTask{}, err
	}
	_ = recordReceipt(repo, taskNumber, opID, dig, versionString(t.Version))
	if err := tx.Commit(); err != nil {
		return task.FumigationTask{}, err
	}
	return t, nil
}

// SwitchCirculation selects one fan circuit and advances the task from
// APPLYING to EXPOSURE_MAINTAINING. Mutually exclusive circuits cannot run in
// parallel within the same task.
func (a *App) SwitchCirculation(ctx context.Context, opID string, expectedVersion int64, taskNumber, circuitCode string) (task.FumigationTask, error) {
	dig := digest(circuitCode)
	tx, err := a.beginWrite(ctx)
	if err != nil {
		return task.FumigationTask{}, err
	}
	defer tx.Rollback()

	repo := tx.Tasks()
	if applied, err := checkReceipt(repo, taskNumber, opID, dig); err != nil {
		return task.FumigationTask{}, err
	} else if applied {
		return repo.Load(ctx, taskNumber)
	}

	t, err := loadTask(repo, taskNumber)
	if err != nil {
		return task.FumigationTask{}, err
	}
	if t.Status != domain.StatusApplying {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}
	if t.Version != expectedVersion {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}

	devices := catalog.DeviceLookup(t.Snapshot.Devices)
	circuit, ok := devices[circuitCode]
	if !ok || circuit.Kind != catalog.DeviceFanCircuit {
		return task.FumigationTask{}, domain.NewError(domain.ErrFanCircuitConflict, opID, t.Version).
			AddReason(t.WarehouseCode, "", 0, circuitCode, domain.ErrFanCircuitConflict, "unknown fan circuit")
	}

	lm := lease.NewManager(tx.Leases(), t.LogicalClock)
	for _, other := range circuit.MutuallyExclusiveWith {
		if other == circuitCode {
			continue
		}
		if lm.IsRunning(string(catalog.DeviceFanCircuit), other) {
			return task.FumigationTask{}, domain.NewError(domain.ErrFanCircuitConflict, opID, t.Version).
				AddReason(t.WarehouseCode, "", 0, other, domain.ErrFanCircuitConflict, "mutually exclusive circuit already running")
		}
	}

	if _, code := lm.Acquire(string(catalog.DeviceFanCircuit), circuitCode, t.Number, 1000); code != domain.ErrNone {
		return task.FumigationTask{}, domain.NewError(domain.ErrResourceLeaseConflict, opID, t.Version).
			AddReason(t.WarehouseCode, "", 0, circuitCode, domain.ErrResourceLeaseConflict, "circuit lease conflict")
	}
	lm.SetRunning(string(catalog.DeviceFanCircuit), circuitCode, true)
	for _, other := range circuit.MutuallyExclusiveWith {
		if other != circuitCode {
			lm.SetRunning(string(catalog.DeviceFanCircuit), other, false)
		}
	}

	t.Status = domain.StatusExposureMaintain
	t.Version++
	t.LogicalClock++
	_ = appendEvent(repo, t, "switch-circulation", circuitCode)
	if err := repo.Save(ctx, t); err != nil {
		return task.FumigationTask{}, err
	}
	_ = recordReceipt(repo, taskNumber, opID, dig, versionString(t.Version))
	if err := tx.Commit(); err != nil {
		return task.FumigationTask{}, err
	}
	return t, nil
}
