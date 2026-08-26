package app_test

import (
	"context"
	"testing"

	"granary-phosphine-fumigation-closure/internal/app"
	"granary-phosphine-fumigation-closure/internal/catalog"
	"granary-phosphine-fumigation-closure/internal/domain"
)

func TestModel_StartApplicationPersistsCumulativeBatchReservations(t *testing.T) {
	tests := []struct {
		name       string
		extraBatch *catalog.PesticideBatch
		plans      []app.ApplicationPlan
		want       map[string]catalog.PesticideBatch
	}{
		{
			name: "two zones reserve the same batch cumulatively",
			plans: []app.ApplicationPlan{
				{ZoneCode: "Z1", BatchCode: "B-1", MassMg: 500},
				{ZoneCode: "Z2", BatchCode: "B-1", MassMg: 500},
			},
			want: map[string]catalog.PesticideBatch{
				"B-1": {Code: "B-1", InitialMg: 100000, AvailableMg: 99000, ReservedMg: 1000},
			},
		},
		{
			name:       "different zones reserve different batches independently",
			extraBatch: &catalog.PesticideBatch{Code: "B-2", InitialMg: 2000, AvailableMg: 2000},
			plans: []app.ApplicationPlan{
				{ZoneCode: "Z1", BatchCode: "B-1", MassMg: 400},
				{ZoneCode: "Z2", BatchCode: "B-2", MassMg: 600},
			},
			want: map[string]catalog.PesticideBatch{
				"B-1": {Code: "B-1", InitialMg: 100000, AvailableMg: 99600, ReservedMg: 400},
				"B-2": {Code: "B-2", InitialMg: 2000, AvailableMg: 1400, ReservedMg: 600},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := newTestApp(t)
			ctx := context.Background()
			if tt.extraBatch != nil {
				if err := a.RegisterBatch(ctx, *tt.extraBatch); err != nil {
					t.Fatalf("register extra batch: %v", err)
				}
			}
			locked := createAndLock(t, a)

			started, err := a.StartApplication(ctx, "reserve-zones", locked.Version, locked.Number, tt.plans)
			if err != nil {
				t.Fatalf("start application: %v", err)
			}
			if started.Status != domain.StatusApplying {
				t.Errorf("status = %s, want %s", started.Status, domain.StatusApplying)
			}

			ledger, err := a.GetLedger(ctx, locked.Number)
			if err != nil {
				t.Fatalf("get ledger: %v", err)
			}
			ledgerReserved := make(map[string]int64)
			for _, entry := range ledger {
				ledgerReserved[entry.BatchCode] += entry.ReservedMg
			}

			batches, err := a.ListBatches(ctx)
			if err != nil {
				t.Fatalf("list batches: %v", err)
			}
			got := make(map[string]catalog.PesticideBatch, len(batches))
			for _, batch := range batches {
				got[batch.Code] = batch
			}
			for code, want := range tt.want {
				batch := got[code]
				if batch.AvailableMg != want.AvailableMg || batch.ReservedMg != want.ReservedMg {
					t.Errorf("batch %s balance = available %d, reserved %d; want available %d, reserved %d",
						code, batch.AvailableMg, batch.ReservedMg, want.AvailableMg, want.ReservedMg)
				}
				if ledgerReserved[code] != want.ReservedMg {
					t.Errorf("batch %s ledger reserved total = %d, want %d", code, ledgerReserved[code], want.ReservedMg)
				}
				if !batch.Balanced() {
					t.Errorf("batch %s is not mass-balanced: %+v", code, batch)
				}
			}

			conserved, err := a.VerifyConservation(ctx, locked.Number)
			if err != nil {
				t.Fatalf("verify conservation: %v", err)
			}
			if !conserved {
				t.Error("persisted reservation state must pass the later conservation recomputation")
			}
		})
	}
}
