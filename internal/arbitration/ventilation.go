package arbitration

import "sort"

// sortedBySlot orders ventilation samples for one point by ascending slot.
func sortedBySlot(samples []VentilationEvidence) []VentilationEvidence {
	out := append([]VentilationEvidence(nil), samples...)
	sort.Slice(out, func(i, j int) bool { return out[i].LogicalSlot < out[j].LogicalSlot })
	return out
}

// ContinuousBelowThreshold reports whether the samples for a single safety
// point form a run of at least requiredSlots consecutive logical slots, each
// strictly below the threshold. A missing or over-threshold sample resets the
// run to zero, exactly as required for safe reentry.
func ContinuousBelowThreshold(samples []VentilationEvidence, threshold, requiredSlots int64) bool {
	if requiredSlots <= 0 {
		return false
	}
	ordered := sortedBySlot(samples)
	run := int64(0)
	var prev int64 = -2 // no previous slot seen yet
	for _, s := range ordered {
		if s.Concentration >= threshold {
			run = 0
			prev = s.LogicalSlot
			continue
		}
		if prev != -2 && s.LogicalSlot != prev+1 {
			// A gap in the slot sequence breaks continuity.
			run = 0
		}
		run++
		prev = s.LogicalSlot
		if run >= requiredSlots {
			return true
		}
	}
	return false
}

// AllPointsContinuousBelowThreshold checks every required safety point reaches
// the continuous safe window. If any point fails, the whole ventilation is
// incomplete.
func AllPointsContinuousBelowThreshold(byPoint map[string][]VentilationEvidence, points []string, threshold, requiredSlots int64) bool {
	for _, p := range points {
		if !ContinuousBelowThreshold(byPoint[p], threshold, requiredSlots) {
			return false
		}
	}
	return true
}
