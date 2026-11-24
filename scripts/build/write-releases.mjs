#!/usr/bin/env node
// A tag build is verified before the corresponding GitHub release exists.
// Its primary record describes the exact files the publisher must supply.
// The separately fetched history never selects or supplies primary files.
import { createHash } from 'node:crypto';
import { readdirSync, writeFileSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';

export const installers = ['HYB2-Desktop-Linux-x64.deb', 'HYB2-Desktop-Linux-x64.tar.gz',
  'HYB2-Desktop-Windows-x64-Setup.exe', 'HYB2-Desktop-macOS-arm64.dmg'];
const releaseFiles = [...installers, 'SHA256SUMS.txt'].sort();
const stable = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;

function checkTag(tag) {
  if (!stable.test(tag)) throw new Error(`expected stable release tag vMAJOR.MINOR.PATCH, got ${tag}`);
}

function exactFiles(actual, expected) {
  if (JSON.stringify([...actual].sort()) !== JSON.stringify(expected)) {
    throw new Error(`incorrect release files: ${JSON.stringify(actual)}; expected ${JSON.stringify(expected)}`);
  }
}

export function checkInstallers(directory) {
  const entries = readdirSync(directory, { withFileTypes: true });
  if (!entries.every(file => file.isFile())) throw new Error('expected regular installer files only');
  exactFiles(entries.map(file => file.name), installers);
}

export function catalog(list, tag) {
  if (tag) checkTag(tag);
  const history = list.filter(r => !r.draft && !r.prerelease && stable.test(r.tag_name) && r.tag_name !== tag)
    .sort((a, b) => {
      const left = a.tag_name.slice(1).split('.').map(BigInt);
      const right = b.tag_name.slice(1).split('.').map(BigInt);
      for (let index = 0; index < left.length; index++) {
        if (left[index] !== right[index]) return left[index] > right[index] ? -1 : 1;
      }
      return 0;
    })
    .slice(0, 12).map(r => ({ tag: r.tag_name, published: String(r.published_at).slice(0, 10),
      files: r.assets.filter(a => a.state === 'uploaded').map(a => a.name).sort() }));
  return `window.HYB2_RELEASE_PRIMARY = ${JSON.stringify(tag ? { tag, files: releaseFiles } : null)};\n` +
    `window.HYB2_RELEASES = ${JSON.stringify(history, null, 2)};\n`;
}

export function catalogDigest(list) {
  return createHash('sha256').update(catalog(list, '')).digest('hex');
}

// Run *inside* the Pages deployment lock. Publication time and API order
// are not version order (older installers may finish building last).
export function isCurrent(list, tag) {
  checkTag(tag);
  const releases = list.filter(r => !r.draft && !r.prerelease && stable.test(r.tag_name));
  const own = releases.find(r => r.tag_name === tag);
  if (!own) throw new Error(`published release not found: ${tag}`);
  exactFiles(own.assets.filter(a => a.state === 'uploaded' && a.size > 0).map(a => a.name), releaseFiles);
  const current = tag.slice(1).split('.').map(BigInt);
  return !releases.some(r => {
    const parts = r.tag_name.slice(1).split('.').map(BigInt);
    const index = parts.findIndex((value, i) => value !== current[i]);
    return index >= 0 && parts[index] > current[index];
  });
}

async function published(repo) {
  const headers = { Accept: 'application/vnd.github+json' };
  if (process.env.GITHUB_TOKEN) headers.Authorization = `Bearer ${process.env.GITHUB_TOKEN}`;
  const releases = [];
  for (let page = 1; ; page++) {
    const response = await fetch(`https://api.github.com/repos/${repo}/releases?per_page=100&page=${page}`, { headers });
    if (!response.ok) throw new Error(`release API: HTTP ${response.status}`);
    const batch = await response.json();
    releases.push(...batch);
    if (batch.length < 100) return releases;
  }
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  const [command, arg] = process.argv.slice(2);
  if (command === '--check-installers') {
    checkInstallers(arg);
  } else {
    const tag = process.env.RELEASE_TAG || '';
    if (tag && process.env.GITHUB_REF !== `refs/tags/${tag}`) throw new Error('release tag does not match invoking ref');
    if (tag) checkTag(tag);
    const repo = process.env.GITHUB_REPOSITORY || 'Vicky-0256/hyb2';
    const list = await published(repo); // API failures must not replace history with a stale snapshot.
    if (command === '--check-deployment') {
      const current = isCurrent(list, tag);
      console.log(`${tag}: ${current ? 'current' : 'superseded; not deploying'}`);
      writeFileSync(process.env.GITHUB_OUTPUT, `current=${current}\n`, { flag: 'a' });
    } else if (command === '--check-main-deployment') {
      const expected = process.env.RELEASE_CATALOG_SHA256 || '';
      const current = /^[a-f0-9]{64}$/.test(expected) && catalogDigest(list) === expected;
      console.log(`main release catalog: ${current ? 'current' : 'superseded; not deploying'}`);
      writeFileSync(process.env.GITHUB_OUTPUT, `current=${current}\n`, { flag: 'a' });
    } else if (!command) {
      writeFileSync(fileURLToPath(new URL('../../web/releases.js', import.meta.url)), catalog(list, tag));
    } else throw new Error(`unknown command: ${command}`);
  }
}
