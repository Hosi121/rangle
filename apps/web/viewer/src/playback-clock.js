/** A looping source clock, independent of sorting latency and page suspension. */
export class PlaybackClock {
  constructor(frameCount, frameRate, now = () => performance.now()) {
    this.duration = (frameCount - 1) / frameRate;
    this.period = frameCount / frameRate;
    this.now = now;
    this.position = 0;
    this.started = now();
    this.playing = false;
    this.suspended = false;
  }
  value() {
    if (!this.playing || this.suspended) return this.position;
    return Math.min((this.position + (this.now() - this.started) / 1000) % this.period, this.duration);
  }
  seek(position) { this.position = Math.max(0, Math.min(position, this.duration)); this.started = this.now(); }
  toggle() { this.seek(this.value()); this.playing = !this.playing; }
  suspend(value) {
    if (value === this.suspended) return;
    this.seek(this.value());
    this.suspended = value;
  }
}
