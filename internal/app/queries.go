package app

import (
	"context"

	"granary-phosphine-fumigation-closure/internal/arbitration"
	"granary-phosphine-fumigation-closure/internal/coverage"
	"granary-phosphine-fumigation-closure/internal/store"
	"granary-phosphine-fumigation-closure/internal/task"
)

// GetTask returns a task aggregate by number.
func (a *App) GetTask(ctx context.Context, number string) (task.FumigationTask, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return task.FumigationTask{}, err
	}
	defer tx.Rollback()
	return tx.Tasks().Load(ctx, number)
}

// ListTasks returns all tasks.
func (a *App) ListTasks(ctx context.Context) ([]task.FumigationTask, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return tx.Tasks().List(ctx)
}

// CoverageView is the deterministic evidence view for one task.
type CoverageView struct {
	TaskNumber   string
	Warehouse    string
	Cells        []coverage.CoverageCell
	Integrals    []coverage.ExposureIntegral
	Measurements []coverage.Measurement
}

// GetCoverage returns the persisted coverage cells, integrals and raw
// measurements for a task.
func (a *App) GetCoverage(ctx context.Context, number string) (CoverageView, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return CoverageView{}, err
	}
	defer tx.Rollback()
	t, err := tx.Tasks().Load(ctx, number)
	if err != nil {
		return CoverageView{}, err
	}
	cells, _ := tx.Coverage().ListCells(ctx, number)
	integrals, _ := tx.Coverage().ListIntegrals(ctx, number)
	meas, _ := tx.Coverage().ListMeasurements(ctx, number)
	return CoverageView{
		TaskNumber:   number,
		Warehouse:    t.WarehouseCode,
		Cells:        cells,
		Integrals:    integrals,
		Measurements: meas,
	}, nil
}

// GetLedger returns the dose ledger entries for a task.
func (a *App) GetLedger(ctx context.Context, number string) ([]coverage.DoseLedgerEntry, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return tx.Coverage().ListLedger(ctx, number)
}

// ListLeases returns all resource leases.
func (a *App) ListLeases(ctx context.Context) ([]store.ResourceLease, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return tx.Leases().ListLeases(ctx)
}

// ListDeviceCalls returns the device calls for a task.
func (a *App) ListDeviceCalls(ctx context.Context, number string) ([]store.DeviceCall, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return tx.Leases().ListDeviceCalls(ctx, number)
}

// GetLeakClosure returns the persisted risk closure for a task.
func (a *App) GetLeakClosure(ctx context.Context, number string) (arbitration.RiskClosure, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return arbitration.RiskClosure{}, err
	}
	defer tx.Rollback()
	return tx.Arbitration().GetClosure(ctx, number)
}

// ListLeaks returns the leak evidence for a task.
func (a *App) ListLeaks(ctx context.Context, number string) ([]arbitration.LeakEvidence, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return tx.Arbitration().ListLeaks(ctx, number)
}

// ListVentilation returns the ventilation samples for a task.
func (a *App) ListVentilation(ctx context.Context, number string) ([]arbitration.VentilationEvidence, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return tx.Arbitration().ListVentilation(ctx, number)
}

// ListReviews returns the reentry reviews for a task.
func (a *App) ListReviews(ctx context.Context, number string) ([]arbitration.ReentryReview, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return tx.Arbitration().ListReviews(ctx, number)
}

// ListEvents returns the audit events for a task.
func (a *App) ListEvents(ctx context.Context, number string) ([]task.TaskEvent, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return tx.Tasks().ListEvents(ctx, number)
}

// ListMeasurements returns raw measurements for a task.
func (a *App) ListMeasurements(ctx context.Context, number string) ([]coverage.Measurement, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return tx.Coverage().ListMeasurements(ctx, number)
}

// VerifyConservation recomputes the batch conservation identity for every batch
// referenced by a task's snapshot and reports whether the ledger is conserved.
func (a *App) VerifyConservation(ctx context.Context, number string) (bool, error) {
	tx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	t, err := tx.Tasks().Load(ctx, number)
	if err != nil {
		return false, err
	}
	if t.Snapshot == nil {
		return true, nil
	}
	for _, b := range t.Snapshot.Batches {
		cur, err := tx.Catalog().GetBatch(ctx, b.Code)
		if err != nil {
			return false, err
		}
		if !cur.Balanced() {
			return false, nil
		}
	}
	return true, nil
}
