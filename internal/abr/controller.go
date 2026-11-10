package abr

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/Hosi121/rangle/internal/stream"
)

var (
	ErrInvalidRequest           = errors.New("invalid selection request")
	ErrNoFeasibleRepresentation = errors.New("no representation satisfies hard resource limits")
	ErrTransferSizeOverflow     = errors.New("segment and initialization size exceed int64 bytes")
)

// Config controls a limited throughput-and-buffer baseline. All rates are
// bits/second, durations ending in Seconds are seconds, decode durations are
// milliseconds, and memory sizes are bytes. Hard limits are inclusive.
type Config struct {
	Estimator EstimatorConfig

	SafetyFactor         float64 // (0, 1]: usable fraction of the conservative estimate.
	StartupBandwidthBPS  float64 // Explicit prediction fallback until a sample exists.
	StartupBufferSeconds float64 // Below this buffer, select the lowest feasible quality.

	BufferReserveSeconds      float64
	EmptyBufferHorizonSeconds float64 // Acquisition allowance only when buffer is exactly zero.
	MaxDownloadHorizonSeconds float64

	UpshiftHeadroom      float64 // > 1: extra bandwidth and deadline margin for upshifts.
	UpshiftConfirmations int     // Consecutive segment decisions needed for one quality step.
	MaxDecodedBytes      int64
	MaxDecodeTimeMS      float64 // Per-segment decode budget; zero allows zero-cost decodes.
}

// DefaultConfig supplies initial policy values, not measured device/network
// optima. Callers should set resource limits for their actual client.
func DefaultConfig() Config {
	return Config{
		Estimator: DefaultEstimatorConfig(), SafetyFactor: 0.8,
		StartupBandwidthBPS: 500_000, StartupBufferSeconds: 1,
		BufferReserveSeconds: 0.5, EmptyBufferHorizonSeconds: 1, MaxDownloadHorizonSeconds: 8,
		UpshiftHeadroom: 1.2, UpshiftConfirmations: 2,
		MaxDecodedBytes: 256 << 20, MaxDecodeTimeMS: 500,
	}
}

// Request describes the next independent segment to retrieve. CurrentID is the
// quality actually in use, or empty at startup. InitCached is keyed by
// representation ID within this controller's clip/revision, and must only mark
// initialization assets available locally. Do not reuse it for another revision.
// BufferSeconds is playable media already buffered before fetching this segment.
type Request struct {
	SegmentIndex  int
	CurrentID     string
	BufferSeconds float64
	InitCached    map[string]bool
}

type Reason string

const (
	ReasonStartup           Reason = "startup"
	ReasonHold              Reason = "hold"
	ReasonUpshift           Reason = "upshift"
	ReasonDownshift         Reason = "downshift"
	ReasonResourceLimit     Reason = "resource-limit"
	ReasonBandwidthFallback Reason = "bandwidth-fallback"
)

// Decision identifies an entry in the original manifest order. RequiredBytes
// includes an uncached init asset. Predictions use SafetyFactor * EstimateBPS;
// ReadySeconds includes serial download and decode time. Predictions may be +Inf
// when a finite, extremely small estimate cannot represent the required time.
// AcquisitionOnly marks an empty-buffer acquisition allowance rather than a
// playable-buffer deadline. WithinDeliveryBudget reports the nominal bitrate and
// readiness checks against this horizon; false means a selected quality still
// obeys hard limits but may stall playback.
type Decision struct {
	RepresentationID     string
	RepresentationIndex  int
	SegmentIndex         int
	RequiredBytes        int64
	EstimateBPS          float64
	DownloadSeconds      float64
	ReadySeconds         float64
	HorizonSeconds       float64
	AcquisitionOnly      bool
	WithinDeliveryBudget bool
	Reason               Reason
}

type rendition struct {
	representation stream.Representation
	originalIndex  int
}

// Controller chooses the highest sustainable advertised bitrate, subject to
// actual segment size, buffer deadline, and hard memory/decode limits. Quality
// order is bitrate then ID, independent of manifest order. Each segment is
// independently decodable; only a representation's init asset is carried across
// requests. Controller is stateful and is not safe for concurrent use.
type Controller struct {
	config    Config
	estimator *Estimator
	ladder    []rendition
	byID      map[string]int

	pendingID, pendingCurrent  string
	pendingCount, pendingIndex int
}

