// Times a full type check of each benchmark app with each compiler (one warm-up run, then the
// median of --runs runs) after scripts/bench/setup.sh. Also records each tool's peak memory (from
// the first run) and error count, so results can be checked for agreement.
// Usage: node scripts/bench/run.ts <work-dir> [--apps trpc,typeorm] [--tools tsc7,tsc-lua] [--runs 5]
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { parseArgs } from 'node:util';

const root = path.resolve(import.meta.dirname, '..', '..');
const { values, positionals } = parseArgs({
	allowPositionals: true,
	options: {
		'apps': { type: 'string', default: 'trpc,typeorm,excalidraw,playwright,sentry,vscode' },
		'tools': { type: 'string', default: 'tsc6,tsc7,tsc-rs,tsc-rs-fast,tsrs,tsc-ts,bun,tsc-lua' },
		'runs': { type: 'string', default: '5' },
		'slow-runs': { type: 'string', default: '1' },
		'timeout': { type: 'string', default: String(4 * 60 * 60) },
	},
});
const work = path.resolve(positionals[0] ?? path.join(root, '..', 'tsc-lua-bench'));
const resultsDir = path.join(work, 'results');
fs.mkdirSync(resultsDir, { recursive: true });
const isWindows = process.platform === 'win32';
const exe = isWindows ? '.exe' : '';
// Runs a package's bin script with node directly; Windows .cmd shims would need a shell.
function bin(pkg: string, name = pkg): Pick<Tool, 'command' | 'args'> {
	const dir = path.join(work, 'node_modules', pkg);
	const manifest = JSON.parse(fs.readFileSync(path.join(dir, 'package.json'), 'utf8')) as { bin?: string | Record<string, string>; };
	const script = typeof manifest.bin === 'string' ? manifest.bin : manifest.bin?.[name];
	if (!script) {
		throw new Error(`${pkg} has no bin named ${name}`);
	}
	return { command: process.execPath, args: [path.join(dir, script)] };
}

// Tools whose packages are not installed are skipped instead of failing the whole run.
function binTool(pkg: string, name: string, args: string[]): Tool | undefined {
	try {
		const base = bin(pkg, name);
		return { command: base.command, args: [...base.args, ...args] };
	} catch {
		return undefined;
	}
}

// app -> [repo dir, tsconfig]
const apps: Record<string, [string, string]> = {
	trpc: ['trpc', 'packages/server/tsconfig.json'],
	typeorm: ['typeorm', 'packages/typeorm/tsconfig.bench.json'],
	excalidraw: ['excalidraw', 'tsconfig.bench.json'],
	playwright: ['playwright', 'tsconfig.json'],
	sentry: ['sentry', 'tsconfig.json'],
	vscode: ['vscode', 'src/tsconfig.json'],
};

interface Tool {
	command: string;
	args: string[];
	slow?: boolean;
}

function tools(config: string): Record<string, Tool | undefined> {
	const flags = ['-p', config, '--noEmit', '--incremental', 'false', '--pretty', 'false'];
	return {
		'tsc6': { command: process.execPath, args: ['--max-old-space-size=16384', path.join(work, 'node_modules', 'ts6', 'lib', 'tsc.js'), ...flags] },
		'tsc7': binTool('typescript', 'tsc', flags),
		'tsc-rs': binTool('tsc-rs', 'tsc-rs', flags),
		'tsc-rs-fast': binTool('tsc-rs', 'tsc-rs', [...flags, '--fast']),
		'tsrs': binTool('tsrs', 'tsrs', flags),
		'tsc-ts': binTool('tsc-ts', 'tsc-ts', flags),
		'bun': { command: 'bun', args: ['check', '-p', config, '--noEmit', '--incremental', 'false', '--no-pretty', '--all'] },
		'tsc-lua': { command: path.join(root, 'build', 'bin', `tsc-lua${exe}`), args: flags, slow: true },
	};
}

interface Measurement {
	app: string;
	tool: string;
	times: number[];
	median: number;
	exitCode: number | null;
	errors: number;
	peakMB: number | undefined;
}

interface Run {
	seconds: number;
	status: number | null;
	output: string;
	peakBytes?: number;
}

