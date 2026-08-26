package coverage

import (
	"testing"

	"granary-phosphine-fumigation-closure/internal/domain"
)

func TestTrapezoidBasic(t *testing.T) {
	// concentration 100 µg/m³, duration 60 s -> (100+100)*60/2 = 6000
	got, code := Trapezoid(100, 100, 60)
	if code != domain.ErrNone {
		t.Fatalf("unexpected code %s", code)
	}
	if got != 6000 {
		t.Fatalf("got %d, want 6000", got)
	}
}

func TestTrapezoidHalfUpRounding(t *testing.T) {
	// (0 + 1) * 1 / 2 = 0.5 -> half-up rounds to 1
	got, code := Trapezoid(0, 1, 1)
	if code != domain.ErrNone {
		t.Fatalf("unexpected code %s", code)
	}
	if got != 1 {
		t.Fatalf("got %d, want 1", got)
	}
}

func TestTrapezoidNegativeRejected(t *testing.T) {
	if _, code := Trapezoid(-1, 5, 10); code != domain.ErrMeasurementOutOfWindow {
		t.Fatalf("negative concentration must be rejected, got %s", code)
	}
	if _, code := Trapezoid(5, 5, -10); code != domain.ErrMeasurementOutOfWindow {
		t.Fatalf("negative duration must be rejected, got %s", code)
	}
}

func TestTrapezoidOverflowRejected(t *testing.T) {
	big := int64(1) << 62
	if _, code := Trapezoid(big, big, big); code != domain.ErrFixedPointOverflow {
		t.Fatalf("overflow must be rejected, got %s", code)
	}
}

func TestConvertCapacity(t *testing.T) {
	// nominal 1000 dm³, factor 750 per-thousand -> 750
	got, code := ConvertCapacity(1000, 750)
	if code != domain.ErrNone {
		t.Fatalf("unexpected code %s", code)
	}
	if got != 750 {
		t.Fatalf("got %d, want 750", got)
	}
}

func TestConvertCapacityZeroDivisor(t *testing.T) {
	// factor 0 -> 0, no division error
	if got, code := ConvertCapacity(1000, 0); code != domain.ErrNone || got != 0 {
		t.Fatalf("factor 0 should yield 0, got %d/%s", got, code)
	}
}
