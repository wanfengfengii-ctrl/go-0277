package app_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"granary-phosphine-fumigation-closure/internal/app"
	"granary-phosphine-fumigation-closure/internal/bboltstore"
	"granary-phosphine-fumigation-closure/internal/catalog"
	"granary-phosphine-fumigation-closure/internal/domain"
	"granary-phosphine-fumigation-closure/internal/lease"
	"granary-phosphine-fumigation-closure/internal/task"
)

func standardWarehouse() catalog.Warehouse {
	return catalog.Warehouse{
		Code:             "WH-01",
		RatedCapacityDm3: 2000,
		AllowedGrains:    []catalog.GrainType{"WHEAT"},
		StructureVersion: 1,
		Zones: []catalog.Zone{
			{Code: "Z1", Warehouse: "WH-01", CapacityDm3: 1000},
			{Code: "Z2", Warehouse: "WH-01", CapacityDm3: 1000},
		},
		Edges: []catalog.NeighborEdge{{From: "Z1", To: "Z2"}},
		Devices: []catalog.Device{
			{Code: "CAGE-1", Warehouse: "WH-01", Kind: catalog.DeviceDosingCage},
			{Code: "FAN-1", Warehouse: "WH-01", Kind: catalog.DeviceFanCircuit, MutuallyExclusiveWith: []string{"FAN-2"}},
			{Code: "FAN-2", Warehouse: "WH-01", Kind: catalog.DeviceFanCircuit, MutuallyExclusiveWith: []string{"FAN-1"}},
			{Code: "SL-1", Warehouse: "WH-01", Kind: catalog.DeviceSamplingLine},
		},
		SamplingPoints: []catalog.SamplingPoint{
			{Code: "SP-1", Warehouse: "WH-01", Zone: "Z1"},
			{Code: "SP-2", Warehouse: "WH-01", Zone: "Z2"},
		},
	}
}

func standardRule() catalog.FumigationRule {
	return catalog.FumigationRule{
		Version:             1,
		GrainTypes:          []catalog.GrainType{"WHEAT"},
		MinHeightDm:         1,
		MaxHeightDm:         10,
		CapacityFactor:      1000,
		TargetDoseCT:        1000,
		SamplingWindowSlots: 3,
		SlotDurationSec:     60,
		LeakThreshold:       50,
		ReentryThreshold:    5,
		SafeContinuousSlots: 2,
		RetryMaxAttempts:    3,
		RetryBaseDelaySlots: 1,
	}
}

func standardBatch() catalog.PesticideBatch {
	return catalog.PesticideBatch{Code: "B-1", InitialMg: 100000, AvailableMg: 100000}
}

// newTestApp opens a fresh bbolt database and registers the standard fixture.
func newTestApp(t *testing.T) (*app.App, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "granary.db")
	db, err := bboltstore.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Recover(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	a := app.New(db, nil)
	ctx := context.Background()
	if err := a.RegisterWarehouse(ctx, standardWarehouse()); err != nil {
		t.Fatalf("register warehouse: %v", err)
	}
	if err := a.RegisterRule(ctx, standardRule()); err != nil {
		t.Fatalf("register rule: %v", err)
	}
	if err := a.RegisterBatch(ctx, standardBatch()); err != nil {
		t.Fatalf("register batch: %v", err)
	}
	return a, path
}

// createAndLock creates a task and locks it, returning the locked task.
func createAndLock(t *testing.T, a *app.App) task.FumigationTask {
	t.Helper()
	ctx := context.Background()
	created, err := a.CreateTask(ctx, "WH-01")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	sum, err := a.SummarizePreview(ctx, "WH-01")
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	locked, err := a.LockTask(ctx, "lock-1", created.Version, created.Number, app.LockRequest{
		GrainType:     "WHEAT",
		StackHeightDm: 5,
		Summary:       sum,
	})
	if err != nil {
		t.Fatalf("lock task: %v", err)
	}
	return locked
}

