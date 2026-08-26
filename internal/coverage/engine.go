package coverage

import (
	"fmt"
	"sort"

	"granary-phosphine-fumigation-closure/internal/catalog"
	"granary-phosphine-fumigation-closure/internal/domain"
)

// cellKey identifies one coverage cell: a locked sampling point at a logical
// slot within a zone.
type cellKey struct {
	zone  string
	slot  int64
	point string
}

func (k cellKey) String() string {
	return fmt.Sprintf("%s|%d|%s", k.zone, k.slot, k.point)
}

// Engine accumulates coverage cells and dose integrals for a single locked
// task snapshot. It is a pure in-memory domain object: the application layer
// reconstructs it from persisted evidence, applies new measurements, then
// persists the resulting cells and integrals inside the same transaction.
type Engine struct {
	warehouseCode string
	generation    int64
	supplement    int64
	samplingSlots int64
	slotDuration  int64
	points        map[string]catalog.SamplingPoint // by code
	zones         map[string][]string              // zone code -> sorted point codes
	cells         map[cellKey]int64
	seen          map[string]int64 // measurement key -> concentration
}

// NewEngine builds an engine from the frozen snapshot facts it needs.
func NewEngine(warehouseCode string, generation, supplement, samplingSlots, slotDuration int64, points []catalog.SamplingPoint) *Engine {
	e := &Engine{
		warehouseCode: warehouseCode,
		generation:    generation,
		supplement:    supplement,
		samplingSlots: samplingSlots,
		slotDuration:  slotDuration,
		points:        make(map[string]catalog.SamplingPoint),
		zones:         make(map[string][]string),
		cells:         make(map[cellKey]int64),
		seen:          make(map[string]int64),
	}
	for _, p := range points {
		e.points[p.Code] = p
		e.zones[p.Zone] = append(e.zones[p.Zone], p.Code)
	}
	for z := range e.zones {
		sort.Strings(e.zones[z])
	}
	return e
}

// Zones returns the zone codes known to the engine in sorted order.
func (e *Engine) Zones() []string {
	out := make([]string, 0, len(e.zones))
	for z := range e.zones {
		out = append(out, z)
	}
	sort.Strings(out)
	return out
}

// LoadCell rehydrates an already-persisted coverage cell so the engine can
// resume integration after a restart.
func (e *Engine) LoadCell(c CoverageCell) {
	e.cells[cellKey{zone: c.ZoneCode, slot: c.LogicalSlot, point: c.PointCode}] = c.Concentration
}

// LoadMeasurement rehydrates a persisted raw measurement so idempotency and
// conflict detection survive restarts.
func (e *Engine) LoadMeasurement(m Measurement) {
	e.seen[m.Key.String()] = m.Concentration
}

// Accept validates one raw measurement and, when it is the first valid value
// for its cell, advances coverage. It returns whether the cell advanced and
// an error code describing a rejection (ErrNone when accepted or idempotent).
func (e *Engine) Accept(m Measurement) (advanced bool, code domain.ErrorCode) {
	// Idempotent replay: identical key with identical value.
	if prev, ok := e.seen[m.Key.String()]; ok {
		if prev == m.Concentration {
			return false, domain.ErrNone
		}
		return false, domain.ErrMeasurementConflict
	}
	e.seen[m.Key.String()] = m.Concentration

	point, locked := e.points[m.Key.PointCode]
	if !locked {
		return false, domain.ErrMeasurementMissing
	}
	if m.Key.Generation != e.generation || m.Key.SupplementGeneration != e.supplement {
		return false, domain.ErrMeasurementGenerationStale
	}
	if m.Key.LogicalSlot < 0 || m.Key.LogicalSlot >= e.samplingSlots {
		return false, domain.ErrMeasurementOutOfWindow
	}
	if m.Concentration < 0 {
		return false, domain.ErrMeasurementOutOfWindow
	}

	key := cellKey{zone: point.Zone, slot: m.Key.LogicalSlot, point: point.Code}
	if _, covered := e.cells[key]; covered {
		// A second valid reading for an already-covered cell is duplicate
		// evidence; it must not overwrite the first value.
		return false, domain.ErrMeasurementConflict
	}
	e.cells[key] = m.Concentration
	return true, domain.ErrNone
}

