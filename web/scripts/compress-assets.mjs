import { readdir, readFile, writeFile } from 'node:fs/promises';
import { URL } from 'node:url';
import { promisify } from 'node:util';
import { gzip } from 'node:zlib';

const assets = new URL('../dist/assets/', import.meta.url);
const compress = promisify(gzip);

for (const name of await readdir(assets)) {
  if (!/\.(js|css)$/.test(name)) continue;

  const asset = new URL(name, assets);
  const data = await readFile(asset);
  const encoded = await compress(data, { level: 9 });
  if (encoded.length < data.length) {
    await writeFile(new URL(name + '.gz', assets), encoded);
  }
}