// startExposure drives a task into EXPOSURE_MAINTAINING.
func startExposure(t *testing.T, a *app.App, tsk task.FumigationTask) task.FumigationTask {
	t.Helper()
	ctx := context.Background()
	applied, err := a.StartApplication(ctx, "app-1", tsk.Version, tsk.Number, []app.ApplicationPlan{
		{ZoneCode: "Z1", BatchCode: "B-1", MassMg: 500},
		{ZoneCode: "Z2", BatchCode: "B-1", MassMg: 500},
	})
	if err != nil {
		t.Fatalf("start application: %v", err)
	}
	exposed, err := a.SwitchCirculation(ctx, "circ-1", applied.Version, tsk.Number, "FAN-1")
	if err != nil {
		t.Fatalf("switch circulation: %v", err)
	}
	return exposed
}

func fullCoverage(concentration int64) []app.MeasurementInput {
	return []app.MeasurementInput{
		{PointCode: "SP-1", LogicalSlot: 0, Concentration: concentration, Sequence: 0},
		{PointCode: "SP-1", LogicalSlot: 1, Concentration: concentration, Sequence: 1},
		{PointCode: "SP-1", LogicalSlot: 2, Concentration: concentration, Sequence: 2},
		{PointCode: "SP-2", LogicalSlot: 0, Concentration: concentration, Sequence: 0},
		{PointCode: "SP-2", LogicalSlot: 1, Concentration: concentration, Sequence: 1},
		{PointCode: "SP-2", LogicalSlot: 2, Concentration: concentration, Sequence: 2},
	}
}

func TestLockSnapshotAndRejections(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()

	created, err := a.CreateTask(ctx, "WH-01")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sum, _ := a.SummarizePreview(ctx, "WH-01")

	// Grain type mismatch leaves the task PENDING_LOCK.
	if _, err := a.LockTask(ctx, "bad-grain", created.Version, created.Number, app.LockRequest{
		GrainType: "RICE", StackHeightDm: 5, Summary: sum,
	}); err == nil {
		t.Fatalf("grain mismatch must be rejected")
	} else if de, ok := err.(*domain.Error); !ok || de.Code != domain.ErrGrainTypeMismatch {
		t.Fatalf("want GRAIN_TYPE_MISMATCH, got %v", err)
	}

	// Stale summary is rejected.
	if _, err := a.LockTask(ctx, "stale", created.Version, created.Number, app.LockRequest{
		GrainType: "WHEAT", StackHeightDm: 5, Summary: catalog.Summary{Digest: "deadbeef"},
	}); err == nil {
		t.Fatalf("stale summary must be rejected")
	}

	// A valid lock freezes an immutable snapshot.
	locked, err := a.LockTask(ctx, "lock-ok", created.Version, created.Number, app.LockRequest{
		GrainType: "WHEAT", StackHeightDm: 5, Summary: sum,
	})
	if err != nil {
		t.Fatalf("valid lock failed: %v", err)
	}
	if locked.Status != domain.StatusAirtightChecking {
		t.Fatalf("status = %s, want AIRTIGHT_CHECKING", locked.Status)
	}
	if locked.Snapshot == nil || len(locked.Snapshot.Zones) != 2 || len(locked.Snapshot.Devices) != 4 {
		t.Fatalf("snapshot not fully frozen: %+v", locked.Snapshot)
	}
}

func TestResourceLeaseConflict(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()

	t1 := createAndLock(t, a)
	t2 := createAndLock(t, a)

	var wg sync.WaitGroup
	results := make([]error, 2)
	start := make(chan struct{})
	for i, tsk := range []task.FumigationTask{t1, t2} {
		wg.Add(1)
		go func(i int, tsk task.FumigationTask) {
			defer wg.Done()
			<-start
			_, results[i] = a.StartApplication(ctx, fmt.Sprintf("op-%d", i), tsk.Version, tsk.Number, []app.ApplicationPlan{
				{ZoneCode: "Z1", BatchCode: "B-1", MassMg: 500},
			})
		}(i, tsk)
	}
	close(start)
	wg.Wait()

	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		} else if de, ok := err.(*domain.Error); !ok || de.Code != domain.ErrResourceLeaseConflict {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("exactly one task should acquire the resources, got %d", successes)
	}
}

