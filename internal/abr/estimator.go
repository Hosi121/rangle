package abr

import (
	"errors"
	"fmt"
	"math"
)

var (
	ErrInvalidSample  = errors.New("invalid bandwidth sample")
	ErrExcludedSample = errors.New("sample does not represent a completed network transfer")
)

// EstimatorConfig sets EWMA half-lives in seconds of completed transfer time.
// A longer transfer contributes more weight; time spent idle contributes none.
type EstimatorConfig struct {
	FastHalfLifeSeconds float64
	SlowHalfLifeSeconds float64
}

func DefaultEstimatorConfig() EstimatorConfig {
	return EstimatorConfig{FastHalfLifeSeconds: 2, SlowHalfLifeSeconds: 5}
}

// Sample describes one completed download. Bytes counts response payload bytes,
// and DurationSeconds measures its network transfer, including request latency.
// Cached, failed, and aborted transfers must be marked and are excluded.
type Sample struct {
	Bytes           int64
	DurationSeconds float64
	CacheHit        bool
	Failed          bool
	Aborted         bool
}

// BandwidthEstimate reports bits/second. ConservativeBPS is min(FastBPS, SlowBPS).
type BandwidthEstimate struct {
	FastBPS         float64
	SlowBPS         float64
	ConservativeBPS float64
}

// Estimator is a dual, transfer-time-weighted EWMA. It is not safe for concurrent
// use. Construct it with NewEstimator; rejected samples leave its state intact.
type Estimator struct {
	config      EstimatorConfig
	fast, slow  float64
	initialized bool
}

func NewEstimator(config EstimatorConfig) (*Estimator, error) {
	if !finite(config.FastHalfLifeSeconds) || !finite(config.SlowHalfLifeSeconds) || config.FastHalfLifeSeconds <= 0 || config.SlowHalfLifeSeconds <= config.FastHalfLifeSeconds {
		return nil, fmt.Errorf("EWMA half-lives must be finite and 0 < fast < slow")
	}
	return &Estimator{config: config}, nil
}

func (e *Estimator) Observe(sample Sample) error {
	if sample.CacheHit || sample.Failed || sample.Aborted {
		return ErrExcludedSample
	}
	if sample.Bytes <= 0 || !finite(sample.DurationSeconds) || sample.DurationSeconds <= 0 {
		return ErrInvalidSample
	}
	// Convert before multiplication so an int64 byte count cannot overflow.
	bps := float64(sample.Bytes) * 8 / sample.DurationSeconds
	if !finite(bps) || bps <= 0 {
		return ErrInvalidSample
	}
	if !e.initialized {
		e.fast, e.slow, e.initialized = bps, bps, true
		return nil
	}
	e.fast = updateEWMA(e.fast, bps, sample.DurationSeconds, e.config.FastHalfLifeSeconds)
	e.slow = updateEWMA(e.slow, bps, sample.DurationSeconds, e.config.SlowHalfLifeSeconds)
	return nil
}

// Estimate returns false until the first accepted sample; there is no implicit
// initial bandwidth. A Controller applies its explicit startup fallback.
func (e *Estimator) Estimate() (BandwidthEstimate, bool) {
	if !e.initialized {
		return BandwidthEstimate{}, false
	}
	return BandwidthEstimate{FastBPS: e.fast, SlowBPS: e.slow, ConservativeBPS: math.Min(e.fast, e.slow)}, true
}

func updateEWMA(previous, sample, seconds, halfLife float64) float64 {
	weight := -math.Expm1(-math.Ln2 * (seconds / halfLife))
	// This form avoids cancellation when a long transfer nearly replaces a much
	// larger previous estimate. Its terms are nonnegative and bounded by inputs.
	return (1-weight)*previous + weight*sample
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