// NewController validates and snapshots the manifest. Later caller mutations do
// not change policy inputs. It rejects even a possible uncached-init size sum
// that cannot be represented in int64, rather than allowing wrapped byte counts.
func NewController(manifest stream.Manifest, config Config) (*Controller, error) {
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("ABR manifest: %w", err)
	}
	estimator, err := NewEstimator(config.Estimator)
	if err != nil {
		return nil, err
	}
	if !finite(config.SafetyFactor) || config.SafetyFactor <= 0 || config.SafetyFactor > 1 ||
		!finite(config.StartupBandwidthBPS) || config.StartupBandwidthBPS <= 0 ||
		!finite(config.StartupBufferSeconds) || config.StartupBufferSeconds < 0 ||
		!finite(config.BufferReserveSeconds) || config.BufferReserveSeconds < 0 ||
		!finite(config.EmptyBufferHorizonSeconds) || config.EmptyBufferHorizonSeconds <= 0 ||
		!finite(config.MaxDownloadHorizonSeconds) || config.MaxDownloadHorizonSeconds < config.EmptyBufferHorizonSeconds ||
		!finite(config.UpshiftHeadroom) || config.UpshiftHeadroom <= 1 || config.UpshiftConfirmations < 1 ||
		config.MaxDecodedBytes <= 0 || !finite(config.MaxDecodeTimeMS) || config.MaxDecodeTimeMS < 0 {
		return nil, fmt.Errorf("invalid ABR configuration")
	}
	c := &Controller{config: config, estimator: estimator, byID: make(map[string]int)}
	for i, r := range manifest.Representations {
		for _, s := range r.Segments {
			if s.SizeBytes > math.MaxInt64-r.InitSizeBytes {
				return nil, fmt.Errorf("%w for %q segment %d", ErrTransferSizeOverflow, r.ID, s.Index)
			}
		}
		r.Segments = append([]stream.Segment(nil), r.Segments...)
		c.ladder = append(c.ladder, rendition{representation: r, originalIndex: i})
	}
	sort.Slice(c.ladder, func(i, j int) bool {
		a, b := c.ladder[i].representation, c.ladder[j].representation
		if a.BitrateBPS != b.BitrateBPS {
			return a.BitrateBPS < b.BitrateBPS
		}
		return a.ID < b.ID
	})
	for i, r := range c.ladder {
		c.byID[r.representation.ID] = i
	}
	return c, nil
}

func (c *Controller) Observe(sample Sample) error { return c.estimator.Observe(sample) }

func (c *Controller) Estimate() (BandwidthEstimate, bool) { return c.estimator.Estimate() }