func TestApplicationIdempotency(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	tsk := createAndLock(t, a)

	plans := []app.ApplicationPlan{{ZoneCode: "Z1", BatchCode: "B-1", MassMg: 500}}

	first, err := a.StartApplication(ctx, "op-1", tsk.Version, tsk.Number, plans)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// Same operation id and content must be idempotent.
	second, err := a.StartApplication(ctx, "op-1", tsk.Version, tsk.Number, plans)
	if err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}
	if second.Version != first.Version {
		t.Fatalf("idempotent retry must not advance version: %d vs %d", second.Version, first.Version)
	}

	batch, _ := a.ListBatches(ctx)
	if batch[0].ReservedMg != 500 {
		t.Fatalf("reserved mass = %d, want 500 (deducted once)", batch[0].ReservedMg)
	}
}

func TestMeasurementCoverageAndIntegral(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	tsk := startExposure(t, a, createAndLock(t, a))

	measured, err := a.SubmitMeasurements(ctx, "m-1", tsk.Version, tsk.Number, fullCoverage(10))
	if err != nil {
		t.Fatalf("submit measurements: %v", err)
	}
	view, err := a.GetCoverage(ctx, tsk.Number)
	if err != nil {
		t.Fatalf("coverage: %v", err)
	}
	if len(view.Cells) != 6 {
		t.Fatalf("cells = %d, want 6", len(view.Cells))
	}

	// Duplicate conflicting value must not overwrite the cell.
	if _, err := a.SubmitMeasurements(ctx, "m-2", measured.Version, tsk.Number, []app.MeasurementInput{
		{PointCode: "SP-1", LogicalSlot: 0, Concentration: 99, Sequence: 99},
	}); err == nil {
		t.Fatalf("conflicting duplicate must be rejected")
	} else if de, ok := err.(*domain.Error); !ok || de.Code != domain.ErrMeasurementConflict {
		t.Fatalf("want MEASUREMENT_CONFLICT, got %v", err)
	}
}

func TestSupplementSingleGeneration(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	tsk := startExposure(t, a, createAndLock(t, a))

	measured, err := a.SubmitMeasurements(ctx, "m-1", tsk.Version, tsk.Number, fullCoverage(1))
	if err != nil {
		t.Fatalf("submit low coverage: %v", err)
	}

	var wg sync.WaitGroup
	results := make([]error, 4)
	start := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, results[i] = a.CreateSupplement(ctx, fmt.Sprintf("sup-%d", i), measured.Version, tsk.Number)
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("exactly one supplement should succeed, got %d", successes)
	}
	got, _ := a.GetTask(ctx, tsk.Number)
	if got.SupplementGeneration != 1 {
		t.Fatalf("supplement generation = %d, want 1", got.SupplementGeneration)
	}
}

