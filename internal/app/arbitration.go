package app

import (
	"context"
	"fmt"

	"granary-phosphine-fumigation-closure/internal/arbitration"
	"granary-phosphine-fumigation-closure/internal/catalog"
	"granary-phosphine-fumigation-closure/internal/domain"
	"granary-phosphine-fumigation-closure/internal/lease"
	"granary-phosphine-fumigation-closure/internal/store"
	"granary-phosphine-fumigation-closure/internal/task"
)

// buildGraph converts the locked neighbour edges into the undirected graph
// used for leak propagation closure.
func buildGraph(edges []catalog.NeighborEdge) arbitration.Graph {
	g := arbitration.Graph{Edges: map[string][]string{}}
	for _, e := range edges {
		g.Edges[e.From] = append(g.Edges[e.From], e.To)
		g.Edges[e.To] = append(g.Edges[e.To], e.From)
	}
	return g
}

// LeakInput is a reported leak reading for a zone (or adjacent warehouse).
type LeakInput struct {
	SourceCode    string
	MeasuredValue int64
}

// ReportLeak records a leak reading. When it exceeds the threshold it computes
// the propagation closure over the locked neighbour graph, atomically freezes
// the related fan circuits and moves the task to LEAK_CONTAINING.
func (a *App) ReportLeak(ctx context.Context, opID string, expectedVersion int64, taskNumber string, in LeakInput) (task.FumigationTask, error) {
	dig := digest(in)
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
	if t.Status != domain.StatusExposureMaintain && t.Status != domain.StatusSupplementing && t.Status != domain.StatusApplying {
		return task.FumigationTask{}, domain.NewError(domain.ErrLeakPropagationActive, opID, t.Version)
	}
	if t.Version != expectedVersion {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}

	exceeded := in.MeasuredValue > t.Snapshot.LeakThreshold
	evidence := arbitration.LeakEvidence{
		TaskNumber:    t.Number,
		SourceCode:    in.SourceCode,
		MeasuredValue: in.MeasuredValue,
		Threshold:     t.Snapshot.LeakThreshold,
		Exceeded:      exceeded,
		RecordedAt:    t.LogicalClock,
	}
	if err := tx.Arbitration().SaveLeak(ctx, evidence); err != nil {
		return task.FumigationTask{}, err
	}

	if exceeded {
		graph := buildGraph(t.Snapshot.Edges)
		closure := graph.Closure([]string{in.SourceCode})

		lm := lease.NewManager(tx.Leases(), t.LogicalClock)
		var frozen []string
		for _, d := range t.Snapshot.Devices {
			if d.Kind == catalog.DeviceFanCircuit {
				lm.Freeze(string(d.Kind), d.Code, "leak propagation closure")
				frozen = append(frozen, d.Code)
			}
		}
		if err := tx.Arbitration().SaveClosure(ctx, arbitration.RiskClosure{
			TaskNumber:     t.Number,
			ClosureCodes:   closure,
			FrozenCircuits: frozen,
			FrozenAt:       t.LogicalClock,
			Closed:         false,
		}); err != nil {
			return task.FumigationTask{}, err
		}
		t.Status = domain.StatusLeakContaining
		t.Version++
		t.LogicalClock++
		_ = appendEvent(repo, t, "report-leak", fmt.Sprintf("closure=%v", closure))
		if err := repo.Save(ctx, t); err != nil {
			return task.FumigationTask{}, err
		}
		_ = recordReceipt(repo, taskNumber, opID, dig, versionString(t.Version))
		if err := tx.Commit(); err != nil {
			return task.FumigationTask{}, err
		}
		return t, nil
	}

	t.Version++
	t.LogicalClock++
	_ = recordReceipt(repo, taskNumber, opID, dig, versionString(t.Version))
	if err := repo.Save(ctx, t); err != nil {
		return task.FumigationTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return task.FumigationTask{}, err
	}
	return t, nil
}

// ResolveLeak marks the active risk closure as resolved and returns the task
// to exposure maintenance so ventilation can be reconsidered.
func (a *App) ResolveLeak(ctx context.Context, opID string, expectedVersion int64, taskNumber string) (task.FumigationTask, error) {
	tx, err := a.beginWrite(ctx)
	if err != nil {
		return task.FumigationTask{}, err
	}
	defer tx.Rollback()

	repo := tx.Tasks()
	if applied, err := checkReceipt(repo, taskNumber, opID, digest("resolve-leak")); err != nil {
		return task.FumigationTask{}, err
	} else if applied {
		return repo.Load(ctx, taskNumber)
	}

	t, err := loadTask(repo, taskNumber)
	if err != nil {
		return task.FumigationTask{}, err
	}
	if t.Status != domain.StatusLeakContaining {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}
	if t.Version != expectedVersion {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}
	closure, err := tx.Arbitration().GetClosure(ctx, t.Number)
	if err != nil {
		return task.FumigationTask{}, err
	}
	closure.Closed = true
	if err := tx.Arbitration().SaveClosure(ctx, closure); err != nil {
		return task.FumigationTask{}, err
	}
	t.Status = domain.StatusExposureMaintain
	t.Version++
	t.LogicalClock++
	_ = appendEvent(repo, t, "resolve-leak", "")
	if err := repo.Save(ctx, t); err != nil {
		return task.FumigationTask{}, err
	}
	_ = recordReceipt(repo, taskNumber, opID, digest("resolve-leak"), versionString(t.Version))
	if err := tx.Commit(); err != nil {
		return task.FumigationTask{}, err
	}
	return t, nil
}

