/** Opacity is quantized like the existing snapshots, including ties-to-even. */
export function opacityByte(peak, time, centre, duration) {
  const delta = Math.fround(time - centre);
  const ratio = Math.fround(delta / duration);
  const exponent = Math.fround(-.5 * Math.fround(ratio * ratio));
  const value = Math.fround(Math.fround(peak * Math.fround(Math.exp(exponent))) * 255);
  const floor = Math.floor(value), fraction = value - floor;
  return fraction === .5 ? floor + (floor & 1) : Math.round(value);
}

/** Stable float32 radix sort, back to front, using the actual moving centres. */
export class TemporalSorter {
  constructor(motion) {
    this.motion = motion;
    this.count = motion.length / 9;
    this.keys = new Uint32Array(this.count);
    this.depths = new Float32Array(this.keys.buffer);
    this.ids = new Uint32Array(this.count);
    this.scratch = new Uint32Array(this.count);
    this.buckets = new Uint32Array(256);
  }

  sort(time, view, output = new Uint32Array(this.count)) {
    let active = 0;
    time = Math.fround(time);
    for (let i = 0; i < this.count; i++) {
      const offset = i * 9, p = this.motion;
      if (opacityByte(p[offset + 8], time, p[offset + 6], p[offset + 7]) < 1) continue;
      const delta = Math.fround(time - p[offset + 6]);
      const x = Math.fround(p[offset] + Math.fround(p[offset + 3] * delta));
      const y = Math.fround(p[offset + 1] + Math.fround(p[offset + 4] * delta));
      const z = Math.fround(p[offset + 2] + Math.fround(p[offset + 5] * delta));
      const depth = view[2] * x + view[6] * y + view[10] * z + view[14];
      if (!Number.isFinite(depth)) continue;
      this.depths[i] = depth;
      const bits = this.keys[i];
      this.keys[i] = bits & 0x80000000 ? ~bits : bits ^ 0x80000000;
      this.ids[active++] = i;
    }
    let from = this.ids, to = this.scratch;
    for (let shift = 0; shift < 32; shift += 8) {
      const buckets = this.buckets;
      buckets.fill(0);
      for (let i = 0; i < active; i++) buckets[(this.keys[from[i]] >>> shift) & 255]++;
      let sum = 0;
      for (let i = 0; i < 256; i++) { const size = buckets[i]; buckets[i] = sum; sum += size; }
      for (let i = 0; i < active; i++) {
        const id = from[i];
        to[buckets[(this.keys[id] >>> shift) & 255]++] = id;
      }
      [from, to] = [to, from];
    }
    output.set(from.subarray(0, active));
    return { indexes: output, active };
  }
}
