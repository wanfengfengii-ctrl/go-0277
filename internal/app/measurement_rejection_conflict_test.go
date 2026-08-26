package app_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"granary-phosphine-fumigation-closure/internal/app"
	"granary-phosphine-fumigation-closure/internal/bboltstore"
	"granary-phosphine-fumigation-closure/internal/domain"
)

func TestModel_RejectedMeasurementKeyRemainsImmutable(t *testing.T) {
	tests := []struct {
		name    string
		restart bool
	}{
		{name: "engine reconstruction", restart: false},
		{name: "process recovery", restart: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "granary.db")
			db, err := bboltstore.Open(path)
			if err != nil {
				t.Fatalf("open database: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if _, err := db.Recover(ctx); err != nil {
				t.Fatalf("recover database: %v", err)
			}

			service := app.New(db, nil)
			if err := service.RegisterWarehouse(ctx, standardWarehouse()); err != nil {
				t.Fatalf("register warehouse: %v", err)
			}
			if err := service.RegisterRule(ctx, standardRule()); err != nil {
				t.Fatalf("register rule: %v", err)
			}
			if err := service.RegisterBatch(ctx, standardBatch()); err != nil {
				t.Fatalf("register batch: %v", err)
			}
			task := startExposure(t, service, createAndLock(t, service))

			key := app.MeasurementInput{
				PointCode:     "SP-1",
				LogicalSlot:   1,
				Sequence:      7,
				Concentration: -1,
			}
			recorded, err := service.SubmitMeasurements(ctx, "negative-reading", task.Version, task.Number, []app.MeasurementInput{key})
			if err != nil {
				t.Fatalf("record rejected measurement evidence: %v", err)
			}

			before, err := service.GetCoverage(ctx, task.Number)
			if err != nil {
				t.Fatalf("get coverage before retry: %v", err)
			}
			if len(before.Measurements) != 1 {
				t.Fatalf("raw measurements before retry = %d, want 1", len(before.Measurements))
			}
			original := before.Measurements[0]
			if original.Accepted || original.RejectCode != domain.ErrMeasurementOutOfWindow || original.Concentration != -1 {
				t.Fatalf("original rejection evidence = %+v", original)
			}
			if len(before.Cells) != 0 {
				t.Fatalf("coverage cells after rejected reading = %d, want 0", len(before.Cells))
			}

			if tt.restart {
				if err := db.Close(); err != nil {
					t.Fatalf("close database: %v", err)
				}
				db, err = bboltstore.Open(path)
				if err != nil {
					t.Fatalf("reopen database: %v", err)
				}
				if _, err := db.Recover(ctx); err != nil {
					t.Fatalf("recover reopened database: %v", err)
				}
				service = app.New(db, nil)
			}

			key.Concentration = 10
			_, err = service.SubmitMeasurements(ctx, "corrected-reading", recorded.Version, task.Number, []app.MeasurementInput{key})
			if err == nil {
				t.Fatal("different value for a rejected immutable measurement key was accepted")
			}
			de, ok := err.(*domain.Error)
			if !ok || de.Code != domain.ErrMeasurementConflict {
				t.Fatalf("retry error = %v, want MEASUREMENT_CONFLICT", err)
			}

			after, err := service.GetCoverage(ctx, task.Number)
			if err != nil {
				t.Fatalf("get coverage after retry: %v", err)
			}
			if len(after.Measurements) != 1 || !reflect.DeepEqual(after.Measurements[0], original) {
				t.Fatalf("rejection evidence changed: before=%+v after=%+v", original, after.Measurements)
			}
			if len(after.Cells) != 0 {
				t.Fatalf("conflicting retry changed coverage cells: %+v", after.Cells)
			}
		})
	}
}
