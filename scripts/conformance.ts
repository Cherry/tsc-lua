// Runs typescript-go's compiler/conformance baseline suites under tsc-lua, sharded across
// processes, and summarizes the results.
// Usage: node scripts/conformance.ts [--suite all|compiler|conformance] [--run <go test -run pattern>]
//        [--jobs N] [--shards-per-job N] [--out build/conformance] [--lua interpreter] [--build build/lua] [--mem-limit MB]
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { parseArgs } from 'node:util';

const root = path.resolve(import.meta.dirname, '..');

// Prefer the LuaJIT from scripts/build-dev-lua.sh: it is built with a Lua stack deep enough
// for the compiler's recursion (stock LuaJIT stops at 65500 slots).
function defaultLua(): string {
	const built = path.join(root, 'build', 'tools', process.platform === 'win32' ? 'luajit.exe' : 'luajit');
	return fs.existsSync(built) ? built : 'luajit';
}

interface Result {
	name: string;
	status: 'pass' | 'fail' | 'skip';
}

// One job per 3 GB of RAM at most: the heaviest tests need about that much each.
const gigabyte = 1024 ** 3;
const memoryJobs = Math.floor(os.totalmem() / (3 * gigabyte));
const defaultJobs = Math.max(1, Math.min(os.availableParallelism() - 1, memoryJobs));

const { values } = parseArgs({
	options: {
		'suite': { type: 'string', default: 'all' },
		'run': { type: 'string', default: '' },
		'jobs': { type: 'string', default: String(defaultJobs) },
		// Processes per job: each runs a slice of the tests and exits, because a process keeps its
		// peak memory until it ends.
		'shards-per-job': { type: 'string', default: '8' },
		'out': { type: 'string', default: path.join(root, 'build', 'conformance') },
		'lua': { type: 'string', default: process.env.LUA ?? defaultLua() },
		'build': { type: 'string', default: path.join(root, 'build', 'lua') },
		// Per process, in MB: a test whose live heap grows past it fails instead of exhausting memory.
		// LuaJIT's count includes garbage not yet collected, which runs well ahead of the live heap.
		'mem-limit': { type: 'string', default: '12288' },
	},
});

const jobs = Number.parseInt(values.jobs, 10);
const shardCount = jobs * Number.parseInt(values['shards-per-job'], 10);
fs.mkdirSync(values.out, { recursive: true });

function parseLog(log: string): Result[] {
	const results: Result[] = [];
	for (const line of log.split('\n')) {
		const match = /^(ok {3}|FAIL |skip )(.+)$/.exec(line);
		if (!match) {
			continue;
		}
		const statuses: Record<string, Result['status']> = { ok: 'pass', FAIL: 'fail', skip: 'skip' };
		results.push({ name: match[2], status: statuses[match[1].trim()] });
	}
	return results;
}

function runShard(index: number): Promise<Result[]> {
	const resultsFile = path.join(values.out, `shard-${index}.json`);
	const logFile = path.join(values.out, `shard-${index}.log`);
	const args = [path.join(root, 'runtime', 'run.lua'), path.resolve(values.build), 'tsc.cmd.tscluatest', '-suite', values.suite];
	if (values.run) {
		args.push('-test.run', values.run);
	}
	return new Promise((resolve) => {
		const log = fs.createWriteStream(logFile);
		// Unix main threads get their stack from ulimit; deep recursion needs more than the default.
		const [command, commandArgs] = process.platform === 'win32'
			? [values.lua, args]
			: ['sh', ['-c', 'ulimit -s unlimited 2>/dev/null || ulimit -s 1048576 2>/dev/null; exec "$0" "$@"', values.lua, ...args]];
		const child = spawn(command, commandArgs, {
			cwd: root,
			env: {
				...process.env,
				TSCLUA_REPO_ROOT: path.join(root, 'upstream', 'TypeScript', 'tsc'),
				TSCLUA_SHARD: `${index}/${shardCount}`,
				TSCLUA_TEST_RESULTS: resultsFile,
				TSCLUA_TEST_VERBOSE: '1',
				TSCLUA_MEMLIMIT_MB: values['mem-limit'],
			},
		});
		child.stdout.pipe(log);
		child.stderr.pipe(log);
		child.on('close', (code, signal) => {
			let results: Result[];
			try {
				results = JSON.parse(fs.readFileSync(resultsFile, 'utf8')) as Result[];
			} catch {
				// The shard crashed before reporting; recover what it logged.
				results = parseLog(fs.readFileSync(logFile, 'utf8'));
				console.error(`shard ${index} crashed (exit ${signal ?? code}) after ${results.length} results; see ${logFile}`);
			}
			resolve(results);
		});
	});
}

const started = Date.now();
const shards: Result[][] = [];
let nextShard = 0;
async function worker(): Promise<void> {
	while (nextShard < shardCount) {
		const index = nextShard++;
		shards[index] = await runShard(index);
	}
}
await Promise.all(Array.from({ length: jobs }, () => worker()));
const results = shards.flat().toSorted((left, right) => left.name.localeCompare(right.name));

// Each test must run in exactly one shard; a repeat means some other test did not run at all.
const repeated = results.filter((result, index) => index > 0 && result.name === results[index - 1].name);
if (repeated.length > 0) {
	console.error(`${repeated.length} results were reported by more than one shard, e.g. ${repeated[0].name}`);
	process.exitCode = 1;
}

// A test file passes when every baseline (subtest) for it passes.
const files = new Map<string, boolean>();
const byKind = new Map<string, { pass: number; total: number; }>();
for (const result of results) {
	const [file, kind = 'test'] = result.name.split('/', 2);
	if (result.status === 'skip') {
		continue;
	}
	files.set(file, (files.get(file) ?? true) && result.status === 'pass');
	const entry = byKind.get(kind) ?? { pass: 0, total: 0 };
	entry.total++;
	if (result.status === 'pass') {
		entry.pass++;
	}
	byKind.set(kind, entry);
}
const passedFiles = files.values().filter(Boolean).toArray().length;
const passedBaselines = results.filter(result => result.status === 'pass').length;
const totalBaselines = results.filter(result => result.status !== 'skip').length;

const lines = [
	`tests:     ${passedFiles}/${files.size} (${(passedFiles * 100 / Math.max(files.size, 1)).toFixed(2)}%)`,
	`baselines: ${passedBaselines}/${totalBaselines} (${(passedBaselines * 100 / Math.max(totalBaselines, 1)).toFixed(2)}%)`,
	...[...byKind].toSorted(([left], [right]) => left.localeCompare(right)).map(([kind, entry]) => `  ${kind.padEnd(28)} ${entry.pass}/${entry.total}`),
	`time:      ${((Date.now() - started) / 1000).toFixed(0)}s with ${jobs} jobs`,
];
console.log(lines.join('\n'));
fs.writeFileSync(path.join(values.out, 'summary.txt'), `${lines.join('\n')}\n`);
fs.writeFileSync(path.join(values.out, 'results.json'), `${JSON.stringify(results, null, '\t')}\n`);
fs.writeFileSync(path.join(values.out, 'failures.txt'), `${results.filter(result => result.status === 'fail').map(result => result.name).join('\n')}\n`);
