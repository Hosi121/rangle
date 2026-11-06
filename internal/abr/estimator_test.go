package abr

import (
	"errors"
	"math"
	"testing"
)

func TestEstimatorCollapseAndRecovery(t *testing.T) {
	e, err := NewEstimator(EstimatorConfig{FastHalfLifeSeconds: 1, SlowHalfLifeSeconds: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Estimate(); ok {
		t.Fatal("estimate available before any completed network transfer")
	}
	trace := []struct {
		name                string
		bytes               int64
		seconds, fast, slow float64
	}{
		{"initial", 500_000, 1, 4_000_000, 4_000_000},
		{"collapse", 125_000, 1, 2_500_000, 3_522_689.245761143},
		{"recovery", 1_000_000, 1, 5_250_000, 4_235_045.436783641},
		{"long-recovery-transfer", 4_000_000, 4, 7_828_125, 6_117_522.718391821},
	}
	for _, step := range trace {
		t.Run(step.name, func(t *testing.T) {
			if err := e.Observe(Sample{Bytes: step.bytes, DurationSeconds: step.seconds}); err != nil {
				t.Fatal(err)
			}
			estimate, ok := e.Estimate()
			if !ok || !closeRate(estimate.FastBPS, step.fast) || !closeRate(estimate.SlowBPS, step.slow) || estimate.ConservativeBPS != math.Min(estimate.FastBPS, estimate.SlowBPS) {
				t.Fatalf("estimate = %+v, available=%t; want fast=%v slow=%v", estimate, ok, step.fast, step.slow)
			}
		})
	}
}

func TestEstimatorTransferTimeWeight(t *testing.T) {
	whole, _ := NewEstimator(DefaultEstimatorConfig())
	split, _ := NewEstimator(DefaultEstimatorConfig())
	for _, e := range []*Estimator{whole, split} {
		if err := e.Observe(Sample{Bytes: 125_000, DurationSeconds: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := whole.Observe(Sample{Bytes: 4_000_000, DurationSeconds: 4}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := split.Observe(Sample{Bytes: 1_000_000, DurationSeconds: 1}); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := whole.Estimate()
	b, _ := split.Estimate()
	if !closeRate(a.FastBPS, b.FastBPS) || !closeRate(a.SlowBPS, b.SlowBPS) {
		t.Fatalf("a constant-rate transfer changes weight when split: %+v vs %+v", a, b)
	}
}

func TestEstimatorRejectsInvalidAndUnrepresentativeSamples(t *testing.T) {
	tests := []struct {
		name   string
		sample Sample
		want   error
	}{
		{"zero bytes", Sample{DurationSeconds: 1}, ErrInvalidSample},
		{"negative bytes", Sample{Bytes: -1, DurationSeconds: 1}, ErrInvalidSample},
		{"zero time", Sample{Bytes: 1}, ErrInvalidSample},
		{"negative time", Sample{Bytes: 1, DurationSeconds: -1}, ErrInvalidSample},
		{"NaN time", Sample{Bytes: 1, DurationSeconds: math.NaN()}, ErrInvalidSample},
		{"infinite time", Sample{Bytes: 1, DurationSeconds: math.Inf(1)}, ErrInvalidSample},
		{"rate overflow", Sample{Bytes: math.MaxInt64, DurationSeconds: math.SmallestNonzeroFloat64}, ErrInvalidSample},
		{"cache hit", Sample{Bytes: 1_000_000, DurationSeconds: 0.000001, CacheHit: true}, ErrExcludedSample},
		{"failed", Sample{Bytes: 1_000_000, DurationSeconds: 1, Failed: true}, ErrExcludedSample},
		{"aborted", Sample{Bytes: 1_000_000, DurationSeconds: 1, Aborted: true}, ErrExcludedSample},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e, _ := NewEstimator(DefaultEstimatorConfig())
			if err := e.Observe(test.sample); !errors.Is(err, test.want) {
				t.Fatalf("Observe = %v, want %v", err, test.want)
			}
			if _, ok := e.Estimate(); ok {
				t.Fatal("rejected sample initialized estimator")
			}
			if err := e.Observe(Sample{Bytes: 500_000, DurationSeconds: 1}); err != nil {
				t.Fatal(err)
			}
			before, _ := e.Estimate()
			if err := e.Observe(test.sample); !errors.Is(err, test.want) {
				t.Fatalf("Observe = %v, want %v", err, test.want)
			}
			after, _ := e.Estimate()
			if before != after {
				t.Fatalf("rejected sample changed estimate: %+v -> %+v", before, after)
			}
		})
	}
}

func TestEstimatorConfigAndLargeByteCount(t *testing.T) {
	for _, config := range []EstimatorConfig{
		{}, {FastHalfLifeSeconds: -1, SlowHalfLifeSeconds: 4},
		{FastHalfLifeSeconds: 4, SlowHalfLifeSeconds: 4},
		{FastHalfLifeSeconds: math.NaN(), SlowHalfLifeSeconds: 4},
		{FastHalfLifeSeconds: 1, SlowHalfLifeSeconds: math.Inf(1)},
	} {
		if _, err := NewEstimator(config); err == nil {
			t.Fatalf("accepted invalid config %+v", config)
		}
	}
	e, _ := NewEstimator(DefaultEstimatorConfig())
	if err := e.Observe(Sample{Bytes: math.MaxInt64, DurationSeconds: 1}); err != nil {
		t.Fatal(err)
	}
	estimate, _ := e.Estimate()
	if estimate.ConservativeBPS != float64(math.MaxInt64)*8 {
		t.Fatalf("large byte-to-bit conversion overflowed: %+v", estimate)
	}
}

func closeRate(a, b float64) bool { return math.Abs(a-b) <= 1e-8*math.Max(1, math.Abs(b)) }
