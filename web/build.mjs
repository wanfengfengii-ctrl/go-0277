// Deterministic static build: copies the source page and assets into dist/,
// which the Go service embeds at runtime. No external dependencies are used,
// so the build is reproducible for a pinned Node version.
import { cpSync, mkdirSync, rmSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname } from 'node:path';

const root = dirname(fileURLToPath(import.meta.url));
const src = `${root}/src`;
const dist = `${root}/dist`;

rmSync(dist, { recursive: true, force: true });
mkdirSync(dist, { recursive: true });
cpSync(src, dist, { recursive: true });
console.log('built frontend -> web/dist');
