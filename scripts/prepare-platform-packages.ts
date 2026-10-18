// Creates the per-platform npm packages from CI build artifacts and pins them as optional
// dependencies of the main package. Usage: node scripts/prepare-platform-packages.ts <version>
import fs from 'node:fs';
import path from 'node:path';

import { binaryName, platformPackages } from '../src/platform.ts';

const root = path.resolve(import.meta.dirname, '..');
const version = process.argv[2];
if (!version) {
	console.error('usage: prepare-platform-packages.ts <version>');
	process.exit(1);
}

const rootPackagePath = path.join(root, 'package.json');
const rootPackage = JSON.parse(fs.readFileSync(rootPackagePath, 'utf8')) as {
	description: string;
	license: string;
	repository: unknown;
	optionalDependencies?: Record<string, string>;
};

const optionalDependencies: Record<string, string> = {};
for (const [key, name] of Object.entries(platformPackages)) {
	const [platform, arch, libc] = key.split('-', 3);
	const artifact = path.join(root, 'build', 'platforms', `bin-${key}`, binaryName(platform));
	// A release must carry every platform, or installs there would fall back to a missing package.
	if (!fs.existsSync(artifact)) {
		throw new Error(`no build artifact for ${name} at ${artifact}`);
	}
	const dir = path.join(root, 'build', 'npm', key);
	fs.mkdirSync(path.join(dir, 'bin'), { recursive: true });
	fs.copyFileSync(artifact, path.join(dir, 'bin', binaryName(platform)));
	fs.chmodSync(path.join(dir, 'bin', binaryName(platform)), 0o755);
	// Named copies are attached to the GitHub release for direct downloads.
	const releaseDir = path.join(root, 'build', 'release');
	fs.mkdirSync(releaseDir, { recursive: true });
	fs.copyFileSync(artifact, path.join(releaseDir, `tsc-lua-${key}${platform === 'win32' ? '.exe' : ''}`));
	// The executable contains TypeScript, Go and Lua code, so it ships with their licenses.
	for (const file of ['LICENSE', 'THIRD_PARTY_NOTICES.txt']) {
		fs.copyFileSync(path.join(root, file), path.join(dir, file));
	}
	fs.writeFileSync(path.join(dir, 'package.json'), `${JSON.stringify({
		name,
		version,
		description: `The ${key} executable for tsc-lua`,
		license: rootPackage.license,
		repository: rootPackage.repository,
		os: [platform],
		cpu: [arch],
		// Package managers that know the libc field skip the Linux build for the other libc.
		...(platform === 'linux' && { libc: [libc === 'musl' ? 'musl' : 'glibc'] }),
		files: ['bin', 'THIRD_PARTY_NOTICES.txt'],
		publishConfig: { access: 'public' },
	}, null, '\t')}\n`);
	fs.writeFileSync(path.join(dir, 'README.md'), `# ${name}\n\nThe ${key} executable for [tsc-lua](https://github.com/Cherry/tsc-lua). Install \`tsc-lua\` instead of this package.\n`);
	optionalDependencies[name] = version;
}

rootPackage.optionalDependencies = optionalDependencies;
fs.writeFileSync(rootPackagePath, `${JSON.stringify(rootPackage, null, '\t')}\n`);
console.log(`prepared ${Object.keys(optionalDependencies).length} platform packages for ${version}`);
