import test from 'node:test';
import assert from 'node:assert/strict';
import { Matrix4, PerspectiveCamera, Vector3 } from 'three';
import { attachCameraIntrinsics, validIntrinsics } from '../src/camera-intrinsics.js';

const intrinsics = { width: 1280, height: 720, fx: 900, fy: 910, cx: 630, cy: 350 };
const close = (actual, expected) => assert.ok(Math.abs(actual - expected) < 1e-9, `${actual} != ${expected}`);

test('calibrated projection matches the OpenCV pinhole equation across viewport aspects', () => {
  const camera = new PerspectiveCamera(50, 1280 / 720, .1, 100);
  attachCameraIntrinsics(camera, intrinsics);
  for (const width of [1280, 640, 300]) {
    camera.aspect = width / 720;
    camera.updateProjectionMatrix();
    for (const [x, y, z] of [[.2, .3, 4], [-.5, -.4, 3], [0, 0, 5]]) {
      const point = new Vector3(x, -y, -z).project(camera);
      close((point.x + 1) * width / 2, 900 * x / z + 630 - (1280 - width) / 2);
      close((1 - point.y) * 720 / 2, 910 * y / z + 350);
    }
    const identity = new Matrix4().multiplyMatrices(camera.projectionMatrix, camera.projectionMatrixInverse);
    identity.elements.forEach((value, index) => close(value, index % 5 === 0 ? 1 : 0));
  }
});

test('unspecified intrinsics preserve the existing camera and disposal releases the override', () => {
  const camera = new PerspectiveCamera(50, 16 / 9);
  const update = camera.updateProjectionMatrix;
  const before = camera.projectionMatrix.clone();
  attachCameraIntrinsics(camera, undefined)();
  assert.equal(camera.updateProjectionMatrix, update);
  assert.deepEqual(camera.projectionMatrix, before);
  const release = attachCameraIntrinsics(camera, intrinsics);
  assert.notEqual(camera.updateProjectionMatrix, update);
  release();
  assert.equal(camera.updateProjectionMatrix, update);
});

test('manifest intrinsics require finite focal lengths, dimensions and a valid principal point', () => {
  assert.ok(validIntrinsics(intrinsics));
  for (const change of [{ fx: 0 }, { fy: Infinity }, { cx: -1 }, { cy: 721 },
    { width: 0 }, { height: 16385 }, { width: 1280.5 }, { fx: undefined },
    { fx: Number.MIN_VALUE }, { fy: Number.MIN_VALUE }, { fy: Number.MAX_VALUE }, { fx: Number.MAX_VALUE },
    { fx: 1e-300 }, { fx: 1e300, fy: 1e300 }]) {
    assert.ok(!validIntrinsics({ ...intrinsics, ...change }));
  }
});