// VentilationInput is one safe-window sample during ventilation.
type VentilationInput struct {
	PointCode     string
	LogicalSlot   int64
	Concentration int64
}

// SubmitVentilation persists safe-window samples during ventilation.
func (a *App) SubmitVentilation(ctx context.Context, opID string, expectedVersion int64, taskNumber string, samples []VentilationInput) (task.FumigationTask, error) {
	dig := digest(samples)
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
	if t.Status != domain.StatusVentilating {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}
	if t.Version != expectedVersion {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}
	for _, s := range samples {
		if err := tx.Arbitration().SaveVentilation(ctx, arbitration.VentilationEvidence{
			TaskNumber:     t.Number,
			PointCode:      s.PointCode,
			LogicalSlot:    s.LogicalSlot,
			Concentration:  s.Concentration,
			BelowThreshold: s.Concentration < t.Snapshot.ReentryThreshold,
		}); err != nil {
			return task.FumigationTask{}, err
		}
	}
	t.Version++
	t.LogicalClock++
	_ = appendEvent(repo, t, "submit-ventilation", fmt.Sprintf("%d samples", len(samples)))
	if err := repo.Save(ctx, t); err != nil {
		return task.FumigationTask{}, err
	}
	_ = recordReceipt(repo, taskNumber, opID, dig, versionString(t.Version))
	if err := tx.Commit(); err != nil {
		return task.FumigationTask{}, err
	}
	return t, nil
}

// allPointsSafe reports whether every locked sampling point has reached the
// continuous safe window below the reentry threshold.
func allPointsSafe(ctx context.Context, tx store.Tx, t task.FumigationTask) (bool, error) {
	samples, err := tx.Arbitration().ListVentilation(ctx, t.Number)
	if err != nil {
		return false, err
	}
	byPoint := map[string][]arbitration.VentilationEvidence{}
	for _, s := range samples {
		byPoint[s.PointCode] = append(byPoint[s.PointCode], s)
	}
	points := make([]string, 0, len(t.Snapshot.SamplingPoints))
	for _, p := range t.Snapshot.SamplingPoints {
		points = append(points, p.Code)
	}
	return arbitration.AllPointsContinuousBelowThreshold(byPoint, points, t.Snapshot.ReentryThreshold, t.Snapshot.SafeSlots), nil
}

// ReviewInput is one person's reentry review.
type ReviewInput struct {
	ReviewerID  string
	QualifiedAt domain.LogicalTime
	Approved    bool
}

// SubmitReview persists a reentry review and, once ventilation is complete and
// two distinct qualified reviewers have approved, advances the task to
// REENTRY_READY.
func (a *App) SubmitReview(ctx context.Context, opID string, expectedVersion int64, taskNumber string, in ReviewInput) (task.FumigationTask, error) {
	dig := digest(in)
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
	if t.Status != domain.StatusVentilating {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}
	if t.Version != expectedVersion {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}

	if err := tx.Arbitration().SaveReview(ctx, arbitration.ReentryReview{
		TaskNumber:  t.Number,
		ReviewerID:  in.ReviewerID,
		Qualified:   true,
		QualifiedAt: in.QualifiedAt,
		Approved:    in.Approved,
		ReviewedAt:  t.LogicalClock,
	}); err != nil {
		return task.FumigationTask{}, err
	}

	safe, err := allPointsSafe(ctx, tx, t)
	if err != nil {
		return task.FumigationTask{}, err
	}
	if safe {
		reviews, err := tx.Arbitration().ListReviews(ctx, t.Number)
		if err != nil {
			return task.FumigationTask{}, err
		}
		if arbitration.IsReentryEligible(reviews, t.LogicalClock) {
			t.Status = domain.StatusReentryReady
		}
	}
	t.Version++
	t.LogicalClock++
	_ = appendEvent(repo, t, "submit-review", in.ReviewerID)
	if err := repo.Save(ctx, t); err != nil {
		return task.FumigationTask{}, err
	}
	_ = recordReceipt(repo, taskNumber, opID, dig, versionString(t.Version))
	if err := tx.Commit(); err != nil {
		return task.FumigationTask{}, err
	}
	return t, nil
}

