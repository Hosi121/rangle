import { exactFields, strictJson } from './strict-json.js';

const id = value => typeof value === 'string' && /^[A-Za-z0-9_-]{1,64}$/.test(value);
const positive = value => Number.isSafeInteger(value) && value > 0;
export function parseTransport(input, source, identity = {}) {
  const m = typeof input === 'string' ? strictJson(input) : input;
  if (!exactFields(m, ['version', 'clip_id', 'revision', 'codec', 'representations']) || m.version !== 1 ||
      !id(m.clip_id) || !id(m.revision) || !['rangle-temporal-rgs1-segments-v1', 'rangle-temporal-rgs1'].includes(m.codec) ||
      (identity.clipId !== undefined && identity.clipId !== m.clip_id) ||
      (identity.revision !== undefined && identity.revision !== m.revision) ||
      (identity.codec !== undefined && identity.codec !== m.codec) ||
      !Array.isArray(m.representations) || !m.representations.length) throw Error('Invalid transport manifest');
  const base = new URL(source);
  if (!['http:', 'https:'].includes(base.protocol) || base.username || base.password || base.search || base.hash) throw Error('Invalid transport URL');
  if (base.pathname !== `/manifests/${m.clip_id}/${m.revision}.json`) throw Error('Transport identity mismatch');
  const ids = new Set();
  let baseline;
  const representations = m.representations.map(r => {
    if (!exactFields(r, ['id', 'bitrate_bps', 'peak_decoded_bytes', 'decode_time_ms', 'segments'], ['init_url', 'init_size_bytes']) ||
        !id(r.id) || ids.has(r.id) || !positive(r.bitrate_bps) || !positive(r.peak_decoded_bytes) ||
        !Number.isFinite(r.decode_time_ms) || r.decode_time_ms < 0 || !Array.isArray(r.segments) || !r.segments.length) throw Error('Invalid representation');
    ids.add(r.id);
    const prefix = `/assets/${m.clip_id}/${m.revision}/${r.id}/`;
    const asset = value => {
      if (typeof value !== 'string' || !value.startsWith(prefix) || !/^[A-Za-z0-9_.-]+$/.test(value.slice(prefix.length)) || ['.', '..'].includes(value.slice(prefix.length))) throw Error('Invalid transport asset');
      return new URL(value, base).href;
    };
    if (r.init_url !== undefined && typeof r.init_url !== 'string') throw Error('Invalid initialization URL');
    const initSize = r.init_size_bytes ?? 0;
    if (r.init_size_bytes === null) throw Error('Invalid initialization size');
    if ((r.init_url ? !positive(initSize) : initSize !== 0)) throw Error('Invalid initialization size');
    const init = r.init_url ? asset(r.init_url) : null;
    const urls = new Set(init ? [init] : []);
    let end = 0;
    const segments = r.segments.map((s, i) => {
      if (!exactFields(s, ['index', 'start_seconds', 'duration_seconds', 'url', 'size_bytes', 'independent', 'sha256']) ||
          s.index !== i || !Number.isFinite(s.start_seconds) || s.start_seconds < 0 || Math.abs(s.start_seconds - end) > 1e-6 ||
          !Number.isFinite(s.duration_seconds) || s.duration_seconds <= 0 || s.independent !== true || !positive(s.size_bytes) ||
          !Number.isSafeInteger(s.size_bytes + initSize) || typeof s.sha256 !== 'string' || !/^[a-fA-F0-9]{64}$/.test(s.sha256)) throw Error('Invalid transport segment');
      const url = asset(s.url);
      if (urls.has(url)) throw Error('Repeated transport asset');
      urls.add(url); end = s.start_seconds + s.duration_seconds;
      if (!Number.isFinite(end)) throw Error('Invalid transport duration');
      return { ...s, url, sha256: s.sha256.toLowerCase() };
    });
    if (baseline && (segments.length !== baseline.length || segments.some((s, i) => Math.abs(s.start_seconds - baseline[i].start_seconds) > 1e-6 || Math.abs(s.duration_seconds - baseline[i].duration_seconds) > 1e-6))) throw Error('Unaligned transport ladder');
    baseline ??= segments;
    return { ...r, init_url: init, init_size_bytes: initSize, segments };
  });
  return { ...m, representations };
}
