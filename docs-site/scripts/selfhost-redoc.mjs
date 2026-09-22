// Self-hosts the Redoc viewer JS: downloads the exact pinned bundle the
// generated page references and rewrites the script tag to the local
// copy, so /docs/api/ works offline and behind firewalls.
import { readFileSync, writeFileSync } from 'node:fs';

const page = 'dist/api/index.html';
const bundle = 'dist/api/redoc.standalone.js';

const html = readFileSync(page, 'utf8');
const match = html.match(/src="(https:\/\/cdn\.redocly\.com\/[^"]+\.js)"/);

if (!match) {
  throw new Error('no redoc CDN script found in dist/api/index.html');
}

const res = await fetch(match[1]);

if (!res.ok) {
  throw new Error(`fetching ${match[1]}: ${res.status}`);
}

writeFileSync(bundle, Buffer.from(await res.arrayBuffer()));
writeFileSync(page, html.replace(match[1], './redoc.standalone.js'));
console.log(`self-hosted ${match[1]} -> ${bundle}`);
