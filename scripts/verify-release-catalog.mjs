import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { catalog, catalogDigest, checkInstallers, installers, isCurrent } from './write-releases.mjs';

const assets = names => names.map(name => ({ name, state: 'uploaded', size: 1 }));
const files = [...installers, 'SHA256SUMS.txt'];
const releases = [
  { tag_name: 'v1.9.0', published_at: '2026-11-21T00:00:00Z', assets: assets(files) },
  { tag_name: 'v2.0.0', published_at: '2026-10-01T00:00:00Z', assets: assets(files) },
  { tag_name: 'v2.0.0-rc.1', draft: false, prerelease: true, assets: [] },
  { tag_name: 'v2.0.1', draft: true, prerelease: false, assets: [] }
];

assert.equal(isCurrent(releases, 'v2.0.0'), true, 'stable version order wins over publication date');
assert.equal(isCurrent(releases, 'v1.9.0'), false, 'an older deployment cannot replace a newer release');
assert.throws(() => isCurrent(releases, 'v2.0.1'), /published release not found/);
assert.throws(() => isCurrent([{ ...releases[0], assets: assets(files.slice(1)) }], 'v1.9.0'), /incorrect release files/);

const site = catalog(releases, 'v1.9.0');
assert.match(site, /HYB2_RELEASE_PRIMARY = \{"tag":"v1\.9\.0","files":\[/);
const history = catalog(releases, '');
assert.ok(history.indexOf('"tag": "v2.0.0"') < history.indexOf('"tag": "v1.9.0"'),
  'historical releases use semantic version order, not publication date');
const currentCatalog = catalogDigest(releases);
assert.equal(catalogDigest(releases), currentCatalog, 'an unchanged catalog keeps its deployment fingerprint');
assert.notEqual(catalogDigest([...releases, { tag_name: 'v2.1.0', published_at: '2026-11-22T00:00:00Z', assets: assets(files) }]), currentCatalog,
  'a release published after the main build invalidates its catalog fingerprint');
assert.doesNotMatch(site, /v2\.0\.0-rc\.1|v2\.0\.1/);

const root = mkdtempSync(join(tmpdir(), 'hyb2-release-catalog-'));
try {
  for (const file of installers) writeFileSync(join(root, file), 'installer');
  checkInstallers(root);
  writeFileSync(join(root, 'unexpected.exe'), 'extra');
  assert.throws(() => checkInstallers(root), /incorrect release files/);
  rmSync(join(root, 'unexpected.exe'));
  mkdirSync(join(root, 'directory'));
  assert.throws(() => checkInstallers(root), /regular installer files/);
} finally {
  rmSync(root, { recursive: true, force: true });
}

process.stdout.write('release-catalog: ok\n');
