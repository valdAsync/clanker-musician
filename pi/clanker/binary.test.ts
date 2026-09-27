// Run: node --test --experimental-strip-types pi/clanker/binary.test.ts
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { locateEngine, prebuiltPath } from "./binary.ts";

test("CLANKER_MUSICIAN_BIN wins over everything", () => {
	const loc = locateEngine("/nowhere", { CLANKER_MUSICIAN_BIN: "/opt/cm" }, "darwin", "arm64");
	assert.deepEqual(loc, { kind: "env", bin: "/opt/cm" });
});

test("a Go checkout builds from source", () => {
	const root = mkdtempSync(join(tmpdir(), "cm-src-"));
	writeFileSync(join(root, "go.mod"), "module x\n");
	assert.deepEqual(locateEngine(root, {}, "linux", "x64"), { kind: "source", bin: join(root, "clanker-musician") });
});

test("an npm install uses the engine shipped for its platform", () => {
	const root = mkdtempSync(join(tmpdir(), "cm-pkg-"));
	const bin = join(root, prebuiltPath("darwin-arm64"));
	mkdirSync(dirname(bin), { recursive: true });
	writeFileSync(bin, "");
	assert.deepEqual(locateEngine(root, {}, "darwin", "arm64"), { kind: "prebuilt", bin });
	assert.equal(locateEngine(root, {}, "linux", "x64").kind, "missing");
});

test("an unsupported platform explains itself", () => {
	const loc = locateEngine("/pkg", {}, "win32", "x64");
	assert.equal(loc.kind, "missing");
	assert.match((loc as { reason: string }).reason, /win32-x64/);
});