// CoveredSlots reports how many cells are covered for a zone.
func (e *Engine) CoveredSlots(zone string) int {
	pts := e.zones[zone]
	if len(pts) == 0 {
		return 0
	}
	count := 0
	for slot := int64(0); slot < e.samplingSlots; slot++ {
		full := true
		for _, p := range pts {
			if _, ok := e.cells[cellKey{zone: zone, slot: slot, point: p}]; !ok {
				full = false
				break
			}
		}
		if full {
			count++
		}
	}
	return count
}

// CoverageComplete reports whether every point of a zone has a value in every
// slot of the locked sampling window.
func (e *Engine) CoverageComplete(zone string) bool {
	return e.CoveredSlots(zone) == int(e.samplingSlots)
}

// Gaps returns the deterministically ordered list of missing cells for a zone:
// each missing (slot, point) pair.
func (e *Engine) Gaps(zone string) []string {
	pts := e.zones[zone]
	var out []string
	for slot := int64(0); slot < e.samplingSlots; slot++ {
		for _, p := range pts {
			if _, ok := e.cells[cellKey{zone: zone, slot: slot, point: p}]; !ok {
				out = append(out, fmt.Sprintf("%s/slot=%d/point=%s", zone, slot, p))
			}
		}
	}
	return out
}

// zoneConcentration returns the representative concentration of a zone at a
// slot as the minimum across its locked points (a conservative safety
// reading), or -1 when the slot is incomplete.
func (e *Engine) zoneConcentration(zone string, slot int64) int64 {
	pts := e.zones[zone]
	if len(pts) == 0 {
		return -1
	}
	min := int64(-1)
	for _, p := range pts {
		v, ok := e.cells[cellKey{zone: zone, slot: slot, point: p}]
		if !ok {
			return -1
		}
		if min < 0 || v < min {
			min = v
		}
	}
	return min
}

// Integrate computes the trapezoidal concentration-time dose for a zone over
// its full locked window. It returns the per-interval integrals, the
// accumulated total and whether the coverage was complete. Any arithmetic
// failure aborts with a stable code and no partial accumulation.
func (e *Engine) Integrate(zone string) ([]ExposureIntegral, int64, bool, domain.ErrorCode) {
	if !e.CoverageComplete(zone) {
		return nil, 0, false, domain.ErrMeasurementMissing
	}
	var out []ExposureIntegral
	var acc int64
	for slot := int64(0); slot < e.samplingSlots-1; slot++ {
		ca := e.zoneConcentration(zone, slot)
		cb := e.zoneConcentration(zone, slot+1)
		product, code := Trapezoid(ca, cb, e.slotDuration)
		if code != domain.ErrNone {
			return nil, 0, false, code
		}
		next, ok := addChecked(acc, product)
		if !ok {
			return nil, 0, false, domain.ErrFixedPointOverflow
		}
		acc = next
		out = append(out, ExposureIntegral{
			ZoneCode:       zone,
			Generation:     e.generation,
			ConcentrationA: ca,
			ConcentrationB: cb,
			DurationSec:    e.slotDuration,
			ProductCT:      product,
			AccumulatedCT:  acc,
		})
	}
	return out, acc, true, domain.ErrNone
}

// keyString renders a measurement key in its canonical immutable form.
func (k MeasurementKey) String() string {
	return fmt.Sprintf("%s|%d|%d|%s|%d|%d", k.TaskNumber, k.Generation, k.SupplementGeneration, k.PointCode, k.LogicalSlot, k.Sequence)
}
