package app_test

import (
	"context"
	"math"
	"reflect"
	"testing"

	"granary-phosphine-fumigation-closure/internal/app"
	"granary-phosphine-fumigation-closure/internal/bboltstore"
	"granary-phosphine-fumigation-closure/internal/coverage"
	"granary-phosphine-fumigation-closure/internal/domain"
)

func TestModel_CoverageDecisionPersistsIntegralEvidence(t *testing.T) {
	tests := []struct {
		name          string
		decision      string
		concentration int64
		incomplete    bool
		wantCode      domain.ErrorCode
		wantStatus    domain.TaskStatus
		wantProduct   int64
	}{
		{
			name:          "supplement decision",
			decision:      "supplement",
			concentration: 1,
			wantStatus:    domain.StatusSupplementing,
			wantProduct:   60,
		},
		{
			name:          "ventilation decision",
			decision:      "ventilation",
			concentration: 10,
			wantStatus:    domain.StatusVentilating,
			wantProduct:   600,
		},
		{
			name:          "supplement has no demand",
			decision:      "supplement",
			concentration: 10,
			wantStatus:    domain.StatusExposureMaintain,
			wantProduct:   600,
		},
		{
			name:          "incomplete coverage rejected without partial integrals",
			decision:      "ventilation",
			concentration: 10,
			incomplete:    true,
			wantCode:      domain.ErrMeasurementMissing,
		},
		{
			name:          "overflow rejected without partial integrals",
			decision:      "ventilation",
			concentration: math.MaxInt64,
			wantCode:      domain.ErrFixedPointOverflow,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			a, path := newTestApp(t)
			tsk := startExposure(t, a, createAndLock(t, a))

			inputs := fullCoverage(tc.concentration)
			if tc.incomplete {
				inputs = inputs[:len(inputs)-1]
			}
			measured, err := a.SubmitMeasurements(ctx, "integral-measurements", tsk.Version, tsk.Number, inputs)
			if err != nil {
				t.Fatalf("submit measurements: %v", err)
			}

			decide := func() (domain.TaskStatus, error) {
				if tc.decision == "supplement" {
					got, err := a.CreateSupplement(ctx, "integral-decision", measured.Version, tsk.Number)
					return got.Status, err
				}
				got, err := a.StartVentilation(ctx, "integral-decision", measured.Version, tsk.Number)
				return got.Status, err
			}

			status, err := decide()
			if tc.wantCode != domain.ErrNone {
				de, ok := err.(*domain.Error)
				if !ok || de.Code != tc.wantCode {
					t.Fatalf("decision error = %v, want code %s", err, tc.wantCode)
				}
			} else {
				if err != nil {
					t.Fatalf("decision: %v", err)
				}
				if status != tc.wantStatus {
					t.Fatalf("decision status = %s, want %s", status, tc.wantStatus)
				}
				status, err = decide()
				if err != nil || status != tc.wantStatus {
					t.Fatalf("idempotent retry = (%s, %v), want (%s, nil)", status, err, tc.wantStatus)
				}
			}

			want := []coverage.ExposureIntegral(nil)
			if tc.wantCode == domain.ErrNone {
				want = []coverage.ExposureIntegral{
					{TaskNumber: tsk.Number, ZoneCode: "Z1", Generation: tsk.Generation, ConcentrationA: tc.concentration, ConcentrationB: tc.concentration, DurationSec: 60, ProductCT: tc.wantProduct, AccumulatedCT: tc.wantProduct},
					{TaskNumber: tsk.Number, ZoneCode: "Z1", Generation: tsk.Generation, ConcentrationA: tc.concentration, ConcentrationB: tc.concentration, DurationSec: 60, ProductCT: tc.wantProduct, AccumulatedCT: tc.wantProduct * 2},
					{TaskNumber: tsk.Number, ZoneCode: "Z2", Generation: tsk.Generation, ConcentrationA: tc.concentration, ConcentrationB: tc.concentration, DurationSec: 60, ProductCT: tc.wantProduct, AccumulatedCT: tc.wantProduct},
					{TaskNumber: tsk.Number, ZoneCode: "Z2", Generation: tsk.Generation, ConcentrationA: tc.concentration, ConcentrationB: tc.concentration, DurationSec: 60, ProductCT: tc.wantProduct, AccumulatedCT: tc.wantProduct * 2},
				}
			}

			view, err := a.GetCoverage(ctx, tsk.Number)
			if err != nil {
				t.Fatalf("coverage before restart: %v", err)
			}
			if !reflect.DeepEqual(view.Integrals, want) {
				t.Fatalf("integrals before restart = %#v, want %#v", view.Integrals, want)
			}

			if err := a.Store.Close(); err != nil {
				t.Fatalf("close before restart: %v", err)
			}
			db, err := bboltstore.Open(path)
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if _, err := db.Recover(ctx); err != nil {
				t.Fatalf("recover: %v", err)
			}
			restarted := app.New(db, nil)
			view, err = restarted.GetCoverage(ctx, tsk.Number)
			if err != nil {
				t.Fatalf("coverage after restart: %v", err)
			}
			if !reflect.DeepEqual(view.Integrals, want) {
				t.Fatalf("integrals after restart = %#v, want %#v", view.Integrals, want)
			}
		})
	}
}
