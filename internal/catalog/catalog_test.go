package catalog

import "testing"

func TestPesticideBatchBalanced(t *testing.T) {
	b := PesticideBatch{
		InitialMg:   1000,
		AvailableMg: 700,
		ReservedMg:  100,
		AppliedMg:   250,
		ReturnedMg:  50,
		AdjustedMg:  0,
	}
	// 700 + 100 + 250 - 50 + 0 = 1000
	if !b.Balanced() {
		t.Fatalf("batch should be balanced")
	}
	b.AppliedMg = 260
	if b.Balanced() {
		t.Fatalf("batch should be unbalanced")
	}
}

func TestSummarizeDeterministic(t *testing.T) {
	w := Warehouse{
		Code:             "WH-01",
		RatedCapacityDm3: 500000,
		StructureVersion: 3,
		AllowedGrains:    []GrainType{"WHEAT", "RICE"},
		Zones: []Zone{
			{Code: "Z2", Warehouse: "WH-01", CapacityDm3: 250000},
			{Code: "Z1", Warehouse: "WH-01", CapacityDm3: 250000},
		},
		Edges: []NeighborEdge{
			{From: "Z2", To: "Z1"},
		},
		Devices: []Device{
			{Code: "FAN-2", Warehouse: "WH-01", Kind: DeviceFanCircuit},
			{Code: "CAGE-1", Warehouse: "WH-01", Kind: DeviceDosingCage},
		},
		SamplingPoints: []SamplingPoint{
			{Code: "SP-B", Warehouse: "WH-01", Zone: "Z2"},
			{Code: "SP-A", Warehouse: "WH-01", Zone: "Z1"},
		},
	}
	r := FumigationRule{Version: 7, GrainTypes: []GrainType{"WHEAT", "RICE"}}

	s1 := Summarize(w, r)
	s2 := Summarize(w, r)
	if s1.Digest != s2.Digest {
		t.Fatalf("summary must be deterministic: %q != %q", s1.Digest, s2.Digest)
	}
	if s1.StructureVersion != 3 || s1.RuleVersion != 7 {
		t.Fatalf("summary versions wrong: %+v", s1)
	}

	// Changing the rule version must change the digest (stale detection).
	r2 := r
	r2.Version = 8
	if Summarize(w, r2).Digest == s1.Digest {
		t.Fatalf("rule version change must alter summary digest")
	}
}

func TestNeighborEdgeKeyCanonical(t *testing.T) {
	a := NeighborEdge{From: "Z1", To: "Z2"}
	b := NeighborEdge{From: "Z2", To: "Z1"}
	if a.Key() != b.Key() {
		t.Fatalf("edge key must be order-independent: %q != %q", a.Key(), b.Key())
	}
}
