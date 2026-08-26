// Package catalog implements the warehouse and fumigation rule catalogue:
// warehouse capacity, grain constraints, stack-height conversion, zone and
// neighbour graph, device topology, sampling points, pesticide batches and
// versioned rules, together with the canonical summary used for task locking
// and stale-rule detection.
package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// GrainType is the name of an allowed grain species.
type GrainType string

// Zone is a grain-pile partition inside a warehouse.
type Zone struct {
	Code        string `json:"code"`
	Warehouse   string `json:"warehouse"`
	CapacityDm3 int64  `json:"capacity_dm3"` // cubic decimetres
}

// NeighborEdge connects two zones (or an adjacent warehouse) for leak
// propagation purposes. Direction is irrelevant; edges are canonicalised by
// sorting their endpoint codes.
type NeighborEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Key returns a canonical, order-independent edge key.
func (e NeighborEdge) Key() string {
	a, b := e.From, e.To
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// DeviceKind classifies the equipment that participates in fumigation.
type DeviceKind string

const (
	DeviceDosingCage   DeviceKind = "DOSING_CAGE"
	DeviceFanCircuit   DeviceKind = "FAN_CIRCUIT"
	DeviceSamplingLine DeviceKind = "SAMPLING_LINE"
)

// Device is a piece of fumigation equipment with a stable code.
type Device struct {
	Code      string     `json:"code"`
	Warehouse string     `json:"warehouse"`
	Kind      DeviceKind `json:"kind"`
	// MutuallyExclusiveWith lists device codes that cannot run in parallel
	// within the same task.
	MutuallyExclusiveWith []string `json:"mutually_exclusive_with"`
}

// SamplingPoint is a sensor location whose measurements drive coverage.
type SamplingPoint struct {
	Code      string `json:"code"`
	Warehouse string `json:"warehouse"`
	Zone      string `json:"zone"`
}

// PesticideBatch is a lot of phosphine pesticide tracked by integer mass in
// milligrams.
type PesticideBatch struct {
	Code        string `json:"code"`
	InitialMg   int64  `json:"initial_mg"`
	AvailableMg int64  `json:"available_mg"`
	ReservedMg  int64  `json:"reserved_mg"`
	AppliedMg   int64  `json:"applied_mg"`
	ReturnedMg  int64  `json:"returned_mg"`
	AdjustedMg  int64  `json:"adjusted_mg"`
}

// Balanced reports whether the batch satisfies the deterministic conservation
// identity: initial = available + reserved + applied - returned + adjusted.
func (b PesticideBatch) Balanced() bool {
	return b.InitialMg == b.AvailableMg+b.ReservedMg+b.AppliedMg-b.ReturnedMg+b.AdjustedMg
}

// FumigationRule is the versioned rule set governing a fumigation. It is the
// source of the target concentration-duration dose, thresholds, sampling
// windows, retry policy and fixed-point arithmetic parameters.
type FumigationRule struct {
	Version             int64       `json:"version"`
	GrainTypes          []GrainType `json:"grain_types"`
	MinHeightDm         int64       `json:"min_height_dm"`
	MaxHeightDm         int64       `json:"max_height_dm"`
	CapacityFactor      int64       `json:"capacity_factor"` // parts-per-thousand applied to nominal capacity
	TargetDoseCT        int64       `json:"target_dose_ct"`  // microgram * second / cubic decimetre (integer)
	SamplingWindowSlots int64       `json:"sampling_window_slots"`
	SlotDurationSec     int64       `json:"slot_duration_sec"` // seconds covered by one logical slot
	LeakThreshold       int64       `json:"leak_threshold"`    // micrograms per cubic metre
	ReentryThreshold    int64       `json:"reentry_threshold"`
	SafeContinuousSlots int64       `json:"safe_continuous_slots"`
	RetryMaxAttempts    int64       `json:"retry_max_attempts"`
	RetryBaseDelaySlots int64       `json:"retry_base_delay_slots"`
}

// Warehouse describes a flat grain warehouse and its catalogued structure.
type Warehouse struct {
	Code             string          `json:"code"`
	RatedCapacityDm3 int64           `json:"rated_capacity_dm3"`
	AllowedGrains    []GrainType     `json:"allowed_grains"`
	StructureVersion int64           `json:"structure_version"`
	Zones            []Zone          `json:"zones"`
	Edges            []NeighborEdge  `json:"edges"`
	Devices          []Device        `json:"devices"`
	SamplingPoints   []SamplingPoint `json:"sampling_points"`
}

// Summary is the canonical, order-stable digest of a warehouse and its active
// rule. It is fixed into a task snapshot at lock time so that later catalogue
// changes never silently affect a locked task.
type Summary struct {
	WarehouseCode    string `json:"warehouse_code"`
	StructureVersion int64  `json:"structure_version"`
	RuleVersion      int64  `json:"rule_version"`
	Digest           string `json:"digest"`
}

// Summarize computes the canonical summary. All composite parts are sorted by
// their stable business keys before hashing so the digest is deterministic.
func Summarize(w Warehouse, r FumigationRule) Summary {
	var sb []byte
	sb = append(sb, w.Code...)
	sb = append(sb, '|')
	sb = appendInt64(sb, w.StructureVersion)
	sb = append(sb, '|')
	sb = appendInt64(sb, r.Version)

	zones := append([]Zone(nil), w.Zones...)
	sort.Slice(zones, func(i, j int) bool { return zones[i].Code < zones[j].Code })
	for _, z := range zones {
		sb = append(sb, ';')
		sb = append(sb, z.Code...)
		sb = appendInt64(sb, z.CapacityDm3)
	}

	edges := append([]NeighborEdge(nil), w.Edges...)
	sort.Slice(edges, func(i, j int) bool { return edges[i].Key() < edges[j].Key() })
	for _, e := range edges {
		sb = append(sb, ';')
		sb = append(sb, e.Key()...)
	}

	devices := append([]Device(nil), w.Devices...)
	sort.Slice(devices, func(i, j int) bool { return devices[i].Code < devices[j].Code })
	for _, d := range devices {
		sb = append(sb, ';')
		sb = append(sb, d.Code...)
		sb = append(sb, string(d.Kind)...)
	}

	points := append([]SamplingPoint(nil), w.SamplingPoints...)
	sort.Slice(points, func(i, j int) bool { return points[i].Code < points[j].Code })
	for _, p := range points {
		sb = append(sb, ';')
		sb = append(sb, p.Code...)
		sb = append(sb, p.Zone...)
	}

	sum := sha256.Sum256(sb)
	return Summary{
		WarehouseCode:    w.Code,
		StructureVersion: w.StructureVersion,
		RuleVersion:      r.Version,
		Digest:           hex.EncodeToString(sum[:]),
	}
}

func appendInt64(b []byte, v int64) []byte {
	return append(b, fmt.Sprintf("%d", v)...)
}
