// Package abr provides an original, bounded throughput-and-buffer ABR baseline.
// It is not a codec or an implementation of BOLA. It assumes every manifest
// segment is independently decodable, and ranks qualities by advertised bitrate.
//
// Construct a Controller with a validated stream manifest and an explicit Config.
// Feed only completed network transfers to Observe, then call Select with the
// next segment index, current representation, playable buffer, and init-cache
// state. Observe rejects cache hits and failed/aborted requests without changing
// the estimate. Select does not fetch, decode, or mutate cache/player state.
//
// Hard decoded-memory and per-segment decode limits are never relaxed. The
// delivery budget is softer: if the estimate cannot sustain even the lowest
// hard-feasible quality, Select returns that quality and explicitly flags the
// expected budget miss. This lets the caller choose to stall, pause, or stop.
// A positive buffer's horizon is max(0, buffer-reserve), capped at maximum. Only
// an exactly empty buffer uses the configured acquisition allowance, marked by
// AcquisitionOnly in the decision; it cannot promise stall-free playback.
// The default one-second startup buffer permits a short VOD to leave startup;
// this is an initial policy, not an experimentally optimized threshold.
// Memory is the manifest's peak
// working-set estimate for one segment, not a sum over a retained decoded cache.
// Decode predictions are manifest estimates and do not measure GPU rendering.
package abr
