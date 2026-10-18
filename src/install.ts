import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import zlib from 'node:zlib';

import { binaryName, fallbackBinaryPath, platformPackage } from './platform.js';
import { extractFileFromTarball } from './tarball.js';

const require = createRequire(import.meta.url);
const packageJson = require('../package.json') as { version: string; };

function binaryWorks(binary: string): boolean {
	try {
		execFileSync(binary, ['--version'], { stdio: 'pipe' });
		return true;
	} catch {
		return false;
	}
}

async function downloadFromRegistry(pkg: string, version: string): Promise<Buffer> {
	const registry = (process.env.npm_config_registry ?? 'https://registry.npmjs.org').replace(/\/$/, '');
	const shortName = pkg.split('/', 2)[1];
	const url = `${registry}/${pkg}/-/${shortName}-${version}.tgz`;
	const response = await fetch(url);
	if (!response.ok) {
		throw new Error(`failed to download ${url}: ${response.status} ${response.statusText}`);
	}
	return zlib.gunzipSync(Buffer.from(await response.arrayBuffer()));
}

async function install(): Promise<void> {
	// Development checkouts have no published platform packages to fetch.
	if (packageJson.version.startsWith('0.0.0') || process.env.TSC_LUA_SKIP_DOWNLOAD) {
		return;
	}
	const pkg = platformPackage();
	try {
		require.resolve(`${pkg}/bin/${binaryName()}`);
		// Installed via optionalDependencies, nothing to do.
		return;
	} catch {
		// Optional dependencies can be disabled (--no-optional, some CI setups); fall back to fetching it.
	}
	const target = fallbackBinaryPath();
	if (fs.existsSync(target) && binaryWorks(target)) {
		return;
	}
	const tarball = await downloadFromRegistry(pkg, packageJson.version);
	const binary = extractFileFromTarball(tarball, `package/bin/${binaryName()}`);
	if (!binary) {
		throw new Error(`${pkg}@${packageJson.version} does not contain bin/${binaryName()}`);
	}
	fs.mkdirSync(path.dirname(target), { recursive: true });
	fs.writeFileSync(target, binary, { mode: 0o755 });
}

try {
	await install();
} catch (error) {
	console.error(`tsc-lua: failed to install the native executable: ${(error as Error).message}`);
	process.exitCode = 1;
}