// Terminal performs the single compare-and-swap guarded terminal decision.
// Only the first successful terminal for a task wins; every later attempt
// returns TERMINAL_ALREADY_DECIDED.
func (a *App) Terminal(ctx context.Context, opID string, expectedVersion int64, taskNumber string, kind task.TerminalKind) (task.FumigationTask, error) {
	dig := digest(kind)
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
	if t.Status.IsTerminal() {
		return task.FumigationTask{}, domain.NewError(domain.ErrTerminalAlreadyDecided, opID, t.Version)
	}
	if t.Version != expectedVersion {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version)
	}
	if kind == task.TerminalCompleted && t.Status != domain.StatusReentryReady {
		return task.FumigationTask{}, domain.NewError(domain.ErrOperationContentConflict, opID, t.Version).
			AddReason(t.WarehouseCode, "", 0, "", domain.ErrOperationContentConflict, "completion requires reentry readiness")
	}

	decision := task.TerminalDecision{
		TaskNumber: t.Number,
		Kind:       kind,
		Version:    t.Version + 1,
		Reason:     domain.ErrNone,
		Evidence:   fmt.Sprintf("terminal %s", kind),
		DecidedAt:  t.LogicalClock + 1,
	}
	if err := tx.Arbitration().SaveTerminal(ctx, decision); err != nil {
		return task.FumigationTask{}, err
	}
	t.Status = task.StatusForTerminal(kind)
	t.Version++
	t.LogicalClock++
	t.Terminal = &decision
	_ = appendEvent(repo, t, "terminal", string(kind))
	if err := repo.Save(ctx, t); err != nil {
		return task.FumigationTask{}, err
	}
	_ = recordReceipt(repo, taskNumber, opID, dig, versionString(t.Version))
	if err := tx.Commit(); err != nil {
		return task.FumigationTask{}, err
	}
	return t, nil
}

// ScheduleDeviceCall records a durable device invocation intent for later
// execution by RunDueDeviceCalls.
func (a *App) ScheduleDeviceCall(ctx context.Context, taskNumber, deviceCode, kind string, maxAttempts int64) (store.DeviceCall, error) {
	tx, err := a.beginWrite(ctx)
	if err != nil {
		return store.DeviceCall{}, err
	}
	defer tx.Rollback()
	t, err := loadTask(tx.Tasks(), taskNumber)
	if err != nil {
		return store.DeviceCall{}, err
	}
	id := fmt.Sprintf("%s|%s", taskNumber, deviceCode)
	call := store.DeviceCall{
		ID:          id,
		DeviceCode:  deviceCode,
		Kind:        kind,
		TaskNumber:  taskNumber,
		MaxAttempts: maxAttempts,
		NextAt:      t.LogicalClock,
	}
	if err := tx.Leases().SaveDeviceCall(ctx, call); err != nil {
		return store.DeviceCall{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.DeviceCall{}, err
	}
	return call, nil
}

// RunDueDeviceCalls executes every due device call through the adapter and
// records the outcome. Successful calls complete; failures are rescheduled
// deterministically, and an exhausted call risk-isolates the task.
func (a *App) RunDueDeviceCalls(ctx context.Context, now domain.LogicalTime) ([]store.DeviceCall, error) {
	rtx, err := a.Store.Begin(ctx, false)
	if err != nil {
		return nil, err
	}
	due, err := rtx.Leases().ListDueDeviceCalls(ctx, now)
	rtx.Rollback()
	if err != nil {
		return nil, err
	}
	if len(due) == 0 {
		return nil, nil
	}

	outcomes := make([]lease.Outcome, len(due))
	for i, c := range due {
		outcomes[i], _ = a.Devices.Run(ctx, c.DeviceCode, c.Kind)
	}

	tx, err := a.beginWrite(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	for i, c := range due {
		outcome := outcomes[i]
		switch outcome {
		case lease.OutcomeSuccess:
			c.Completed = true
			c.Result = string(lease.OutcomeSuccess)
			c.Attempts++
			_ = tx.Leases().SaveDeviceCall(ctx, c)
		default:
			c.FailureKind = string(outcome)
			attempts, nextAt, exhausted := lease.NextAttempt(c.Attempts, c.MaxAttempts, 1, now)
			c.Attempts = attempts
			c.NextAt = domain.LogicalTime(nextAt)
			c.Result = string(outcome)
			_ = tx.Leases().SaveDeviceCall(ctx, c)
			if exhausted {
				_ = a.quarantineTask(ctx, tx, c.TaskNumber, domain.ErrDeviceRetryExhausted, "device retry exhausted")
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return due, nil
}

// quarantineTask force-terminates a task into risk isolation without touching
// any other evidence.
func (a *App) quarantineTask(ctx context.Context, tx store.Tx, taskNumber string, reason domain.ErrorCode, msg string) error {
	t, err := loadTask(tx.Tasks(), taskNumber)
	if err != nil {
		return err
	}
	if t.Status.IsTerminal() {
		return nil
	}
	decision := task.TerminalDecision{
		TaskNumber: t.Number,
		Kind:       task.TerminalRiskIsolated,
		Version:    t.Version + 1,
		Reason:     reason,
		Evidence:   msg,
		DecidedAt:  t.LogicalClock + 1,
	}
	_ = tx.Arbitration().SaveTerminal(ctx, decision)
	t.Status = domain.StatusRiskIsolated
	t.Version++
	t.LogicalClock++
	t.Terminal = &decision
	_ = appendEvent(tx.Tasks(), t, "quarantine", msg)
	return tx.Tasks().Save(ctx, t)
}
