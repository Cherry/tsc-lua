// Publishes the per-platform packages prepared by prepare-platform-packages.ts, before the
// main package so its optional dependencies resolve.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

const npmDir = path.resolve(import.meta.dirname, '..', 'build', 'npm');
if (!fs.existsSync(npmDir)) {
	console.error('no platform packages to publish; run prepare-platform-packages.ts first');
	process.exit(1);
}

for (const entry of fs.readdirSync(npmDir, { withFileTypes: true })) {
	if (!entry.isDirectory()) {
		continue;
	}
	const dir = path.join(npmDir, entry.name);
	console.log(`publishing ${entry.name}`);
	execFileSync('npm', ['publish', '--provenance', '--access', 'public'], { cwd: dir, stdio: 'inherit', shell: process.platform === 'win32' });
}
