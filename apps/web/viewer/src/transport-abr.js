// Policy values and selection rules mirror internal/abr. These are provisional
// controller defaults, not measured network/device capacity.
export const defaultABR = Object.freeze({ fast: 2, slow: 5, safety: .8, startupBps: 500000,
  startupBuffer: 1, reserve: .5, emptyHorizon: 1, maxHorizon: 8, headroom: 1.2,
  confirmations: 2, maxDecodedBytes: 256 << 20, maxDecodeMs: 500 });
export class TransportABR {
  constructor(manifest, config = {}) {
    this.config = { ...defaultABR, ...config };
    const c = this.config;
    if (![c.fast,c.slow,c.safety,c.startupBps,c.startupBuffer,c.reserve,c.emptyHorizon,c.maxHorizon,c.headroom,c.maxDecodeMs].every(Number.isFinite) ||
        c.fast <= 0 || c.slow <= c.fast || c.safety <= 0 || c.safety > 1 || c.startupBps <= 0 ||
        c.startupBuffer < 0 || c.reserve < 0 || c.emptyHorizon <= 0 || c.maxHorizon < c.emptyHorizon ||
        c.headroom <= 1 || !Number.isSafeInteger(c.confirmations) || c.confirmations < 1 ||
        !Number.isSafeInteger(c.maxDecodedBytes) || c.maxDecodedBytes <= 0 || c.maxDecodeMs < 0) throw Error('Invalid ABR configuration');
    this.ladder = manifest.representations.map((r, index) => ({ ...r, segments: r.segments.map(s => ({ ...s })), originalIndex: index }))
      .sort((a, b) => a.bitrate_bps - b.bitrate_bps || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
    this.pending = null;
  }
  observe({ bytes, seconds, cacheHit = false, failed = false, aborted = false }) {
    if (cacheHit || failed || aborted) return false;
    if (!Number.isSafeInteger(bytes) || bytes <= 0 || !Number.isFinite(seconds) || seconds <= 0) throw Error('Invalid bandwidth sample');
    const rate = bytes * 8 / seconds;
    if (!Number.isFinite(rate) || rate <= 0) throw Error('Invalid bandwidth sample');
    const update = (old, halfLife) => {
      const weight = -Math.expm1(-Math.LN2 * seconds / halfLife);
      return (1 - weight) * old + weight * rate;
    };
    this.fast = this.fast === undefined ? rate : update(this.fast, this.config.fast);
    this.slow = this.slow === undefined ? rate : update(this.slow, this.config.slow);
    return true;
  }
  select({ segmentIndex, currentId = '', bufferSeconds = 0, initCached = new Set() }) {
    const c = this.config, current = this.ladder.findIndex(r => r.id === currentId);
    if (!Number.isInteger(segmentIndex) || segmentIndex < 0 || segmentIndex >= this.ladder[0].segments.length ||
        !Number.isFinite(bufferSeconds) || bufferSeconds < 0 || (currentId && current < 0)) throw Error('Invalid selection request');
    const measured = this.fast !== undefined, bps = measured ? Math.min(this.fast, this.slow) : c.startupBps;
    const horizon = bufferSeconds === 0 ? c.emptyHorizon : Math.min(c.maxHorizon, Math.max(0, bufferSeconds - c.reserve));
    const eligible = this.ladder.map((r, index) => {
      if (r.peak_decoded_bytes > c.maxDecodedBytes || r.decode_time_ms > c.maxDecodeMs) return null;
      const bytes = r.segments[segmentIndex].size_bytes + (initCached.has(r.id) ? 0 : r.init_size_bytes);
      const download = bytes * 8 / (bps * c.safety), ready = download + r.decode_time_ms / 1000;
      return { representation: r, index, requiredBytes: bytes, estimateBps: bps, downloadSeconds: download,
        readySeconds: ready, horizonSeconds: horizon, withinDeliveryBudget: r.bitrate_bps <= bps * c.safety && ready <= horizon };
    }).filter(Boolean);
    if (!eligible.length) throw Error('No feasible representation');
    const finish = (d, reason, clear = true) => { if (clear) this.pending = null; return { ...d, reason }; };
    if (!currentId || !measured || bufferSeconds < c.startupBuffer) return finish(eligible[0], 'startup');
    const feasible = eligible.filter(d => d.withinDeliveryBudget), best = feasible.at(-1);
    if (!best) return finish(eligible[0], 'bandwidth-fallback');
    const active = eligible.find(d => d.index === current);
    if (!active) return finish(best, 'resource-limit');
    if (best.index < current) return finish(best, 'downshift');
    if (best.index === current) return finish(active, 'hold');
    let hold = active, reason = 'hold';
    if (!active.withinDeliveryBudget) { hold = feasible.filter(d => d.index <= current).at(-1) ?? eligible[0]; reason = hold.withinDeliveryBudget ? 'downshift' : 'bandwidth-fallback'; }
    const target = eligible.find(d => d.index > current && d.representation.bitrate_bps <= bps * c.safety / c.headroom &&
      d.downloadSeconds * c.headroom + d.representation.decode_time_ms / 1000 <= horizon);
    if (!target) return finish(hold, reason);
    const p = this.pending;
    const count = !p || p.id !== target.representation.id || p.currentId !== currentId || segmentIndex < p.index || segmentIndex > p.index + 1 ? 1 :
      segmentIndex === p.index + 1 ? Math.min(c.confirmations, p.count + 1) : p.count;
    this.pending = { id: target.representation.id, currentId, index: segmentIndex, count };
    return count < c.confirmations ? finish(hold, reason, false) : finish(target, 'upshift', false);
  }
}
