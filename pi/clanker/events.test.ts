// Run: node --test --experimental-strip-types pi/clanker/events.test.ts
import assert from "node:assert/strict";
import { test } from "node:test";
import { LineBuffer, panelWidth, toolLine } from "./events.ts";

test("LineBuffer returns whole lines across deltas", () => {
	const b = new LineBuffer();
	assert.deepEqual(b.push("Hello wo"), []);
	assert.deepEqual(b.push("rld\nsecond "), ["Hello world"]);
	assert.deepEqual(b.push("line\n\n\nthird"), ["second line"]);
	assert.deepEqual(b.flush(), ["third"]);
	assert.deepEqual(b.flush(), []);
});

test("LineBuffer collapses whitespace and caps long lines", () => {
	const b = new LineBuffer();
	const [line] = b.push(`${"x ".repeat(300)}\n`);
	assert.ok(line.length <= 200);
	assert.ok(line.endsWith("…"));
});

test("toolLine picks the most telling argument", () => {
	assert.equal(toolLine("edit", { path: "main.go", oldText: "a" }), "tool: edit main.go");
	assert.equal(toolLine("bash", { command: "go   test\n./..." }), "tool: bash go test ./...");
	assert.equal(toolLine("todo", undefined), "tool: todo");
	assert.ok(toolLine("bash", { command: "y".repeat(500) }).length <= 80);
});

test("panelWidth stays within 44–70", () => {
	assert.equal(panelWidth(80), 44);
	assert.equal(panelWidth(160), 56);
	assert.equal(panelWidth(400), 70);
});
