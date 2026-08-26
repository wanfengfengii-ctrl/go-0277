// Package coverage implements the concentration coverage and dose integration
// engine: the zone-logical-slot coverage grid, immutable measurement
// generations, and checked integer fixed-point arithmetic for capacity
// conversion, trapezoidal dose integration, rounding and overflow handling.
package coverage

import (
	"granary-phosphine-fumigation-closure/internal/domain"
)

// MeasurementKey uniquely and immutably identifies a raw measurement. It is
// composed of task number, task generation, supplement generation, sampling
// point, logical slot and measurement sequence.
type MeasurementKey struct {
	TaskNumber           string
	Generation           int64
	SupplementGeneration int64
	PointCode            string
	LogicalSlot          int64
	Sequence             int64
}

// Measurement is an immutable raw reading. A reading is either accepted into
// the coverage grid or kept purely as rejection evidence.
type Measurement struct {
	Key           MeasurementKey
	Concentration int64 // integer micrograms per cubic metre
	Accepted      bool
	RejectCode    domain.ErrorCode
	ReceivedAt    domain.LogicalTime
}

// CoverageCell is the first accepted value for a zone, logical slot and
// sampling point. Duplicates, out-of-window values and stale generations are
// never written here.
type CoverageCell struct {
	TaskNumber           string
	WarehouseCode        string
	ZoneCode             string
	LogicalSlot          int64
	PointCode            string
	Concentration        int64
	Generation           int64
	SupplementGeneration int64
}

// ExposureIntegral records one trapezoidal concentration-time product, along
// with the fixed-point inputs and rounding remainder so the result can be
// recomputed from persisted evidence.
type ExposureIntegral struct {
	TaskNumber        string
	ZoneCode          string
	Generation        int64
	ConcentrationA    int64
	ConcentrationB    int64
	DurationSec       int64
	ProductCT         int64 // microgram-second per cubic decimetre
	RoundingRemainder int64
	AccumulatedCT     int64
}

// DoseLedgerEntry is one conservation-tracked mass movement in milligrams.
type DoseLedgerEntry struct {
	TaskNumber  string
	BatchCode   string
	ZoneCode    string
	Generation  int64
	ReservedMg  int64
	AppliedMg   int64
	ReturnedMg  int64
	AdjustedMg  int64
	OperationID string
}

// ApplicationRecord captures a planned or executed per-zone application.
type ApplicationRecord struct {
	TaskNumber string
	BatchCode  string
	ZoneCode   string
	Generation int64
	MassMg     int64
	Applied    bool
}

// mulChecked multiplies two int64 values and reports overflow.
func mulChecked(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	r := a * b
	if r/b != a {
		return 0, false
	}
	return r, true
}

// halfUpDivide divides a by b using half-up rounding for the absolute value
// and preserving the sign, exactly as required by the fixed-point rules.
func halfUpDivide(a, b int64) (int64, bool) {
	if b == 0 {
		return 0, false
	}
	if a == 0 {
		return 0, true
	}
	neg := (a < 0) != (b < 0)
	aa := a
	if aa < 0 {
		aa = -aa
	}
	bb := b
	if bb < 0 {
		bb = -bb
	}
	q := aa / bb
	r := aa % bb
	if r*2 >= bb {
		q++
	}
	if neg {
		q = -q
	}
	return q, true
}

// Trapezoid computes the concentration-time product for one interval using
// the checked integer rules. It returns an error code on division-by-zero or
// overflow and never writes partial state.
func Trapezoid(ca, cb, durationSec int64) (product int64, code domain.ErrorCode) {
	if durationSec < 0 || ca < 0 || cb < 0 {
		return 0, domain.ErrMeasurementOutOfWindow
	}
	sum, ok := addChecked(ca, cb)
	if !ok {
		return 0, domain.ErrFixedPointOverflow
	}
	prod, ok := mulChecked(sum, durationSec)
	if !ok {
		return 0, domain.ErrFixedPointOverflow
	}
	q, ok := halfUpDivide(prod, 2)
	if !ok {
		return 0, domain.ErrFixedPointOverflow
	}
	return q, domain.ErrNone
}

// ConvertCapacity applies the rule capacity factor (parts-per-thousand) to a
// nominal capacity in cubic decimetres, with checked multiplication.
func ConvertCapacity(nominalDm3, factorPerThousand int64) (int64, domain.ErrorCode) {
	prod, ok := mulChecked(nominalDm3, factorPerThousand)
	if !ok {
		return 0, domain.ErrFixedPointOverflow
	}
	q, ok := halfUpDivide(prod, 1000)
	if !ok {
		return 0, domain.ErrFixedPointOverflow
	}
	return q, domain.ErrNone
}

// SupplementMassMg derives the deterministic supplement mass in milligrams for
// a zone from its dose shortfall (target minus actual concentration-time dose)
// and its capacity, using checked integer arithmetic and half-up rounding. A
// zone that already meets its target yields zero.
func SupplementMassMg(targetCT, actualCT, capacityDm3 int64) (int64, domain.ErrorCode) {
	if actualCT >= targetCT {
		return 0, domain.ErrNone
	}
	deficit := targetCT - actualCT
	prod, ok := mulChecked(deficit, capacityDm3)
	if !ok {
		return 0, domain.ErrFixedPointOverflow
	}
	q, ok := halfUpDivide(prod, 1000)
	if !ok {
		return 0, domain.ErrFixedPointOverflow
	}
	return q, domain.ErrNone
}

func addChecked(a, b int64) (int64, bool) {
	r := a + b
	// Positive overflow: both positive and the result wrapped negative.
	if a > 0 && b > 0 && r < 0 {
		return 0, false
	}
	// Negative overflow: both negative and the result wrapped positive.
	if a < 0 && b < 0 && r > 0 {
		return 0, false
	}
	return r, true
}
