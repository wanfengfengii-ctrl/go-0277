package app

import (
	"context"
	"fmt"

	"granary-phosphine-fumigation-closure/internal/catalog"
	"granary-phosphine-fumigation-closure/internal/coverage"
	"granary-phosphine-fumigation-closure/internal/domain"
	"granary-phosphine-fumigation-closure/internal/store"
	"granary-phosphine-fumigation-closure/internal/task"
)

// MeasurementInput is one submitted raw concentration reading.
type MeasurementInput struct {
	PointCode     string
	LogicalSlot   int64
	Concentration int64
	Sequence      int64
}

// loadEngine reconstructs the coverage engine for a task from persisted cells
// and measurements, scoped to the current generation and supplement round.
func loadEngine(ctx context.Context, tx store.Tx, t task.FumigationTask) (*coverage.Engine, error) {
	snap := t.Snapshot
	eng := coverage.NewEngine(t.WarehouseCode, t.Generation, t.SupplementGeneration, snap.SamplingSlots, snap.SlotDurationSec, snap.SamplingPoints)
	cells, err := tx.Coverage().ListCells(ctx, t.Number)
	if err != nil {
		return nil, err
	}
	for _, c := range cells {
		if c.Generation == t.Generation && c.SupplementGeneration == t.SupplementGeneration {
			eng.LoadCell(c)
		}
	}
	meas, err := tx.Coverage().ListMeasurements(ctx, t.Number)
	if err != nil {
		return nil, err
	}
	for _, m := range meas {
		if m.Key.Generation == t.Generation && m.Key.SupplementGeneration == t.SupplementGeneration {
			eng.LoadMeasurement(m)
		}
	}
	return eng, nil
}

// SubmitMeasurements accepts a batch of raw concentration readings. Every
// reading is validated and, when valid and first, advances coverage. A
// conflicting reading rejects the whole batch without advancing coverage.
func (a *App) SubmitMeasurements(ctx context.Context, opID string, expectedVersion int64, taskNumber string, inputs []MeasurementInput) (task.FumigationTask, error) {
	dig := digest(inputs)
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
	if t.Status != domain.StatusExposureMaintain && t.Status != domain.StatusSupplementing {
		return task.FumigationTask{}, domain.NewError(domain.ErrMeasurementOutOfWindow, opID, t.Version)
	}
	if t.Version != expectedVersion {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}

	eng, err := loadEngine(ctx, tx, t)
	if err != nil {
		return task.FumigationTask{}, err
	}

	var hardReasons []domain.Reason
	var cellsToWrite []coverage.CoverageCell
	measurements := make([]coverage.Measurement, 0, len(inputs))

	for _, in := range inputs {
		m := coverage.Measurement{
			Key: coverage.MeasurementKey{
				TaskNumber:           t.Number,
				Generation:           t.Generation,
				SupplementGeneration: t.SupplementGeneration,
				PointCode:            in.PointCode,
				LogicalSlot:          in.LogicalSlot,
				Sequence:             in.Sequence,
			},
			Concentration: in.Concentration,
			ReceivedAt:    t.LogicalClock,
		}
		advanced, code := eng.Accept(m)
		m.Accepted = advanced
		m.RejectCode = code
		measurements = append(measurements, m)

		if code == domain.ErrMeasurementConflict {
			point := catalog.PointLookup(t.Snapshot.SamplingPoints)[in.PointCode]
			hardReasons = append(hardReasons, domain.Reason{
				WarehouseCode: t.WarehouseCode,
				ZoneCode:      point.Zone,
				LogicalSlot:   in.LogicalSlot,
				PointCode:     in.PointCode,
				Code:          code,
				Message:       "measurement conflicts with existing evidence",
			})
			continue
		}
		if advanced {
			point := catalog.PointLookup(t.Snapshot.SamplingPoints)[in.PointCode]
			cellsToWrite = append(cellsToWrite, coverage.CoverageCell{
				TaskNumber:           t.Number,
				WarehouseCode:        t.WarehouseCode,
				ZoneCode:             point.Zone,
				LogicalSlot:          in.LogicalSlot,
				PointCode:            in.PointCode,
				Concentration:        in.Concentration,
				Generation:           t.Generation,
				SupplementGeneration: t.SupplementGeneration,
			})
		}
	}

	if len(hardReasons) > 0 {
		e := domain.NewError(domain.ErrMeasurementConflict, opID, t.Version)
		e.Reasons = hardReasons
		return task.FumigationTask{}, e
	}

	for _, m := range measurements {
		if err := tx.Coverage().SaveMeasurement(ctx, m); err != nil {
			return task.FumigationTask{}, err
		}
	}
	for _, c := range cellsToWrite {
		if err := tx.Coverage().SaveCell(ctx, c); err != nil {
			return task.FumigationTask{}, err
		}
	}

	t.Version++
	t.LogicalClock++
	_ = appendEvent(repo, t, "submit-measurements", fmt.Sprintf("%d readings", len(inputs)))
	if err := repo.Save(ctx, t); err != nil {
		return task.FumigationTask{}, err
	}
	_ = recordReceipt(repo, taskNumber, opID, dig, versionString(t.Version))
	if err := tx.Commit(); err != nil {
		return task.FumigationTask{}, err
	}
	return t, nil
}

