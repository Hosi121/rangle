import { attachReferenceSampling } from './reference-sampling.js';
import { attachCameraIntrinsics } from './camera-intrinsics.js';
import { Viewer, SceneRevealMode, RenderMode } from '@mkkellogg/gaussian-splats-3d';
import { CHANNEL, SceneSession } from './session.js';
import { FrameLoader } from './frame-loader.js';

let session;
const send = (message) => window.parent.postMessage(JSON.stringify({ channel: CHANNEL, ...message }), location.origin);
window.addEventListener('message', async (event) => {
  if (event.origin !== location.origin || event.source !== window.parent || typeof event.data !== 'string') return;
  let message;
  try { message = JSON.parse(event.data); } catch { return; }
  if (message.channel !== CHANNEL) return;
  if (message.type === 'dispose') {
    await session?.dispose();
    send({ type: 'disposed' });
  } else if (message.type === 'load' && !session) {
    session = new SceneSession({
      frameLoader: new FrameLoader(),
      notify: send,
      createViewer: (camera) => {
        const viewer = new Viewer({
          cameraUp: camera.up, initialCameraPosition: camera.position, initialCameraLookAt: camera.lookAt,
          sharedMemoryForWorkers: false, gpuAcceleratedSort: false,
          sphericalHarmonicsDegree: 0, sceneRevealMode: SceneRevealMode.Instant,
          renderMode: RenderMode.OnChange,
        });
        viewer.camera.fov = camera.fovYDegrees;
        const releaseIntrinsics = attachCameraIntrinsics(viewer.camera, camera.intrinsics);
        viewer.camera.updateProjectionMatrix();
        const releaseSampling = attachReferenceSampling(viewer, camera.referenceImageHeight);
        const dispose = viewer.dispose.bind(viewer);
        viewer.dispose = () => { releaseSampling(); releaseIntrinsics(); return dispose(); };
        return viewer;
      },
    });
    await session.load(message.url);
  } else if (message.type === 'seek') {
    await session?.seek(message.frame);
  } else if (message.type === 'togglePlayback') {
    session?.togglePlayback();
  } else if (message.type === 'pause') {
    session?.pause(true);
  } else if (message.type === 'resume') {
    session?.pause(false);
  }
});
document.addEventListener('visibilitychange', () => session?.pause(document.hidden));
window.addEventListener('pagehide', () => { void session?.dispose(); });
send({ type: 'boot' });