func TestLeakPropagationBlocksVentilation(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	tsk := startExposure(t, a, createAndLock(t, a))

	measured, err := a.SubmitMeasurements(ctx, "m-1", tsk.Version, tsk.Number, fullCoverage(10))
	if err != nil {
		t.Fatalf("measurements: %v", err)
	}

	if _, err := a.ReportLeak(ctx, "leak-1", measured.Version, tsk.Number, app.LeakInput{SourceCode: "Z1", MeasuredValue: 100}); err != nil {
		t.Fatalf("report leak: %v", err)
	}
	closure, err := a.GetLeakClosure(ctx, tsk.Number)
	if err != nil {
		t.Fatalf("closure: %v", err)
	}
	if len(closure.ClosureCodes) != 2 {
		t.Fatalf("closure = %v, want both zones", closure.ClosureCodes)
	}

	// Leak is active: ventilation is refused.
	got, _ := a.GetTask(ctx, tsk.Number)
	if _, err := a.StartVentilation(ctx, "vent", got.Version, tsk.Number); err == nil {
		t.Fatalf("ventilation must be blocked by active leak")
	}

	// Resolve the leak, then ventilation succeeds.
	got, _ = a.GetTask(ctx, tsk.Number)
	if _, err := a.ResolveLeak(ctx, "resolve", got.Version, tsk.Number); err != nil {
		t.Fatalf("resolve leak: %v", err)
	}
	got, _ = a.GetTask(ctx, tsk.Number)
	if _, err := a.StartVentilation(ctx, "vent-2", got.Version, tsk.Number); err != nil {
		t.Fatalf("ventilation should succeed after resolution: %v", err)
	}
}

func TestVentilationReentryFlow(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	tsk := startExposure(t, a, createAndLock(t, a))

	if _, err := a.SubmitMeasurements(ctx, "m-1", tsk.Version, tsk.Number, fullCoverage(10)); err != nil {
		t.Fatalf("measurements: %v", err)
	}
	v, _ := a.GetTask(ctx, tsk.Number)
	vent, err := a.StartVentilation(ctx, "vent", v.Version, tsk.Number)
	if err != nil {
		t.Fatalf("start ventilation: %v", err)
	}
	if _, err := a.SubmitVentilation(ctx, "vv", vent.Version, tsk.Number, []app.VentilationInput{
		{PointCode: "SP-1", LogicalSlot: 0, Concentration: 1},
		{PointCode: "SP-1", LogicalSlot: 1, Concentration: 1},
		{PointCode: "SP-2", LogicalSlot: 0, Concentration: 1},
		{PointCode: "SP-2", LogicalSlot: 1, Concentration: 1},
	}); err != nil {
		t.Fatalf("ventilation samples: %v", err)
	}

	// Duplicate reviewer must not advance to reentry-ready.
	v, _ = a.GetTask(ctx, tsk.Number)
	r1, err := a.SubmitReview(ctx, "rev-1", v.Version, tsk.Number, app.ReviewInput{ReviewerID: "R1", QualifiedAt: 0, Approved: true})
	if err != nil {
		t.Fatalf("review 1: %v", err)
	}
	if r1.Status == domain.StatusReentryReady {
		t.Fatalf("one review must not reach reentry-ready")
	}
	v, _ = a.GetTask(ctx, tsk.Number)
	if _, err := a.SubmitReview(ctx, "rev-1b", v.Version, tsk.Number, app.ReviewInput{ReviewerID: "R1", QualifiedAt: 0, Approved: true}); err != nil {
		t.Fatalf("duplicate reviewer: %v", err)
	}
	v, _ = a.GetTask(ctx, tsk.Number)
	if v.Status == domain.StatusReentryReady {
		t.Fatalf("duplicate reviewer must not reach reentry-ready")
	}
	r2, err := a.SubmitReview(ctx, "rev-2", v.Version, tsk.Number, app.ReviewInput{ReviewerID: "R2", QualifiedAt: 0, Approved: true})
	if err != nil {
		t.Fatalf("review 2: %v", err)
	}
	if r2.Status != domain.StatusReentryReady {
		t.Fatalf("status = %s, want REENTRY_READY", r2.Status)
	}

	// Complete the loop: only a reentry-ready task may complete.
	if _, err := a.Terminal(ctx, "done", r2.Version, tsk.Number, task.TerminalCompleted); err != nil {
		t.Fatalf("terminal completed: %v", err)
	}
	final, _ := a.GetTask(ctx, tsk.Number)
	if final.Status != domain.StatusCompleted {
		t.Fatalf("final status = %s, want COMPLETED", final.Status)
	}
}

