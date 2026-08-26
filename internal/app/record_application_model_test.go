package app_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"granary-phosphine-fumigation-closure/internal/app"
	"granary-phosphine-fumigation-closure/internal/domain"
)

func TestModel_RecordApplicationAdvancesEachPlanOnce(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T, *app.App)
	}{
		{
			name: "same operation retry returns the existing result without another movement",
			run: func(t *testing.T, a *app.App) {
				ctx := context.Background()
				locked := createAndLock(t, a)
				started, err := a.StartApplication(ctx, "start-z1", locked.Version, locked.Number, []app.ApplicationPlan{
					{ZoneCode: "Z1", BatchCode: "B-1", MassMg: 500},
				})
				if err != nil {
					t.Fatalf("start application: %v", err)
				}

				first, err := a.RecordApplication(ctx, "confirm-z1", started.Version, started.Number, "Z1", "B-1", 500)
				if err != nil {
					t.Fatalf("record application: %v", err)
				}
				batchesBefore, err := a.ListBatches(ctx)
				if err != nil {
					t.Fatalf("list batches before retry: %v", err)
				}
				ledgerBefore, err := a.GetLedger(ctx, started.Number)
				if err != nil {
					t.Fatalf("get ledger before retry: %v", err)
				}

				retried, err := a.RecordApplication(ctx, "confirm-z1", started.Version, started.Number, "Z1", "B-1", 500)
				if err != nil {
					t.Fatalf("idempotent record retry: %v", err)
				}
				batchesAfter, err := a.ListBatches(ctx)
				if err != nil {
					t.Fatalf("list batches after retry: %v", err)
				}
				ledgerAfter, err := a.GetLedger(ctx, started.Number)
				if err != nil {
					t.Fatalf("get ledger after retry: %v", err)
				}

				if retried.Version != first.Version {
					t.Fatalf("retry version = %d, want existing result version %d", retried.Version, first.Version)
				}
				if !reflect.DeepEqual(batchesAfter, batchesBefore) || !reflect.DeepEqual(ledgerAfter, ledgerBefore) {
					t.Fatalf("idempotent retry changed dose state: batches before=%+v after=%+v ledger before=%+v after=%+v", batchesBefore, batchesAfter, ledgerBefore, ledgerAfter)
				}
			},
		},
		{
			name: "different operation cannot confirm an already completed plan",
			run: func(t *testing.T, a *app.App) {
				ctx := context.Background()
				locked := createAndLock(t, a)
				started, err := a.StartApplication(ctx, "start-z1", locked.Version, locked.Number, []app.ApplicationPlan{
					{ZoneCode: "Z1", BatchCode: "B-1", MassMg: 500},
				})
				if err != nil {
					t.Fatalf("start application: %v", err)
				}
				confirmed, err := a.RecordApplication(ctx, "confirm-z1-first", started.Version, started.Number, "Z1", "B-1", 500)
				if err != nil {
					t.Fatalf("first confirmation: %v", err)
				}

				var firstRejection *domain.Error
				for attempt := 1; attempt <= 2; attempt++ {
					_, err = a.RecordApplication(ctx, "confirm-z1-again", confirmed.Version, started.Number, "Z1", "B-1", 500)
					var businessErr *domain.Error
					if !errors.As(err, &businessErr) {
						t.Fatalf("duplicate attempt %d error = %v, want stable business error", attempt, err)
					}
					if businessErr.Code == domain.ErrNone || businessErr.OperationID != "confirm-z1-again" || businessErr.AggregateVersion != confirmed.Version {
						t.Fatalf("duplicate attempt %d error = %+v, want a business rejection for version %d", attempt, businessErr, confirmed.Version)
					}
					if firstRejection == nil {
						firstRejection = businessErr
					} else if !reflect.DeepEqual(businessErr, firstRejection) {
						t.Fatalf("duplicate rejection changed between retries: first=%+v second=%+v", firstRejection, businessErr)
					}
				}

				batches, err := a.ListBatches(ctx)
				if err != nil {
					t.Fatalf("list batches: %v", err)
				}
				if len(batches) != 1 || batches[0].ReservedMg != 0 || batches[0].AppliedMg != 500 || !batches[0].Balanced() {
					t.Fatalf("batch after duplicate confirmations = %+v, want reserved=0 applied=500 and conserved", batches)
				}
				ledger, err := a.GetLedger(ctx, started.Number)
				if err != nil {
					t.Fatalf("get ledger: %v", err)
				}
				appliedMovements := 0
				for _, entry := range ledger {
					if entry.AppliedMg != 0 {
						appliedMovements++
					}
				}
				if appliedMovements != 1 {
					t.Fatalf("applied ledger movements = %d, want exactly 1; ledger=%+v", appliedMovements, ledger)
				}
			},
		},
		{
			name: "different registered zone plans can each be confirmed",
			run: func(t *testing.T, a *app.App) {
				ctx := context.Background()
				locked := createAndLock(t, a)
				started, err := a.StartApplication(ctx, "start-two-zones", locked.Version, locked.Number, []app.ApplicationPlan{
					{ZoneCode: "Z1", BatchCode: "B-1", MassMg: 400},
					{ZoneCode: "Z2", BatchCode: "B-1", MassMg: 600},
				})
				if err != nil {
					t.Fatalf("start application: %v", err)
				}
				z1, err := a.RecordApplication(ctx, "confirm-z1", started.Version, started.Number, "Z1", "B-1", 400)
				if err != nil {
					t.Fatalf("confirm Z1: %v", err)
				}
				if _, err := a.RecordApplication(ctx, "confirm-z2", z1.Version, started.Number, "Z2", "B-1", 600); err != nil {
					t.Fatalf("confirm Z2: %v", err)
				}

				batches, err := a.ListBatches(ctx)
				if err != nil {
					t.Fatalf("list batches: %v", err)
				}
				if len(batches) != 1 || batches[0].ReservedMg != 0 || batches[0].AppliedMg != 1000 || !batches[0].Balanced() {
					t.Fatalf("batch after two valid zone confirmations = %+v, want reserved=0 applied=1000 and conserved", batches)
				}
				ledger, err := a.GetLedger(ctx, started.Number)
				if err != nil {
					t.Fatalf("get ledger: %v", err)
				}
				appliedByZone := map[string]int64{}
				for _, entry := range ledger {
					appliedByZone[entry.ZoneCode] += entry.AppliedMg
				}
				if !reflect.DeepEqual(appliedByZone, map[string]int64{"Z1": 400, "Z2": 600}) {
					t.Fatalf("applied ledger by zone = %v, want Z1=400 Z2=600", appliedByZone)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestApp(t)
			tc.run(t, a)
		})
	}
}
