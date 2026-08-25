// Package arbitration implements the leak, ventilation and terminal
// arbitration: leak propagation closure over the neighbour graph, risk
// closure, continuous safe ventilation coverage, two-person reentry review
// and the single terminal decision.
package arbitration

import (
	"granary-phosphine-fumigation-closure/internal/domain"
)

// LeakEvidence records a leak source and its threshold comparison.
type LeakEvidence struct {
	TaskNumber    string
	SourceCode    string
	MeasuredValue int64
	Threshold     int64
	Exceeded      bool
	RecordedAt    domain.LogicalTime
}

// RiskClosure captures the propagation closure computed from the locked
// neighbour graph and the risk-shutdown evidence for each affected fan
// circuit.
type RiskClosure struct {
	TaskNumber     string
	ClosureCodes   []string
	FrozenCircuits []string
	FrozenAt       domain.LogicalTime
	Closed         bool
}

// VentilationEvidence records one safe-window sample and its continuity
// result. A missing or over-threshold sample resets continuity.
type VentilationEvidence struct {
	TaskNumber     string
	PointCode      string
	LogicalSlot    int64
	Concentration  int64
	BelowThreshold bool
	Continuous     bool
}

// ReentryReview is one person's review of readiness to reenter.
type ReentryReview struct {
	TaskNumber  string
	ReviewerID  string
	Qualified   bool
	QualifiedAt domain.LogicalTime
	Approved    bool
	ReviewedAt  domain.LogicalTime
}

// Graph is the locked neighbour graph used to compute leak propagation.
type Graph struct {
	Edges map[string][]string
}

// Closure computes the connected component reachable from the given seed
// nodes. The result is deterministic because neighbours are traversed in
// sorted order.
func (g Graph) Closure(seeds []string) []string {
	seen := map[string]bool{}
	stack := append([]string(nil), seeds...)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		neighbors := append([]string(nil), g.Edges[n]...)
		sortStrings(neighbors)
		for _, m := range neighbors {
			if !seen[m] {
				stack = append(stack, m)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// IsReentryEligible checks the two-person review rule: two distinct,
// qualified-at-review-time and approved reviewers are required.
func IsReentryEligible(reviews []ReentryReview, now domain.LogicalTime) bool {
	seen := map[string]bool{}
	approved := 0
	for _, r := range reviews {
		if !r.Approved || !r.Qualified || r.QualifiedAt > now {
			continue
		}
		if seen[r.ReviewerID] {
			continue
		}
		seen[r.ReviewerID] = true
		approved++
	}
	return approved >= 2
}