// zoneIntegrals integrates every zone and returns the per-zone accumulated CT.
func zoneIntegrals(ctx context.Context, tx store.Tx, t task.FumigationTask) (map[string]int64, error) {
	eng, err := loadEngine(ctx, tx, t)
	if err != nil {
		return nil, err
	}
	result := map[string]int64{}
	for _, z := range eng.Zones() {
		_, acc, complete, code := eng.Integrate(z)
		if code == domain.ErrMeasurementMissing {
			result[z] = -1
			continue
		}
		if code != domain.ErrNone {
			return nil, domain.NewError(code, "", t.Version)
		}
		if !complete {
			result[z] = -1
			continue
		}
		result[z] = acc
	}
	return result, nil
}

// CreateSupplement derives the deterministic supplement range from a complete
// coverage and integral result, creates one new supplement generation and
// advances the task to SUPPLEMENTING. Concurrent callers: only one wins.
func (a *App) CreateSupplement(ctx context.Context, opID string, expectedVersion int64, taskNumber string) (task.FumigationTask, error) {
	tx, err := a.beginWrite(ctx)
	if err != nil {
		return task.FumigationTask{}, err
	}
	defer tx.Rollback()

	repo := tx.Tasks()
	if applied, err := checkReceipt(repo, taskNumber, opID, digest("supplement")); err != nil {
		return task.FumigationTask{}, err
	} else if applied {
		return repo.Load(ctx, taskNumber)
	}

	t, err := loadTask(repo, taskNumber)
	if err != nil {
		return task.FumigationTask{}, err
	}
	if t.Status != domain.StatusExposureMaintain {
		return task.FumigationTask{}, domain.NewError(domain.ErrSupplementGenerationConflict, opID, t.Version)
	}
	if t.Version != expectedVersion {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}

	integrals, err := zoneIntegrals(ctx, tx, t)
	if err != nil {
		return task.FumigationTask{}, err
	}

	type supplement struct {
		zone string
		mass int64
	}
	var underDosed []supplement
	for _, z := range catalog.SortedZoneCodes(t.Snapshot.Zones) {
		acc, ok := integrals[z]
		if !ok || acc < 0 {
			return task.FumigationTask{}, domain.NewError(domain.ErrMeasurementMissing, opID, t.Version).
				AddReason(t.WarehouseCode, z, 0, "", domain.ErrMeasurementMissing, "coverage incomplete")
		}
		if acc >= t.Snapshot.TargetDoseCT {
			continue
		}
		zoneCap := catalog.ZoneLookup(t.Snapshot.Zones)[z].CapacityDm3
		mass, code := coverage.SupplementMassMg(t.Snapshot.TargetDoseCT, acc, zoneCap)
		if code != domain.ErrNone {
			return task.FumigationTask{}, domain.NewError(code, opID, t.Version)
		}
		if mass > 0 {
			underDosed = append(underDosed, supplement{zone: z, mass: mass})
		}
	}

	if len(underDosed) == 0 {
		// Nothing to supplement: remain in exposure maintenance.
		if err := tx.Commit(); err != nil {
			return task.FumigationTask{}, err
		}
		return t, nil
	}

	// Deduct from the first batch with enough available mass.
	batches := t.Snapshot.Batches
	var total int64
	for _, u := range underDosed {
		total += u.mass
	}
	deducted := false
	for _, b := range batches {
		if b.AvailableMg < total {
			continue
		}
		b.AvailableMg -= total
		b.ReservedMg += total
		if err := tx.Catalog().SaveBatch(ctx, b); err != nil {
			return task.FumigationTask{}, err
		}
		deducted = true
		break
	}
	if !deducted {
		return task.FumigationTask{}, domain.NewError(domain.ErrDoseMassImbalance, opID, t.Version).
			AddReason(t.WarehouseCode, "", 0, "", domain.ErrDoseMassImbalance, "insufficient mass for supplement")
	}

	t.SupplementGeneration++
	for _, u := range underDosed {
		if err := tx.Coverage().SaveLedger(ctx, coverage.DoseLedgerEntry{
			TaskNumber: t.Number, ZoneCode: u.zone, Generation: t.Generation,
			ReservedMg: u.mass, OperationID: opID,
		}); err != nil {
			return task.FumigationTask{}, err
		}
	}
	t.Status = domain.StatusSupplementing
	t.Version++
	t.LogicalClock++
	_ = appendEvent(repo, t, "create-supplement", fmt.Sprintf("%d zones", len(underDosed)))
	if err := repo.Save(ctx, t); err != nil {
		return task.FumigationTask{}, err
	}
	_ = recordReceipt(repo, taskNumber, opID, digest("supplement"), versionString(t.Version))
	if err := tx.Commit(); err != nil {
		return task.FumigationTask{}, err
	}
	return t, nil
}