// Select recommends a quality; the caller reports the quality actually used in
// the next Request. Startup (no current quality, no estimate, or a low buffer)
// always selects the lowest quality meeting hard limits. A sustained upshift
// needs extra headroom and consecutive, distinct segment indices; duplicate
// calls cannot accumulate confirmations. Skipping/seeking resets confirmation.
// Downshifts and replacement of a hard-ineligible current quality are immediate.
// If no quality meets the delivery budget, the lowest hard-feasible quality is
// returned with WithinDeliveryBudget=false. If none meets hard limits, it errors.
func (c *Controller) Select(request Request) (Decision, error) {
	if request.SegmentIndex < 0 || request.SegmentIndex >= len(c.ladder[0].representation.Segments) || !finite(request.BufferSeconds) || request.BufferSeconds < 0 {
		return Decision{}, ErrInvalidRequest
	}
	current, found := c.byID[request.CurrentID]
	if request.CurrentID != "" && !found {
		return Decision{}, fmt.Errorf("%w: unknown current representation %q", ErrInvalidRequest, request.CurrentID)
	}
	estimate, measured := c.estimator.Estimate()
	bps := c.config.StartupBandwidthBPS
	if measured {
		bps = estimate.ConservativeBPS
	}
	horizon := math.Min(c.config.MaxDownloadHorizonSeconds, math.Max(0, request.BufferSeconds-c.config.BufferReserveSeconds))
	if request.BufferSeconds == 0 {
		horizon = c.config.EmptyBufferHorizonSeconds
	}
	safeBPS := bps * c.config.SafetyFactor

	eligible := make([]int, 0, len(c.ladder))
	decisions := make(map[int]Decision, len(c.ladder))
	for i, item := range c.ladder {
		r := item.representation
		if r.PeakDecodedBytes > c.config.MaxDecodedBytes || r.DecodeTimeMS > c.config.MaxDecodeTimeMS {
			continue
		}
		bytes := r.Segments[request.SegmentIndex].SizeBytes
		if !request.InitCached[r.ID] {
			bytes += r.InitSizeBytes // Checked for overflow by NewController.
		}
		download := float64(bytes) * 8 / safeBPS
		ready := download + r.DecodeTimeMS/1000
		decisions[i] = Decision{
			RepresentationID: r.ID, RepresentationIndex: item.originalIndex,
			SegmentIndex: request.SegmentIndex, RequiredBytes: bytes, EstimateBPS: bps,
			DownloadSeconds: download, ReadySeconds: ready, HorizonSeconds: horizon,
			AcquisitionOnly:      request.BufferSeconds == 0,
			WithinDeliveryBudget: float64(r.BitrateBPS) <= safeBPS && ready <= horizon,
		}
		eligible = append(eligible, i)
	}
	if len(eligible) == 0 {
		return Decision{}, ErrNoFeasibleRepresentation
	}
	finish := func(index int, reason Reason) (Decision, error) {
		d := decisions[index]
		d.Reason = reason
		return d, nil
	}
	if request.CurrentID == "" || !measured || request.BufferSeconds < c.config.StartupBufferSeconds {
		c.clearPending()
		return finish(eligible[0], ReasonStartup)
	}
	best := -1
	for _, i := range eligible {
		if decisions[i].WithinDeliveryBudget {
			best = i
		}
	}
	if best == -1 {
		c.clearPending()
		return finish(eligible[0], ReasonBandwidthFallback)
	}
	currentDecision, currentHardEligible := decisions[current]
	if !currentHardEligible {
		c.clearPending()
		return finish(best, ReasonResourceLimit)
	}
	if best < current {
		c.clearPending()
		return finish(best, ReasonDownshift)
	}
	if best == current {
		c.clearPending()
		return finish(current, ReasonHold)
	}
	// If VBR makes the current segment too large but a higher quality's next
	// segment fits, retain upshift hysteresis. While waiting, promptly use the
	// best lower sustainable quality, or the lowest hard-feasible fallback.
	hold, holdReason := current, ReasonHold
	if !currentDecision.WithinDeliveryBudget {
		hold, holdReason = eligible[0], ReasonBandwidthFallback
		for _, i := range eligible {
			if i <= current && decisions[i].WithinDeliveryBudget {
				hold, holdReason = i, ReasonDownshift
			}
		}
	}
	// Advance to the next quality meeting the stricter upshift budget, skipping
	// an intermediate quality if its actual VBR segment is too large.
	target := -1
	for _, i := range eligible {
		if i <= current {
			continue
		}
		d := decisions[i]
		r := c.ladder[i].representation
		if float64(r.BitrateBPS) <= safeBPS/c.config.UpshiftHeadroom &&
			d.DownloadSeconds*c.config.UpshiftHeadroom+r.DecodeTimeMS/1000 <= horizon {
			target = i
			break
		}
	}
	if target == -1 {
		c.clearPending()
		return finish(hold, holdReason)
	}
	id := c.ladder[target].representation.ID
	if c.pendingID != id || c.pendingCurrent != request.CurrentID || request.SegmentIndex < c.pendingIndex || request.SegmentIndex > c.pendingIndex+1 {
		c.pendingID, c.pendingCurrent = id, request.CurrentID
		c.pendingCount = 1
	} else if request.SegmentIndex == c.pendingIndex+1 && c.pendingCount < c.config.UpshiftConfirmations {
		c.pendingCount++
	}
	c.pendingIndex = request.SegmentIndex
	if c.pendingCount < c.config.UpshiftConfirmations {
		return finish(hold, holdReason)
	}
	return finish(target, ReasonUpshift)
}

func (c *Controller) clearPending() {
	c.pendingID, c.pendingCurrent = "", ""
	c.pendingCount, c.pendingIndex = 0, 0
}
