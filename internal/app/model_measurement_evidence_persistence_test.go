package app_test

import (
	"context"
	"testing"

	"granary-phosphine-fumigation-closure/internal/app"
	"granary-phosphine-fumigation-closure/internal/domain"
)

func TestModel_MeasurementEvidencePersistence(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "same rejected measurement replay preserves first evidence",
			run: func(t *testing.T) {
				a, _ := newTestApp(t)
				ctx := context.Background()
				tsk := startExposure(t, a, createAndLock(t, a))
				input := []app.MeasurementInput{{
					PointCode: "SP-1", LogicalSlot: 3, Concentration: 10, Sequence: 7,
				}}

				firstTask, err := a.SubmitMeasurements(ctx, "outside-window-first", tsk.Version, tsk.Number, input)
				if err != nil {
					t.Fatalf("submit first rejected measurement: %v", err)
				}
				firstView, err := a.GetCoverage(ctx, tsk.Number)
				if err != nil {
					t.Fatalf("get first coverage evidence: %v", err)
				}
				if len(firstView.Measurements) != 1 {
					t.Fatalf("first measurement count = %d, want 1", len(firstView.Measurements))
				}
				first := firstView.Measurements[0]
				if first.Accepted || first.RejectCode != domain.ErrMeasurementOutOfWindow {
					t.Fatalf("first evidence = %+v, want rejected with MEASUREMENT_OUT_OF_WINDOW", first)
				}
				if len(firstView.Cells) != 0 {
					t.Fatalf("cells after rejected measurement = %d, want 0", len(firstView.Cells))
				}

				if _, err := a.SubmitMeasurements(ctx, "outside-window-replay", firstTask.Version, tsk.Number, input); err != nil {
					t.Fatalf("replay identical rejected measurement: %v", err)
				}
				replayedView, err := a.GetCoverage(ctx, tsk.Number)
				if err != nil {
					t.Fatalf("get replayed coverage evidence: %v", err)
				}
				if len(replayedView.Measurements) != 1 {
					t.Fatalf("replayed measurement count = %d, want 1", len(replayedView.Measurements))
				}
				if got := replayedView.Measurements[0]; got != first {
					t.Fatalf("replay changed immutable evidence: got %+v, want %+v", got, first)
				}
				if len(replayedView.Cells) != 0 {
					t.Fatalf("cells after replayed rejection = %d, want 0", len(replayedView.Cells))
				}
			},
		},
		{
			name: "same key with different value conflicts without changing evidence",
			run: func(t *testing.T) {
				a, _ := newTestApp(t)
				ctx := context.Background()
				tsk := startExposure(t, a, createAndLock(t, a))

				measured, err := a.SubmitMeasurements(ctx, "first-valid", tsk.Version, tsk.Number, []app.MeasurementInput{{
					PointCode: "SP-1", LogicalSlot: 0, Concentration: 10, Sequence: 7,
				}})
				if err != nil {
					t.Fatalf("submit first measurement: %v", err)
				}
				before, err := a.GetCoverage(ctx, tsk.Number)
				if err != nil || len(before.Measurements) != 1 {
					t.Fatalf("get first evidence: measurements=%d err=%v", len(before.Measurements), err)
				}

				_, err = a.SubmitMeasurements(ctx, "different-value-replay", measured.Version, tsk.Number, []app.MeasurementInput{{
					PointCode: "SP-1", LogicalSlot: 0, Concentration: 11, Sequence: 7,
				}})
				de, ok := err.(*domain.Error)
				if !ok || de.Code != domain.ErrMeasurementConflict {
					t.Fatalf("different value error = %v, want MEASUREMENT_CONFLICT", err)
				}
				after, err := a.GetCoverage(ctx, tsk.Number)
				if err != nil {
					t.Fatalf("get evidence after conflict: %v", err)
				}
				if len(after.Measurements) != 1 || after.Measurements[0] != before.Measurements[0] {
					t.Fatalf("conflict changed evidence: before=%+v after=%+v", before.Measurements, after.Measurements)
				}
			},
		},
		{
			name: "first valid measurement advances its coverage cell",
			run: func(t *testing.T) {
				a, _ := newTestApp(t)
				ctx := context.Background()
				tsk := startExposure(t, a, createAndLock(t, a))

				if _, err := a.SubmitMeasurements(ctx, "first-valid", tsk.Version, tsk.Number, []app.MeasurementInput{{
					PointCode: "SP-1", LogicalSlot: 0, Concentration: 10, Sequence: 7,
				}}); err != nil {
					t.Fatalf("submit first valid measurement: %v", err)
				}
				view, err := a.GetCoverage(ctx, tsk.Number)
				if err != nil {
					t.Fatalf("get coverage: %v", err)
				}
				if len(view.Measurements) != 1 || !view.Measurements[0].Accepted || view.Measurements[0].RejectCode != domain.ErrNone {
					t.Fatalf("valid measurement evidence = %+v, want one accepted record", view.Measurements)
				}
				if len(view.Cells) != 1 || view.Cells[0].PointCode != "SP-1" || view.Cells[0].LogicalSlot != 0 || view.Cells[0].Concentration != 10 {
					t.Fatalf("coverage cells = %+v, want the first valid cell", view.Cells)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}
