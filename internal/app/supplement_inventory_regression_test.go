package app_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"granary-phosphine-fumigation-closure/internal/app"
	"granary-phosphine-fumigation-closure/internal/domain"
)

func TestModel_CreateSupplementPreservesBatchAccounting(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "deducts from persisted balances and records the batch code",
			run: func(t *testing.T) {
				a, _ := newTestApp(t)
				ctx := context.Background()
				tsk := createAndLock(t, a)

				applying, err := a.StartApplication(ctx, "initial-plan", tsk.Version, tsk.Number, []app.ApplicationPlan{
					{ZoneCode: "Z1", BatchCode: "B-1", MassMg: 500},
					{ZoneCode: "Z2", BatchCode: "B-1", MassMg: 500},
				})
				if err != nil {
					t.Fatalf("start initial application: %v", err)
				}
				appliedZ1, err := a.RecordApplication(ctx, "apply-z1", applying.Version, tsk.Number, "Z1", "B-1", 500)
				if err != nil {
					t.Fatalf("record Z1 application: %v", err)
				}
				appliedZ2, err := a.RecordApplication(ctx, "apply-z2", appliedZ1.Version, tsk.Number, "Z2", "B-1", 500)
				if err != nil {
					t.Fatalf("record Z2 application: %v", err)
				}
				exposed, err := a.SwitchCirculation(ctx, "initial-circulation", appliedZ2.Version, tsk.Number, "FAN-1")
				if err != nil {
					t.Fatalf("switch circulation: %v", err)
				}
				measured, err := a.SubmitMeasurements(ctx, "low-measurements", exposed.Version, tsk.Number, fullCoverage(1))
				if err != nil {
					t.Fatalf("submit complete low measurements: %v", err)
				}
				if _, err := a.CreateSupplement(ctx, "supplement-accounting", measured.Version, tsk.Number); err != nil {
					t.Fatalf("create supplement: %v", err)
				}

				batches, err := a.ListBatches(ctx)
				if err != nil {
					t.Fatalf("list batches: %v", err)
				}
				if len(batches) != 1 {
					t.Fatalf("batch count = %d, want 1", len(batches))
				}
				batch := batches[0]
				if batch.AvailableMg != 97240 || batch.ReservedMg != 1760 || batch.AppliedMg != 1000 {
					t.Fatalf("batch balances = available:%d reserved:%d applied:%d, want available:97240 reserved:1760 applied:1000", batch.AvailableMg, batch.ReservedMg, batch.AppliedMg)
				}
				if !batch.Balanced() {
					t.Fatalf("batch conservation was broken: %+v", batch)
				}

				ledger, err := a.GetLedger(ctx, tsk.Number)
				if err != nil {
					t.Fatalf("get ledger: %v", err)
				}
				supplementRows := 0
				for _, row := range ledger {
					if row.OperationID != "supplement-accounting" {
						continue
					}
					supplementRows++
					if row.BatchCode != "B-1" {
						t.Errorf("supplement ledger batch code = %q, want B-1", row.BatchCode)
					}
					if row.ReservedMg != 880 {
						t.Errorf("supplement ledger reserved mass = %d, want 880", row.ReservedMg)
					}
				}
				if supplementRows != 2 {
					t.Fatalf("supplement ledger rows = %d, want 2", supplementRows)
				}
			},
		},
		{
			name: "rejects incomplete coverage",
			run: func(t *testing.T) {
				a, _ := newTestApp(t)
				ctx := context.Background()
				tsk := startExposure(t, a, createAndLock(t, a))
				measured, err := a.SubmitMeasurements(ctx, "partial-measurements", tsk.Version, tsk.Number, []app.MeasurementInput{
					{PointCode: "SP-1", LogicalSlot: 0, Concentration: 1, Sequence: 0},
					{PointCode: "SP-1", LogicalSlot: 1, Concentration: 1, Sequence: 1},
					{PointCode: "SP-1", LogicalSlot: 2, Concentration: 1, Sequence: 2},
				})
				if err != nil {
					t.Fatalf("submit partial measurements: %v", err)
				}
				if _, err := a.CreateSupplement(ctx, "supplement-partial", measured.Version, tsk.Number); err == nil {
					t.Fatal("create supplement succeeded with incomplete coverage")
				} else if de, ok := err.(*domain.Error); !ok || de.Code != domain.ErrMeasurementMissing {
					t.Fatalf("create supplement error = %v, want MEASUREMENT_MISSING", err)
				}
				got, err := a.GetTask(ctx, tsk.Number)
				if err != nil {
					t.Fatalf("get task: %v", err)
				}
				if got.SupplementGeneration != 0 {
					t.Fatalf("supplement generation = %d, want 0", got.SupplementGeneration)
				}
			},
		},
		{
			name: "does not advance generation without underdosed zones",
			run: func(t *testing.T) {
				a, _ := newTestApp(t)
				ctx := context.Background()
				tsk := startExposure(t, a, createAndLock(t, a))
				measured, err := a.SubmitMeasurements(ctx, "sufficient-measurements", tsk.Version, tsk.Number, fullCoverage(10))
				if err != nil {
					t.Fatalf("submit sufficient measurements: %v", err)
				}
				got, err := a.CreateSupplement(ctx, "supplement-not-needed", measured.Version, tsk.Number)
				if err != nil {
					t.Fatalf("create supplement: %v", err)
				}
				if got.SupplementGeneration != 0 || got.Status != domain.StatusExposureMaintain {
					t.Fatalf("task advanced without an underdosed zone: generation=%d status=%s", got.SupplementGeneration, got.Status)
				}
			},
		},
		{
			name: "allows only one concurrent supplement",
			run: func(t *testing.T) {
				a, _ := newTestApp(t)
				ctx := context.Background()
				tsk := startExposure(t, a, createAndLock(t, a))
				measured, err := a.SubmitMeasurements(ctx, "concurrent-measurements", tsk.Version, tsk.Number, fullCoverage(1))
				if err != nil {
					t.Fatalf("submit low measurements: %v", err)
				}

				const callers = 4
				start := make(chan struct{})
				errs := make([]error, callers)
				var wg sync.WaitGroup
				for i := 0; i < callers; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						<-start
						_, errs[i] = a.CreateSupplement(ctx, fmt.Sprintf("concurrent-supplement-%d", i), measured.Version, tsk.Number)
					}(i)
				}
				close(start)
				wg.Wait()

				successes := 0
				for _, err := range errs {
					if err == nil {
						successes++
					}
				}
				if successes != 1 {
					t.Fatalf("successful concurrent supplements = %d, want 1", successes)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}
