package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"granary-phosphine-fumigation-closure/internal/domain"
	"granary-phosphine-fumigation-closure/internal/httpapi"
)

func TestModel_MeasurementGenerationBoundary(t *testing.T) {
	ctx := context.Background()
	a, _ := newTestApp(t)
	tsk := startExposure(t, a, createAndLock(t, a))

	measured, err := a.SubmitMeasurements(ctx, "old-round", tsk.Version, tsk.Number, fullCoverage(1))
	if err != nil {
		t.Fatalf("submit first-round coverage: %v", err)
	}
	supplemented, err := a.CreateSupplement(ctx, "create-supplement", measured.Version, tsk.Number)
	if err != nil {
		t.Fatalf("create supplement: %v", err)
	}
	current, err := a.CompleteSupplement(ctx, "complete-supplement", supplemented.Version, tsk.Number)
	if err != nil {
		t.Fatalf("complete supplement: %v", err)
	}
	if current.Generation != 1 || current.SupplementGeneration != 1 {
		t.Fatalf("current generation = (%d,%d), want (1,1)", current.Generation, current.SupplementGeneration)
	}

	handler := httpapi.NewServer(a, nil).Handler()
	submit := func(operationID string, expectedVersion int64, generation, supplementGeneration int64, measurements []map[string]any) (int, httpapi.ErrorResponse) {
		body, marshalErr := json.Marshal(map[string]any{
			"operation_id":          operationID,
			"expected_version":      expectedVersion,
			"measurements":          measurements,
			"generation":            generation,
			"supplement_generation": supplementGeneration,
		})
		if marshalErr != nil {
			t.Fatalf("marshal measurement request: %v", marshalErr)
		}
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/tasks/%s/measurements", tsk.Number), bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var response httpapi.ErrorResponse
		if rec.Code != http.StatusOK {
			if decodeErr := json.NewDecoder(rec.Body).Decode(&response); decodeErr != nil {
				t.Fatalf("decode error response (status %d): %v", rec.Code, decodeErr)
			}
		}
		return rec.Code, response
	}
	reading := func(point string, slot, concentration, sequence int64) map[string]any {
		return map[string]any{
			"point_code":            point,
			"logical_slot":          slot,
			"concentration":         concentration,
			"sequence":              sequence,
			"generation":            current.Generation,
			"supplement_generation": current.SupplementGeneration,
		}
	}

	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "late_previous_round_is_rejected_and_preserved_as_raw_evidence",
			run: func(t *testing.T) {
				stale := reading("SP-1", 0, 999, 900)
				stale["supplement_generation"] = int64(0)
				status, response := submit("late-old-round", current.Version, current.Generation, 0, []map[string]any{stale})
				if status != http.StatusUnprocessableEntity || response.Code != string(domain.ErrMeasurementGenerationStale) {
					t.Fatalf("late reading response = status %d code %q, want %d %s", status, response.Code, http.StatusUnprocessableEntity, domain.ErrMeasurementGenerationStale)
				}

				view, viewErr := a.GetCoverage(ctx, tsk.Number)
				if viewErr != nil {
					t.Fatalf("get coverage: %v", viewErr)
				}
				for _, cell := range view.Cells {
					if cell.Generation == current.Generation && cell.SupplementGeneration == current.SupplementGeneration {
						t.Fatalf("stale reading wrote current coverage cell: %+v", cell)
					}
				}
				found := false
				for _, measurement := range view.Measurements {
					if measurement.Key.Sequence == 900 {
						found = true
						if measurement.Accepted || measurement.RejectCode != domain.ErrMeasurementGenerationStale || measurement.Key.SupplementGeneration != 0 {
							t.Fatalf("stale raw evidence = %+v", measurement)
						}
					}
				}
				if !found {
					t.Fatal("late reading was not retained as raw rejection evidence")
				}
				got, loadErr := a.GetTask(ctx, tsk.Number)
				if loadErr != nil || got.Version != current.Version {
					t.Fatalf("stale rejection changed task version: task=%+v err=%v", got, loadErr)
				}
			},
		},
		{
			name: "current_round_first_value_advances_coverage",
			run: func(t *testing.T) {
				status, response := submit("current-first", current.Version, current.Generation, current.SupplementGeneration, []map[string]any{reading("SP-1", 0, 10, 100)})
				if status != http.StatusOK {
					t.Fatalf("current first reading = status %d code %q, want 200", status, response.Code)
				}
				current, err = a.GetTask(ctx, tsk.Number)
				if err != nil {
					t.Fatalf("load task: %v", err)
				}
				view, viewErr := a.GetCoverage(ctx, tsk.Number)
				if viewErr != nil {
					t.Fatalf("get coverage: %v", viewErr)
				}
				count := 0
				for _, cell := range view.Cells {
					if cell.Generation == current.Generation && cell.SupplementGeneration == current.SupplementGeneration {
						count++
					}
				}
				if count != 1 {
					t.Fatalf("current coverage cells = %d, want 1", count)
				}
			},
		},
		{
			name: "same_key_same_value_is_idempotent",
			run: func(t *testing.T) {
				status, response := submit("current-replay", current.Version, current.Generation, current.SupplementGeneration, []map[string]any{reading("SP-1", 0, 10, 100)})
				if status != http.StatusOK {
					t.Fatalf("identical replay = status %d code %q, want 200", status, response.Code)
				}
				current, err = a.GetTask(ctx, tsk.Number)
				if err != nil {
					t.Fatalf("load task: %v", err)
				}
				view, viewErr := a.GetCoverage(ctx, tsk.Number)
				if viewErr != nil {
					t.Fatalf("get coverage: %v", viewErr)
				}
				count := 0
				for _, cell := range view.Cells {
					if cell.Generation == current.Generation && cell.SupplementGeneration == current.SupplementGeneration {
						count++
					}
				}
				if count != 1 {
					t.Fatalf("identical replay changed coverage: %d current cells", count)
				}
			},
		},
		{
			name: "same_key_different_value_conflicts",
			run: func(t *testing.T) {
				status, response := submit("current-conflict", current.Version, current.Generation, current.SupplementGeneration, []map[string]any{reading("SP-1", 0, 11, 100)})
				if status != http.StatusUnprocessableEntity || response.Code != string(domain.ErrMeasurementConflict) {
					t.Fatalf("conflicting replay = status %d code %q, want %d %s", status, response.Code, http.StatusUnprocessableEntity, domain.ErrMeasurementConflict)
				}
			},
		},
		{
			name: "only_current_round_drives_integral_and_ventilation",
			run: func(t *testing.T) {
				remaining := []map[string]any{
					reading("SP-1", 1, 10, 101),
					reading("SP-1", 2, 10, 102),
					reading("SP-2", 0, 10, 103),
					reading("SP-2", 1, 10, 104),
					reading("SP-2", 2, 10, 105),
				}
				status, response := submit("current-complete", current.Version, current.Generation, current.SupplementGeneration, remaining)
				if status != http.StatusOK {
					t.Fatalf("complete current coverage = status %d code %q, want 200", status, response.Code)
				}
				current, err = a.GetTask(ctx, tsk.Number)
				if err != nil {
					t.Fatalf("load task: %v", err)
				}
				ventilating, ventErr := a.StartVentilation(ctx, "ventilate-current", current.Version, tsk.Number)
				if ventErr != nil {
					t.Fatalf("current-round integral should permit ventilation: %v", ventErr)
				}
				if ventilating.Status != domain.StatusVentilating {
					t.Fatalf("status = %s, want %s", ventilating.Status, domain.StatusVentilating)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}
