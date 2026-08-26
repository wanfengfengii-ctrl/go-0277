package arbitration

import (
	"reflect"
	"testing"

	"granary-phosphine-fumigation-closure/internal/domain"
)

func TestClosureDeterministic(t *testing.T) {
	g := Graph{Edges: map[string][]string{
		"A": {"B", "C"},
		"B": {"A"},
		"C": {"A"},
		"D": {"E"},
		"E": {"D"},
	}}
	got := g.Closure([]string{"A"})
	want := []string{"A", "B", "C"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("closure = %v, want %v", got, want)
	}
	// Isolated component must not leak into the closure.
	got2 := g.Closure([]string{"D"})
	want2 := []string{"D", "E"}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("closure = %v, want %v", got2, want2)
	}
}

func TestIsReentryEligibleRequiresTwoDistinctQualifiedReviewers(t *testing.T) {
	now := domain.LogicalTime(100)
	qualified := func(id string, approved bool) ReentryReview {
		return ReentryReview{ReviewerID: id, Qualified: true, QualifiedAt: 10, Approved: approved, ReviewedAt: now}
	}

	if IsReentryEligible(nil, now) {
		t.Fatalf("no reviews must not be eligible")
	}
	// Same reviewer twice must not count as two people.
	if IsReentryEligible([]ReentryReview{qualified("R1", true), qualified("R1", true)}, now) {
		t.Fatalf("duplicate reviewer must not be eligible")
	}
	// One approved + one rejected must not be eligible.
	if IsReentryEligible([]ReentryReview{qualified("R1", true), qualified("R2", false)}, now) {
		t.Fatalf("rejected reviewer must not be eligible")
	}
	// Two distinct approved reviewers are eligible.
	if !IsReentryEligible([]ReentryReview{qualified("R1", true), qualified("R2", true)}, now) {
		t.Fatalf("two distinct qualified reviewers must be eligible")
	}
}

func TestIsReentryEligibleQualificationExpiry(t *testing.T) {
	now := domain.LogicalTime(100)
	expired := ReentryReview{ReviewerID: "R1", Qualified: true, QualifiedAt: 200, Approved: true, ReviewedAt: now}
	valid := ReentryReview{ReviewerID: "R2", Qualified: true, QualifiedAt: 10, Approved: true, ReviewedAt: now}
	if IsReentryEligible([]ReentryReview{expired, valid}, now) {
		t.Fatalf("expired qualification must not be eligible")
	}
}
