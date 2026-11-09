/** Private snapshots use their captured source frame, never a fitted trajectory. */
export function privateSceneSource(search, origin) {
  const params = new URLSearchParams(search);
  const ids = params.getAll('scene');
  const id = ids[0] ?? 'basketball-clone-20261110';
  if (ids.length > 1 || !/^[a-z0-9][a-z0-9-]{0,63}$/.test(id)) {
    throw new Error('Invalid scene');
  }
  return new URL(`/api/v1/private-scenes/${id}/scene.json`, origin).href;
}

export function snapshotChoices(manifest, source) {
  const root = new URL('.', source);
  if (!manifest.frames || manifest.frames.length > 32) throw new Error('Invalid snapshots');
  return manifest.frames.map((value, index) => {
    const url = new URL(value, source);
    const match = /^frame-([0-9]{3})\.splat$/.exec(url.pathname.slice(root.pathname.length));
    if (url.origin !== root.origin || !url.pathname.startsWith(root.pathname) ||
        url.search || url.hash || !match) throw new Error('Invalid snapshot URL');
    return { index, sourceFrame: Number(match[1]), seconds: Number(match[1]) / manifest.frameRate };
  });
}

/** A small per-page CPU cache; authenticated models never enter a shared cache. */
export class SnapshotLoader {
  constructor(loader, maximum = 3) {
    this.loader = loader;
    this.maximum = maximum;
    this.cache = new Map();
    this.disposed = false;
  }

  async load(url) {
    if (this.disposed) throw new Error('Snapshot loader disposed');
    if (this.cache.has(url)) {
      this.loader.cancel();
      const value = this.cache.get(url);
      this.cache.delete(url);
      this.cache.set(url, value);
      return value;
    }
    const value = await this.loader.load(url);
    if (this.disposed) throw new DOMException('Snapshot loader disposed', 'AbortError');
    this.cache.set(url, value);
    if (this.cache.size > this.maximum) this.cache.delete(this.cache.keys().next().value);
    return value;
  }

  cancel() { this.loader.cancel(); }
  dispose() { this.disposed = true; this.cache.clear(); this.loader.dispose(); }
}
