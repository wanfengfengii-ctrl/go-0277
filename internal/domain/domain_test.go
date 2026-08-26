package domain

import (
	"strconv"
	"testing"
)

func TestSortReasonsDeterministicOrder(t *testing.T) {
	in := []Reason{
		{WarehouseCode: "W2", ZoneCode: "Z1", LogicalSlot: 2, PointCode: "P1", Code: ErrMeasurementConflict},
		{WarehouseCode: "W1", ZoneCode: "Z9", LogicalSlot: 1, PointCode: "P1", Code: ErrMeasurementMissing},
		{WarehouseCode: "W1", ZoneCode: "Z1", LogicalSlot: 1, PointCode: "P1", Code: ErrMeasurementOutOfWindow},
		{WarehouseCode: "W1", ZoneCode: "Z1", LogicalSlot: 1, PointCode: "P0", Code: ErrMeasurementConflict},
	}
	SortReasons(in)

	wantOrder := []string{"W1/Z1/1/P0", "W1/Z1/1/P1", "W1/Z9/1/P1", "W2/Z1/2/P1"}
	for i, r := range in {
		if i >= len(wantOrder) {
			t.Fatalf("too many reasons: %d", len(in))
		}
		got := r.WarehouseCode + "/" + r.ZoneCode + "/" + strconv.FormatInt(r.LogicalSlot, 10) + "/" + r.PointCode
		if got != wantOrder[i] {
			t.Fatalf("reason %d = %s, want %s", i, got, wantOrder[i])
		}
	}
}

func TestTaskStatusIsTerminal(t *testing.T) {
	terminal := []TaskStatus{StatusCompleted, StatusRiskIsolated, StatusCancelled}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Fatalf("%s should be terminal", s)
		}
	}
	nonTerminal := []TaskStatus{StatusPendingLock, StatusApplying, StatusExposureMaintain, StatusVentilating}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Fatalf("%s should not be terminal", s)
		}
	}
}
