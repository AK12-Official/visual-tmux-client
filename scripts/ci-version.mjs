import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

export function buildVersion(version, ref, sha) {
  if (version.trim() !== version || !/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version)) {
    throw new Error('Source version must be a stable semantic version');
  }
  if (sha.length !== 40 || !/^[a-f0-9]{40}$/.test(sha)) {
    throw new Error('A full commit SHA is required');
  }
  if (ref.startsWith('refs/tags/')) {
    if (ref !== `refs/tags/v${version}`) {
      throw new Error(`Release tag must match source version v${version}`);
    }
    return version;
  }
  return `${version}-dev.${sha.slice(0, 7)}`;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const pkg = JSON.parse(readFileSync('hub/web/package.json', 'utf8'));
  const lock = JSON.parse(readFileSync('hub/web/package-lock.json', 'utf8'));
  const makefile = readFileSync('Makefile', 'utf8');
  const packaging = readFileSync('scripts/package.sh', 'utf8');
  const versions = [
    lock.version,
    lock.packages[''].version,
    /^VERSION \?= (.+)$/m.exec(makefile)?.[1],
    /version=\$\{VERSION:-([^}]+)\}/.exec(packaging)?.[1],
  ];
  if (versions.some(version => version !== pkg.version)) {
    throw new Error('Source version defaults and package lock must agree');
  }
  console.log(`version=${buildVersion(pkg.version, process.env.GITHUB_REF ?? '', process.env.GITHUB_SHA ?? '')}`);
}
