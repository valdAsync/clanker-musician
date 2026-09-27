// Builds the npm package for a release into dist/npm/package/: the extension
// plus a prebuilt engine for every platform in bin/<platform>-<arch>/. The
// extension picks the right one at run time.
//
// The engine is pure Go (the audio library loads the system sound library at
// run time), so every platform cross-compiles here with CGO_ENABLED=0.
//
//   node --experimental-strip-types scripts/npm-build.mjs [vX.Y.Z]
//
// With a tag, the build fails unless it matches package.json's version.

import { execFileSync } from "node:child_process";
import { copyFileSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { PLATFORMS, prebuiltPath } from "../pi/clanker/binary.ts";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const out = join(root, "dist", "npm", "package");
const pkg = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));

const tag = process.argv[2];
if (tag && tag !== `v${pkg.version}`) {
	console.error(`tag ${tag} does not match package.json version ${pkg.version}`);
	process.exit(1);
}

const GOARCH = { x64: "amd64", arm64: "arm64" };

rmSync(join(root, "dist", "npm"), { recursive: true, force: true });

for (const file of [...pkg.files.filter((f) => f !== "bin"), "package.json"]) {
	mkdirSync(dirname(join(out, file)), { recursive: true });
	copyFileSync(join(root, file), join(out, file));
}

for (const platform of PLATFORMS) {
	const [os, cpu] = platform.split("-");
	console.log(`building ${platform}`);
	execFileSync("go", ["build", "-trimpath", "-ldflags", "-s -w", "-o", join(out, prebuiltPath(platform)), "."], {
		cwd: root,
		stdio: "inherit",
		env: { ...process.env, GOOS: os, GOARCH: GOARCH[cpu], CGO_ENABLED: "0" },
	});
}
console.log(`built ${pkg.name}@${pkg.version} in ${out}`);
