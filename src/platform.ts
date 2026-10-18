import { createRequire } from 'node:module';
import path from 'node:path';

const require = createRequire(import.meta.url);

/**
 * Platform packages published alongside tsc-lua, keyed by `${process.platform}-${process.arch}`
 * with a `-musl` suffix for Linux on musl libc (Alpine).
 */
export const platformPackages: Record<string, string> = {
	'darwin-arm64': '@tsc-lua/darwin-arm64',
	'darwin-x64': '@tsc-lua/darwin-x64',
	'linux-arm64': '@tsc-lua/linux-arm64',
	'linux-arm64-musl': '@tsc-lua/linux-arm64-musl',
	'linux-x64': '@tsc-lua/linux-x64',
	'linux-x64-musl': '@tsc-lua/linux-x64-musl',
	'win32-arm64': '@tsc-lua/win32-arm64',
	'win32-x64': '@tsc-lua/win32-x64',
};

/** Reports whether this process runs on musl libc, where glibc executables cannot start. */
export function isMusl(): boolean {
	if (process.platform !== 'linux') {
		return false;
	}
	// Node reports the glibc version it runs against, and none on musl.
	const report = process.report.getReport() as { header?: { glibcVersionRuntime?: string; }; };
	return report.header?.glibcVersionRuntime === undefined;
}

export function platformKey(platform: string = process.platform, arch: string = process.arch, musl: boolean = platform === 'linux' && isMusl()): string {
	return musl ? `${platform}-${arch}-musl` : `${platform}-${arch}`;
}

export function binaryName(platform: string = process.platform): string {
	return platform === 'win32' ? 'tsc-lua.exe' : 'tsc-lua';
}

export function platformPackage(platform: string = process.platform, arch: string = process.arch, musl: boolean = platform === 'linux' && isMusl()): string {
	const key = platformKey(platform, arch, musl);
	const pkg = platformPackages[key];
	if (!pkg) {
		throw new Error(`tsc-lua: unsupported platform ${key}`);
	}
	return pkg;
}

/** Location of a binary downloaded by the install script when optional dependencies were skipped. */
export function fallbackBinaryPath(): string {
	return path.join(import.meta.dirname, '..', 'bin', binaryName());
}

/** Resolves the tsc-lua executable for the current platform. */
export function binaryPath(): string {
	const override = process.env.TSC_LUA_BINARY_PATH;
	if (override) {
		return override;
	}
	const pkg = platformPackage();
	try {
		return require.resolve(`${pkg}/bin/${binaryName()}`);
	} catch {
		return fallbackBinaryPath();
	}
}
