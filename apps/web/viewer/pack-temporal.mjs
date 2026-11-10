import { readFile, stat, writeFile } from 'node:fs/promises';
import { constants, gzipSync } from 'node:zlib';
import { createHash } from 'node:crypto';
import { decodeTemporal, MAX_TEMPORAL_BYTES, packTemporal } from './src/temporal-codec.js';

try {
  const [inputPath, outputPath, ...extra] = process.argv.slice(2);
  if (!inputPath || !outputPath || extra.length > 1 ||
      (extra.length && !['--strategy=rle', '--strategy=auto'].includes(extra[0]))) {
    throw new Error('Usage: node pack-temporal.mjs input.rgs output.rgs [--strategy=rle|auto]');
  }
  const strategy = extra.length ? extra[0].slice('--strategy='.length) : 'default';
  let selectedStrategy = strategy === 'rle' ? 'rle' : 'default';
  const info = await stat(inputPath);
  if (!info.isFile() || info.size > MAX_TEMPORAL_BYTES) throw new Error('Input must be a regular RGS1 file of at most 32 MiB');
  const input = new Uint8Array(await readFile(inputPath));
  const packed = packTemporal(input.buffer, bytes => {
    const original = gzipSync(bytes, {
      level: 9, strategy: strategy === 'rle' ? constants.Z_RLE : constants.Z_DEFAULT_STRATEGY,
    });
    if (strategy !== 'auto') return original;
    const rle = gzipSync(bytes, { level: 9, strategy: constants.Z_RLE });
    if (rle.length >= original.length) return original;
    selectedStrategy = 'rle';
    return rle;
  });
  const decoded = await decodeTemporal(packed);
  if (!Buffer.from(decoded).equals(input)) throw new Error('Lossless round-trip verification failed');
  // A poorly compressible asset keeps its original representation and remains readable by the same decoder.
  const output = packed.byteLength < input.byteLength ? new Uint8Array(packed) : input;
  await writeFile(outputPath, output, { flag: 'wx' });
  const hash = bytes => createHash('sha256').update(bytes).digest('hex');
  process.stdout.write(JSON.stringify({ inputBytes: input.byteLength, outputBytes: output.byteLength,
    encoding: output === input ? 'raw' : 'byte-shuffle-gzip', strategy,
    selectedStrategy: output === input ? 'raw' : selectedStrategy,
    decodedSHA256: hash(input), encodedSHA256: hash(output) }) + '\n');
} catch (error) {
  process.stderr.write(error.message + '\n');
  process.exitCode = 1;
}
