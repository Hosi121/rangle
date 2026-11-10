import { PlaybackClock } from './playback-clock.js';

/** Bounded baseline: one frame install at a time; late rendering skips source
 * samples rather than stretching a ten-second clip into a longer sequence. */
export class SnapshotClock {
  constructor({ frames, fps, show, now, schedule = callback => setTimeout(callback, 8), cancel = clearTimeout }) {
    if (!Number.isInteger(frames) || frames < 2 || frames > 15000 || !Number.isFinite(fps) || fps <= 0 || fps > 240) {
      throw Error('Invalid snapshot timeline');
    }
    this.clock = new PlaybackClock(frames, fps, now);
    this.frames = frames; this.fps = fps; this.show = show; this.schedule = schedule; this.cancel = cancel;
    this.epoch = 0; this.frame = 0; this.busy = false;
  }
  play() { if (this.clock.playing) return; this.clock.toggle(); this.tick(); }
  pause() { if (this.clock.playing) this.clock.toggle(); this.epoch++; this.cancel(this.timer); }
  async seek(frame) {
    if (!Number.isInteger(frame)) throw Error('Invalid snapshot frame');
    frame = Math.max(0, Math.min(this.frames - 1, frame));
    this.pause(); this.clock.seek(frame / this.fps);
    const epoch = this.epoch;
    await this.show(Math.floor(this.clock.value() * this.fps));
    if (epoch === this.epoch) this.frame = frame;
  }
  async tick() {
    if (!this.clock.playing) return;
    const epoch = this.epoch;
    try {
      if (!this.busy) {
        const target = Math.floor(this.clock.value() * this.fps);
        if (target !== this.frame) {
          this.busy = true;
          try { await this.show(target); if (epoch === this.epoch) this.frame = target; }
          finally { this.busy = false; }
        }
      }
    } catch { if (epoch === this.epoch) this.pause(); }
    if (epoch === this.epoch && this.clock.playing) this.timer = this.schedule(() => this.tick());
  }
  dispose() { this.pause(); }
}