// CompleteSupplement returns a supplemented task to exposure maintenance for a
// fresh measurement round under the new supplement generation.
func (a *App) CompleteSupplement(ctx context.Context, opID string, expectedVersion int64, taskNumber string) (task.FumigationTask, error) {
	tx, err := a.beginWrite(ctx)
	if err != nil {
		return task.FumigationTask{}, err
	}
	defer tx.Rollback()

	repo := tx.Tasks()
	if applied, err := checkReceipt(repo, taskNumber, opID, digest("complete-supplement")); err != nil {
		return task.FumigationTask{}, err
	} else if applied {
		return repo.Load(ctx, taskNumber)
	}

	t, err := loadTask(repo, taskNumber)
	if err != nil {
		return task.FumigationTask{}, err
	}
	if t.Status != domain.StatusSupplementing {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}
	if t.Version != expectedVersion {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}
	t.Status = domain.StatusExposureMaintain
	t.Version++
	t.LogicalClock++
	_ = appendEvent(repo, t, "complete-supplement", "")
	if err := repo.Save(ctx, t); err != nil {
		return task.FumigationTask{}, err
	}
	_ = recordReceipt(repo, taskNumber, opID, digest("complete-supplement"), versionString(t.Version))
	if err := tx.Commit(); err != nil {
		return task.FumigationTask{}, err
	}
	return t, nil
}

// StartVentilation checks that every zone is fully covered and dosed, the dose
// ledger is conserved and no leak closure is active, then advances to
// VENTILATING.
func (a *App) StartVentilation(ctx context.Context, opID string, expectedVersion int64, taskNumber string) (task.FumigationTask, error) {
	tx, err := a.beginWrite(ctx)
	if err != nil {
		return task.FumigationTask{}, err
	}
	defer tx.Rollback()

	repo := tx.Tasks()
	if applied, err := checkReceipt(repo, taskNumber, opID, digest("ventilate")); err != nil {
		return task.FumigationTask{}, err
	} else if applied {
		return repo.Load(ctx, taskNumber)
	}

	t, err := loadTask(repo, taskNumber)
	if err != nil {
		return task.FumigationTask{}, err
	}
	if t.Status != domain.StatusExposureMaintain {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}
	if t.Version != expectedVersion {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}

	// Leak closure must be closed before ventilation.
	if closure, err := tx.Arbitration().GetClosure(ctx, t.Number); err == nil && !closure.Closed {
		return task.FumigationTask{}, domain.NewError(domain.ErrLeakPropagationActive, opID, t.Version).
			AddReason(t.WarehouseCode, "", 0, "", domain.ErrLeakPropagationActive, "leak propagation closure is still active")
	}

	integrals, err := zoneIntegrals(ctx, tx, t)
	if err != nil {
		return task.FumigationTask{}, err
	}
	var reasons []domain.Reason
	for _, z := range catalog.SortedZoneCodes(t.Snapshot.Zones) {
		acc, ok := integrals[z]
		if !ok || acc < 0 {
			reasons = append(reasons, domain.Reason{WarehouseCode: t.WarehouseCode, ZoneCode: z, Code: domain.ErrMeasurementMissing, Message: "coverage incomplete"})
			continue
		}
		if acc < t.Snapshot.TargetDoseCT {
			reasons = append(reasons, domain.Reason{WarehouseCode: t.WarehouseCode, ZoneCode: z, Code: domain.ErrMeasurementMissing, Message: "dose below target"})
		}
	}
	if len(reasons) > 0 {
		e := domain.NewError(reasons[0].Code, opID, t.Version)
		e.Reasons = reasons
		return task.FumigationTask{}, e
	}

	// Ledger conservation check.
	for _, b := range t.Snapshot.Batches {
		cur, err := tx.Catalog().GetBatch(ctx, b.Code)
		if err != nil {
			return task.FumigationTask{}, err
		}
		if !cur.Balanced() {
			return task.FumigationTask{}, domain.NewError(domain.ErrDoseMassImbalance, opID, t.Version).
				AddReason(t.WarehouseCode, "", 0, b.Code, domain.ErrDoseMassImbalance, "batch mass not conserved")
		}
	}

	t.Status = domain.StatusVentilating
	t.Version++
	t.LogicalClock++
	_ = appendEvent(repo, t, "start-ventilation", "")
	if err := repo.Save(ctx, t); err != nil {
		return task.FumigationTask{}, err
	}
	_ = recordReceipt(repo, taskNumber, opID, digest("ventilate"), versionString(t.Version))
	if err := tx.Commit(); err != nil {
		return task.FumigationTask{}, err
	}
	return t, nil
}
