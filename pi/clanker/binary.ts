// Finds the Go engine. No pi imports, so it can be tested with plain Node.

import { existsSync } from "node:fs";
import { join } from "node:path";

/** Platforms the npm package ships a prebuilt engine for, as `${platform}-${arch}`. */
export const PLATFORMS = ["darwin-arm64", "darwin-x64", "linux-arm64", "linux-x64"];

/** Where the npm package keeps the engine for a platform, relative to its root. */
export function prebuiltPath(platform: string): string {
	return join("bin", platform, "clanker-musician");
}

export type EngineLocation =
	| { kind: "env"; bin: string } // CLANKER_MUSICIAN_BIN
	| { kind: "source"; bin: string } // a Go checkout: build it when stale
	| { kind: "prebuilt"; bin: string } // shipped in the npm package
	| { kind: "missing"; reason: string };

/**
 * Looks in order: the CLANKER_MUSICIAN_BIN override, a Go checkout at root
 * (development and git installs), then the prebuilt engine for this platform.
 */
export function locateEngine(
	root: string,
	env: Record<string, string | undefined>,
	platform: string,
	arch: string,
): EngineLocation {
	if (env.CLANKER_MUSICIAN_BIN) return { kind: "env", bin: env.CLANKER_MUSICIAN_BIN };
	if (existsSync(join(root, "go.mod"))) return { kind: "source", bin: join(root, "clanker-musician") };

	const target = `${platform}-${arch}`;
	if (!PLATFORMS.includes(target)) return { kind: "missing", reason: `no prebuilt engine for ${target}` };
	const bin = join(root, prebuiltPath(target));
	if (!existsSync(bin)) return { kind: "missing", reason: `engine for ${target} is missing; reinstall the package` };
	return { kind: "prebuilt", bin };
}
