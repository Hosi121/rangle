import { Viewer, SceneRevealMode, RenderMode } from '@mkkellogg/gaussian-splats-3d';
import { SceneSession, parseManifest } from './session.js';
import { FrameLoader } from './frame-loader.js';
import { attachCameraIntrinsics } from './camera-intrinsics.js';
import { attachReferenceSampling } from './reference-sampling.js';
import { strictJson } from './strict-json.js';
import { SnapshotClock } from './snapshot-clock.js';

const status = document.querySelector('#status'), play = document.querySelector('#play');
const seek = document.querySelector('#seek'), time = document.querySelector('#time'), rate = document.querySelector('#rate');
let session, clock, drawn = 0, started;
async function start() {
  try {
    const params = new URLSearchParams(location.search);
    if (params.getAll('scene').length !== 1) throw Error('scene URLを指定してください');
    const source = new URL(params.get('scene'), location.href);
    if (source.username || source.password || source.hash || source.search ||
        !(source.origin === location.origin || source.origin === 'https://media.rangleplay.stream') ||
        !source.pathname.endsWith('/scene.json')) throw Error('公開されたscene URLを指定してください');
    const response = await fetch(source, { credentials: 'omit', redirect: 'error', signal: AbortSignal.timeout(15000) });
    if (!response.ok) throw Error('モデルを取得できませんでした');
    const raw = strictJson(await response.text()), manifest = parseManifest(raw, source);
    if (!manifest.frames || manifest.frames.length < 2 || manifest.frames.length > 15000 || manifest.frameRate > 240) throw Error('フレーム列が必要です');
    if (manifest.frames.some(value => new URL(value).origin !== source.origin)) throw Error('モデルの配信元が一致しません');
    seek.max = String(manifest.frames.length - 1);
    const attribution = document.querySelector('#attribution'), link = document.createElement('a');
    link.href = manifest.attribution.source; link.rel = 'noopener noreferrer'; link.target = '_blank';
    link.textContent = `${manifest.attribution.label} · ${manifest.attribution.license}`; attribution.append(link);
    session = new SceneSession({
      frameLoader: new FrameLoader(), fetchManifest: async () => new Response(JSON.stringify(raw)),
      createViewer: camera => {
        const viewer = new Viewer({ rootElement: document.querySelector('#viewport'), cameraUp: camera.up,
          initialCameraPosition: camera.position, initialCameraLookAt: camera.lookAt,
          sharedMemoryForWorkers: false, gpuAcceleratedSort: false, sphericalHarmonicsDegree: 0,
          sceneRevealMode: SceneRevealMode.Instant, renderMode: RenderMode.OnChange });
        viewer.camera.fov = camera.fovYDegrees;
        const intrinsics = attachCameraIntrinsics(viewer.camera, camera.intrinsics);
        const sampling = attachReferenceSampling(viewer, camera.referenceImageHeight), dispose = viewer.dispose.bind(viewer);
        viewer.camera.updateProjectionMatrix();
        viewer.dispose = () => { intrinsics(); sampling(); return dispose(); };
        return viewer;
      },
      notify: event => {
        if (event.type === 'error') { clock?.pause(); status.textContent = '読み込み・描画に失敗しました'; return; }
        if (event.type !== 'ready') return;
        if (clock?.clock.playing) drawn++;
        seek.value = String(event.frame);
        time.textContent = `${(event.frame / manifest.frameRate).toFixed(2)} / ${(manifest.frames.length / manifest.frameRate).toFixed(2)}秒`;
        const seconds = (performance.now() - started) / 1000;
        if (clock?.clock.playing && seconds > 1) rate.textContent = `${(drawn / seconds).toFixed(1)} 更新/秒`;
        status.textContent = 'ドラッグで視点を回す';
      },
    });
    await session.load(source);
    if (session.disposed) throw Error('モデルを開始できませんでした');
    clock = new SnapshotClock({ frames: manifest.frames.length, fps: manifest.frameRate, show: frame => session.seek(frame) });
    play.disabled = seek.disabled = false;
  } catch (error) { status.textContent = error.message; }
}
play.addEventListener('click', () => {
  if (!clock) return;
  if (clock.clock.playing) clock.pause();
  else { drawn = 0; started = performance.now(); clock.play(); }
  play.textContent = clock.clock.playing ? '停止' : '再生';
});
seek.addEventListener('input', () => { play.textContent = '再生'; void clock?.seek(Number(seek.value)); });
document.addEventListener('visibilitychange', () => { if (document.hidden) { clock?.pause(); play.textContent = '再生'; } });
window.addEventListener('pagehide', () => { clock?.dispose(); void session?.dispose(); });
void start();
