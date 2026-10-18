#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';

import { binaryPath } from './platform.js';

const binary = binaryPath();
if (!fs.existsSync(binary)) {
	console.error(`tsc-lua: executable not found at ${binary}. Try reinstalling tsc-lua.`);
	process.exit(1);
}

const result = spawnSync(binary, process.argv.slice(2), { stdio: 'inherit' });
if (result.error) {
	console.error(`tsc-lua: failed to run ${binary}: ${result.error.message}`);
	process.exit(1);
}
if (result.signal) {
	process.kill(process.pid, result.signal);
} else {
	process.exit(result.status ?? 1);
}
