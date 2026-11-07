import { build } from 'esbuild';
import { readFile, writeFile } from 'node:fs/promises';

const notices = await Promise.all([
  ['GaussianSplats3D 0.4.7', 'node_modules/@mkkellogg/gaussian-splats-3d/LICENSE'],
  ['three 0.160.0', 'node_modules/three/LICENSE'],
].map(async ([name, path]) => `${name}\n\n${await readFile(new URL(path, import.meta.url), 'utf8')}`));
await build({
  entryPoints: ['src/index.js'], bundle: true, format: 'esm', minify: true,
  legalComments: 'eof', outfile: '../web/viewer/viewer.js',
  banner: { js: '/*! GaussianSplats3D and three.js MIT notices: ./THIRD_PARTY_LICENSES.txt */' },
});
await writeFile(new URL('../web/viewer/THIRD_PARTY_LICENSES.txt', import.meta.url), notices.join('\n\n'));
