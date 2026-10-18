import { describe, expect, it } from 'vitest';

import { binaryName, platformPackage } from '../src/platform.js';
import { extractFileFromTarball } from '../src/tarball.js';

// Builds a minimal ustar archive like the ones npm publishes.
function tarball(files: Record<string, string>): Buffer {
	const blocks: Buffer[] = [];
	for (const [name, content] of Object.entries(files)) {
		const header = Buffer.alloc(512);
		const [prefix, base] = name.length > 100 ? [name.slice(0, name.lastIndexOf('/')), name.slice(name.lastIndexOf('/') + 1)] : ['', name];
		header.write(base, 0, 'utf8');
		header.write('0000644\0', 100);
		header.write(`${content.length.toString(8).padStart(11, '0')}\0`, 124);
		header.write('ustar\0', 257);
		header.write(prefix, 345, 'utf8');
		const body = Buffer.alloc(Math.ceil(content.length / 512) * 512);
		body.write(content);
		blocks.push(header, body);
	}
	blocks.push(Buffer.alloc(1024));
	return Buffer.concat(blocks);
}

describe('extractFileFromTarball', () => {
	it('returns the requested file contents', () => {
		const archive = tarball({
			'package/package.json': '{"name":"x"}',
			'package/bin/tsc-lua': 'binary contents that span more than one block'.repeat(20),
		});
		expect(extractFileFromTarball(archive, 'package/bin/tsc-lua')?.toString()).toBe('binary contents that span more than one block'.repeat(20));
	});

	it('supports names stored with the ustar prefix field', () => {
		const longName = `package/${'d'.repeat(90)}/bin/tsc-lua.exe`;
		const archive = tarball({ [longName]: 'exe' });
		expect(extractFileFromTarball(archive, longName)?.toString()).toBe('exe');
	});

	it('returns undefined for missing files', () => {
		expect(extractFileFromTarball(tarball({ 'package/a': 'a' }), 'package/b')).toBeUndefined();
	});
});

describe('platform packages', () => {
	it('maps supported platforms to their package and executable name', () => {
		expect(platformPackage('win32', 'x64')).toBe('@tsc-lua/win32-x64');
		expect(platformPackage('darwin', 'arm64')).toBe('@tsc-lua/darwin-arm64');
		expect(binaryName('win32')).toBe('tsc-lua.exe');
		expect(binaryName('linux')).toBe('tsc-lua');
	});

	it('picks the musl build on Linux with musl libc', () => {
		expect(platformPackage('linux', 'x64', true)).toBe('@tsc-lua/linux-x64-musl');
		expect(platformPackage('linux', 'arm64', false)).toBe('@tsc-lua/linux-arm64');
	});

	it('runs the x64 build on Windows on Arm, under emulation', () => {
		expect(platformPackage('win32', 'arm64')).toBe('@tsc-lua/win32-x64');
	});

	it('rejects unsupported platforms', () => {
		expect(() => platformPackage('aix', 'ppc64')).toThrow('unsupported platform');
	});
});
