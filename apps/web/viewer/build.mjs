import { build } from 'esbuild';
import { readFile, writeFile } from 'node:fs/promises';
import { patchTextureSizing } from './texture-sizing.mjs';

const upstream = JSON.parse(await readFile(new URL('node_modules/@mkkellogg/gaussian-splats-3d/package.json', import.meta.url), 'utf8'));
let patchedModules = 0;

const notices = await Promise.all([
  ['GaussianSplats3D 0.4.7', 'node_modules/@mkkellogg/gaussian-splats-3d/LICENSE'],
  ['three 0.160.0', 'node_modules/three/LICENSE'],
].map(async ([name, path]) => `${name}\n\n${await readFile(new URL(path, import.meta.url), 'utf8')}`));
await build({
  entryPoints: { viewer: 'src/index.js', 'private-preview': 'src/private-preview.js', delivery: 'src/delivery.js', 'frame-worker': 'src/frame-worker.js',
    'temporal-worker': 'src/temporal-worker.js' },
  bundle: true, format: 'esm', minify: true,
  legalComments: 'eof', outdir: '../web/viewer',
  plugins: [{
    name: 'gaussian-splats-texture-sizing',
    setup(bundler) {
      bundler.onLoad({ filter: /gaussian-splats-3d\.module\.js$/ }, async ({ path }) => {
        const contents = patchTextureSizing(await readFile(path, 'utf8'), upstream.version);
        patchedModules++;
        return { contents, loader: 'js' };
      });
    },
  }],
  banner: { js: '/*! GaussianSplats3D and three.js MIT notices: ./THIRD_PARTY_LICENSES.txt */' },
});
if (patchedModules !== 1) throw new Error('GaussianSplats3D texture sizing patch was not applied exactly once');
await writeFile(new URL('../web/viewer/THIRD_PARTY_LICENSES.txt', import.meta.url), notices.join('\n\n'));