func TestDeviceRetryExhaustionQuarantines(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	tsk := createAndLock(t, a)

	a.Devices = lease.NewScriptedAdapter(map[string][]lease.Outcome{
		"FAN-1": {lease.OutcomeTimeout, lease.OutcomeTimeout, lease.OutcomeTimeout, lease.OutcomeTimeout},
	})
	if _, err := a.ScheduleDeviceCall(ctx, tsk.Number, "FAN-1", "FAN_CIRCUIT", 3); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	// Exhaust the retry budget (max 3 attempts).
	for _, now := range []domain.LogicalTime{1, 3, 6, 10} {
		if _, err := a.RunDueDeviceCalls(ctx, now); err != nil {
			t.Fatalf("run at %d: %v", now, err)
		}
	}
	got, _ := a.GetTask(ctx, tsk.Number)
	if got.Status != domain.StatusRiskIsolated {
		t.Fatalf("status = %s, want RISK_ISOLATED after retry exhaustion", got.Status)
	}
	if got.Terminal == nil || got.Terminal.Kind != task.TerminalRiskIsolated {
		t.Fatalf("terminal = %+v, want risk isolation", got.Terminal)
	}
}

func TestTerminalSingleDecision(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	tsk := createAndLock(t, a)

	var wg sync.WaitGroup
	results := make([]error, 3)
	kinds := []task.TerminalKind{task.TerminalCancelled, task.TerminalRiskIsolated, task.TerminalCancelled}
	start := make(chan struct{})
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, results[i] = a.Terminal(ctx, fmt.Sprintf("term-%d", i), tsk.Version, tsk.Number, kinds[i])
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	alreadyDecided := 0
	for _, err := range results {
		if err == nil {
			successes++
		} else if de, ok := err.(*domain.Error); ok && de.Code == domain.ErrTerminalAlreadyDecided {
			alreadyDecided++
		}
	}
	if successes != 1 {
		t.Fatalf("exactly one terminal should win, got %d", successes)
	}
	if alreadyDecided != 2 {
		t.Fatalf("losers should return TERMINAL_ALREADY_DECIDED, got %d", alreadyDecided)
	}
}

func TestDeviceRetryAndRestartRecovery(t *testing.T) {
	a, path := newTestApp(t)
	ctx := context.Background()

	// Create a task and schedule a device call against a scripted adapter.
	tsk := createAndLock(t, a)
	a.Devices = lease.NewScriptedAdapter(map[string][]lease.Outcome{
		"FAN-1": {lease.OutcomeTimeout, lease.OutcomeDisconnected, lease.OutcomeRefused, lease.OutcomeSuccess},
	})
	if _, err := a.ScheduleDeviceCall(ctx, tsk.Number, "FAN-1", "FAN_CIRCUIT", 3); err != nil {
		t.Fatalf("schedule device call: %v", err)
	}

	// Advance through the retry sequence.
	for _, now := range []domain.LogicalTime{1, 2, 4, 7} {
		if _, err := a.RunDueDeviceCalls(ctx, now); err != nil {
			t.Fatalf("run device calls at %d: %v", now, err)
		}
	}
	calls, _ := a.ListDeviceCalls(ctx, tsk.Number)
	if len(calls) != 1 || !calls[0].Completed || calls[0].Attempts != 4 {
		t.Fatalf("device call not completed after retries: %+v", calls)
	}

	// Reopen the database and verify recovery resumes the same state.
	if err := a.Store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	db2, err := bboltstore.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
	if _, err := db2.Recover(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}
	a2 := app.New(db2, nil)
	got, err := a2.GetTask(ctx, tsk.Number)
	if err != nil {
		t.Fatalf("task lost after restart: %v", err)
	}
	if got.Status != domain.StatusAirtightChecking {
		t.Fatalf("status after restart = %s", got.Status)
	}
	calls2, _ := a2.ListDeviceCalls(ctx, tsk.Number)
	if len(calls2) != 1 || !calls2[0].Completed {
		t.Fatalf("device call not recovered: %+v", calls2)
	}
}
