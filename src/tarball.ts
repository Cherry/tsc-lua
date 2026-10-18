/**
 * Extracts a single file from a tar archive (ustar, as produced by `npm pack`).
 */
export function extractFileFromTarball(tarball: Buffer, filePath: string): Buffer | undefined {
	let offset = 0;
	while (offset + 512 <= tarball.length) {
		const header = tarball.subarray(offset, offset + 512);
		const name = header.toString('utf8', 0, 100).replaceAll('\0', '');
		if (name === '') {
			return undefined;
		}
		const prefix = header.toString('utf8', 345, 500).replaceAll('\0', '');
		const fullName = prefix ? `${prefix}/${name}` : name;
		const size = Number.parseInt(header.toString('utf8', 124, 136).replaceAll('\0', '').trim() || '0', 8);
		offset += 512;
		if (fullName === filePath) {
			return tarball.subarray(offset, offset + size);
		}
		offset += Math.ceil(size / 512) * 512;
	}
	return undefined;
}