function runOnce(tool: Tool, cwd: string): Run {
	const started = process.hrtime.bigint();
	const result = spawnSync(tool.command, tool.args, {
		cwd,
		encoding: 'utf8',
		maxBuffer: 1024 * 1024 * 1024,
		timeout: Number(values.timeout) * 1000,
	});
	const seconds = Number(process.hrtime.bigint() - started) / 1e9;
	return { seconds, status: result.status, output: `${result.stdout ?? ''}${result.stderr ?? ''}` };
}

// Runs a tool once recording its peak memory, the largest of any process it starts (npm bin
// shims start the real compiler as a child): /usr/bin/time reports it on Linux (%M, in KB) and
// macOS (-l, in bytes), and peak.ps1 polls peak working sets on Windows.
function runMeasured(tool: Tool, cwd: string): Run {
	if (isWindows) {
		const out = path.join(resultsDir, 'peak-output.txt');
		const script = path.join(import.meta.dirname, 'peak.ps1');
		const result = spawnSync('pwsh', ['-NoProfile', '-File', script, '-Out', out, '-Command', tool.command, '-ArgumentsJson', JSON.stringify(tool.args)], {
			cwd,
			encoding: 'utf8',
			timeout: Number(values.timeout) * 1000,
		});
		const report = JSON.parse(result.stdout) as { seconds: number; peakBytes: number; exitCode: number; };
		const output = fs.readFileSync(out, 'utf8');
		fs.rmSync(out);
		return { seconds: report.seconds, status: report.exitCode, output, peakBytes: report.peakBytes };
	}
	if (process.platform === 'darwin') {
		const run = runOnce({ command: '/usr/bin/time', args: ['-l', tool.command, ...tool.args] }, cwd);
		const match = /(\d+)\s+maximum resident set size/.exec(run.output);
		return { ...run, peakBytes: match ? Number(match[1]) : undefined };
	}
	// GNU time writes the format last, after a "Command exited with non-zero status" line if any.
	const file = path.join(resultsDir, 'peak.txt');
	const run = runOnce({ command: '/usr/bin/time', args: ['-f', '%M', '-o', file, tool.command, ...tool.args] }, cwd);
	const match = /(\d+)\s*$/.exec(fs.readFileSync(file, 'utf8'));
	fs.rmSync(file);
	return { ...run, peakBytes: match ? Number(match[1]) * 1024 : undefined };
}

const median = (list: number[]) => {
	const sorted = list.toSorted((left, right) => left - right);
	return sorted[Math.floor(sorted.length / 2)];
};

const measurements: Measurement[] = [];
for (const app of values.apps.split(',')) {
	const [dir, config] = apps[app];
	const cwd = path.join(work, 'repos', dir);
	for (const [name, tool] of Object.entries(tools(config))) {
		if (!values.tools.split(',').includes(name)) {
			continue;
		}
		if (!tool) {
			console.log(`${app.padEnd(11)} ${name.padEnd(12)} not installed, skipped`);
			continue;
		}
		const runs = Number(tool.slow ? values['slow-runs'] : values.runs);
		// The first run doubles as the warm-up and provides the diagnostics and peak memory.
		const first = runMeasured(tool, cwd);
		fs.writeFileSync(path.join(resultsDir, `${app}.${name}.diag`), first.output);
		const errors = (first.output.match(/error TS\d+/g) ?? []).length;
		// A failing exit without diagnostics is a crash, and its time would mislead.
		if (first.status !== 0 && errors === 0) {
			console.log(`${app.padEnd(11)} ${name.padEnd(12)} failed (exit=${first.status}), see ${app}.${name}.diag`);
			continue;
		}
		const times = tool.slow && runs <= 1 ? [first.seconds] : [];
		for (let i = times.length; i < runs; i++) {
			times.push(runOnce(tool, cwd).seconds);
		}
		const peakMB = first.peakBytes === undefined ? undefined : Math.round(first.peakBytes / 1024 / 1024);
		const measurement = { app, tool: name, times, median: median(times), exitCode: first.status, errors, peakMB };
		measurements.push(measurement);
		console.log(`${app.padEnd(11)} ${name.padEnd(12)} ${measurement.median.toFixed(2).padStart(9)}s  peak=${String(peakMB ?? '?').padStart(6)} MB  exit=${first.status} errors=${errors}`);
		fs.writeFileSync(path.join(resultsDir, 'results.json'), `${JSON.stringify(measurements, null, '\t')}\n`);
	}
}
