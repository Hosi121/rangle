import { Viewer, SceneRevealMode, RenderMode } from '@mkkellogg/gaussian-splats-3d';
import { SceneSession, parseManifest } from './session.js';
import { FrameLoader } from './frame-loader.js';
import { attachCameraIntrinsics } from './camera-intrinsics.js';
import { attachReferenceSampling } from './reference-sampling.js';
import { strictJson } from './strict-json.js';
import { privateSceneSource, snapshotChoices, SnapshotLoader } from './private-preview-model.js';

const status = document.querySelector('#status');
const select = document.querySelector('#snapshot');
const login = document.querySelector('#login');
const viewport = document.querySelector('#viewport');
let session, cameraStart, choices = [], loading = false;

function message(text) { status.textContent = text; }

async function start() {
  if (loading || session) return;
  loading = true;
  message('アカウントを確認しています');
  try {
    const me = await fetch('/api/v1/auth/me', { cache: 'no-store' });
    if (!me.ok) throw new Error('アカウントを確認できませんでした。再読み込みしてください。');
    const account = await me.json();
    if (!account.user) {
      login.hidden = false;
      message('管理者アカウントでログインしてください');
      return;
    }
    if (!account.user.is_admin) throw new Error('管理者アカウントでログインしてください。');
    login.hidden = true;
    const source = privateSceneSource(location.search, location.origin);
    message('モデルを準備しています');
    const response = await fetch(source, { cache: 'no-store' });
    if (!response.ok) throw new Error(response.status === 404 ?
      'このモデルはまだ配置されていません。' : 'モデルを取得できませんでした。再ログインしてください。');
    const raw = strictJson(await response.text());
    const manifest = parseManifest(raw, source);
    choices = snapshotChoices(manifest, source);
    select.replaceChildren(...choices.map(({ index, sourceFrame, seconds }) => {
      const option = document.createElement('option');
      option.value = String(index);
      option.textContent = `${seconds.toFixed(2)}秒 · フレーム${sourceFrame}`;
      return option;
    }));
    session = new SceneSession({
      frameLoader: new SnapshotLoader(new FrameLoader()),
      fetchManifest: async () => new Response(JSON.stringify(raw)),
      notify: event => {
        if (event.type === 'ready') {
          select.disabled = false;
          select.value = String(event.frame);
          document.querySelector('#reset').disabled = false;
          message('ドラッグで視点を回す · ピンチで拡大');
        } else if (event.type === 'loading') {
          message('モデルを準備しています');
        } else if (event.type === 'error') {
          message('描画を開始できませんでした。再読み込みしてください。');
        }
      },
      createViewer: camera => {
        cameraStart = camera;
        const viewer = new Viewer({
          rootElement: viewport,
          cameraUp: camera.up, initialCameraPosition: camera.position, initialCameraLookAt: camera.lookAt,
          sharedMemoryForWorkers: false, gpuAcceleratedSort: false,
          sphericalHarmonicsDegree: 0, sceneRevealMode: SceneRevealMode.Instant,
          renderMode: RenderMode.OnChange,
        });
        viewer.camera.near = 0.2;
        viewer.camera.fov = camera.fovYDegrees;
        const releaseIntrinsics = attachCameraIntrinsics(viewer.camera, camera.intrinsics);
        viewer.camera.updateProjectionMatrix();
        const releaseSampling = attachReferenceSampling(viewer, camera.referenceImageHeight);
        const dispose = viewer.dispose.bind(viewer);
        viewer.dispose = () => { releaseSampling(); releaseIntrinsics(); return dispose(); };
        return viewer;
      },
    });
    await session.load(source);
  } catch (error) {
    message(error.message || 'モデルを開けませんでした。');
  } finally { loading = false; }
}

select.addEventListener('change', () => { void session?.seek(Number(select.value)); });
document.querySelector('#reset').addEventListener('click', () => {
  const viewer = session?.viewer;
  if (!viewer || !cameraStart) return;
  viewer.camera.position.fromArray(cameraStart.position);
  viewer.camera.up.fromArray(cameraStart.up);
  viewer.controls.target.fromArray(cameraStart.lookAt);
  viewer.controls.update();
  viewer.forceRenderNextFrame();
});
login.addEventListener('submit', async event => {
  event.preventDefault();
  const button = login.querySelector('button');
  const password = document.querySelector('#password');
  button.disabled = true;
  try {
    const response = await fetch('/api/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: document.querySelector('#username').value, password: password.value }) });
    password.value = '';
    if (!response.ok) throw new Error('ログインできませんでした。入力を確認してください。');
    await start();
  } catch (error) { message(error.message); }
  finally { password.value = ''; button.disabled = false; }
});
document.addEventListener('visibilitychange', () => session?.pause(document.hidden));
window.addEventListener('pagehide', () => { void session?.dispose(); });
void start();
