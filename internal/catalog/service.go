package catalog

import (
	"sort"

	"granary-phosphine-fumigation-closure/internal/domain"
)

// allowsGrain reports whether the rule permits the grain species.
func allowsGrain(allowed []GrainType, grain GrainType) bool {
	for _, g := range allowed {
		if g == grain {
			return true
		}
	}
	return false
}

// warehouseAllowsGrain reports whether the warehouse permits the grain species.
func warehouseAllowsGrain(w Warehouse, grain GrainType) bool {
	for _, g := range w.AllowedGrains {
		if g == grain {
			return true
		}
	}
	return false
}

// ValidateLock checks the lock preconditions that depend only on catalogue
// data: grain species, stack-height range and the freshness of the submitted
// summary. It returns every violated reason with a stable sort key; an empty
// slice means the lock may proceed.
func ValidateLock(w Warehouse, r FumigationRule, grain GrainType, stackHeightDm int64, submitted Summary) []domain.Reason {
	var reasons []domain.Reason

	if !warehouseAllowsGrain(w, grain) || !allowsGrain(r.GrainTypes, grain) {
		reasons = append(reasons, domain.Reason{
			WarehouseCode: w.Code,
			Code:          domain.ErrGrainTypeMismatch,
			Message:       "grain type not allowed for warehouse or rule",
		})
	}
	if stackHeightDm < r.MinHeightDm || stackHeightDm > r.MaxHeightDm {
		reasons = append(reasons, domain.Reason{
			WarehouseCode: w.Code,
			Code:          domain.ErrWarehouseCapacityMismatch,
			Message:       "stack height outside rule range",
		})
	}

	current := Summarize(w, r)
	if submitted.Digest != current.Digest {
		reasons = append(reasons, domain.Reason{
			WarehouseCode: w.Code,
			Code:          domain.ErrRuleSnapshotStale,
			Message:       "submitted summary is stale",
		})
	}

	domain.SortReasons(reasons)
	return reasons
}

// SortedZoneCodes returns the zone codes in ascending order, the canonical
// order used everywhere a deterministic traversal is required.
func SortedZoneCodes(zones []Zone) []string {
	out := make([]string, 0, len(zones))
	for _, z := range zones {
		out = append(out, z.Code)
	}
	sort.Strings(out)
	return out
}

// ZoneLookup indexes zones by code for quick deterministic access.
func ZoneLookup(zones []Zone) map[string]Zone {
	m := make(map[string]Zone, len(zones))
	for _, z := range zones {
		m[z.Code] = z
	}
	return m
}

// PointLookup indexes sampling points by code.
func PointLookup(points []SamplingPoint) map[string]SamplingPoint {
	m := make(map[string]SamplingPoint, len(points))
	for _, p := range points {
		m[p.Code] = p
	}
	return m
}

// DeviceLookup indexes devices by code.
func DeviceLookup(devices []Device) map[string]Device {
	m := make(map[string]Device, len(devices))
	for _, d := range devices {
		m[d.Code] = d
	}
	return m
}

// BatchLookup indexes pesticide batches by code.
func BatchLookup(batches []PesticideBatch) map[string]PesticideBatch {
	m := make(map[string]PesticideBatch, len(batches))
	for _, b := range batches {
		m[b.Code] = b
	}
	return m
}
