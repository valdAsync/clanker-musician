/**
 * clanker-musician for pi: a side column with the night-shift camera feed and
 * a soundtrack that follows the agent.
 *
 *   /clanker-musician          toggle on/off
 *   /clanker-musician mute     silence the music, keep the picture (and unmute)
 *
 * POWER in the column is the context window left, as in pi's footer.
 *
 * The Go engine (`clanker-musician engine`) plays the music and renders the
 * column as text frames. This extension starts it, forwards pi's agent events,
 * and shows the frames.
 *
 * A real split, where the conversation rewraps beside the column, needs
 * fullscreen mode (`"tuiMode": "fullscreen"` or `--tui-mode fullscreen`). pi
 * has no side-panel API, so the split wraps pi's private layout root in an
 * HStack. When that isn't available the column is a right-anchored overlay
 * that covers the edge of the conversation instead.
 */

import { type ChildProcessWithoutNullStreams, execFile, spawn } from "node:child_process";
import { accessSync, chmodSync, constants, existsSync, readdirSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { type Component, HStack, isViewportTUI, type TUI, truncateToWidth } from "@earendil-works/pi-tui";
import { locateEngine } from "./binary.ts";
import { type EngineEvent, type EngineKind, LineBuffer, panelWidth, toolLine } from "./events.ts";

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const MIN_SPLIT_COLUMNS = 100; // below this the column hides and the chat gets the row
const READY_TIMEOUT_MS = 5000;

// --- engine process ---

class Engine {
	lines: string[] = [];
	onFrame: () => void = () => {};
	onExit: (message: string) => void = () => {};
	private proc?: ChildProcessWithoutNullStreams;
	private stderr = "";
	private stopping = false;

	/** Starts the engine and resolves once it reports ready. */
	start(bin: string): Promise<void> {
		return new Promise((resolveStart, rejectStart) => {
			let settled = false;
			const settle = (err?: Error) => {
				if (settled) return;
				settled = true;
				clearTimeout(timer);
				err ? rejectStart(err) : resolveStart();
			};
			const timer = setTimeout(() => settle(new Error("engine did not start in time")), READY_TIMEOUT_MS);

			const proc = spawn(bin, ["engine"], { stdio: ["pipe", "pipe", "pipe"] });
			this.proc = proc;
			proc.stdin.on("error", () => {}); // the engine exiting closes the pipe; exit handles it
			proc.stderr.on("data", (d: Buffer) => {
				this.stderr = (this.stderr + d.toString()).slice(-2000);
			});
			proc.on("error", (err) => settle(err));
			proc.on("exit", (code) => {
				const message = this.stderr.trim().split("\n").pop() || `engine exited (${code})`;
				settle(new Error(message));
				if (!this.stopping) this.onExit(message);
			});

			createInterface({ input: proc.stdout }).on("line", (line) => {
				let msg: { type?: string; lines?: string[]; message?: string };
				try {
					msg = JSON.parse(line);
				} catch {
					return;
				}
				if (msg.type === "ready") settle();
				else if (msg.type === "error") settle(new Error(msg.message ?? "engine error"));
				else if (msg.type === "frame" && msg.lines) {
					this.lines = msg.lines;
					this.onFrame();
				}
			});
		});
	}

	send(msg: object): void {
		if (this.proc?.stdin.writable) this.proc.stdin.write(`${JSON.stringify(msg)}\n`);
	}

	/** Closing stdin makes the engine exit; the kill is a backstop. */
	stop(): void {
		if (!this.proc) return;
		this.stopping = true;
		const proc = this.proc;
		this.proc = undefined;
		proc.stdin.end();
		setTimeout(() => proc.kill(), 1000).unref();
	}
}

// --- the column ---

class Column implements Component {
	private readonly tui: TUI;
	private readonly engine: Engine;
	private lastWidth = 0;
	private lastRows = 0;

	constructor(tui: TUI, engine: Engine) {
		this.tui = tui;
		this.engine = engine;
	}

	render(width: number): string[] {
		const rows = this.tui.terminal.rows;
		if (width !== this.lastWidth || rows !== this.lastRows) {
			this.lastWidth = width;
			this.lastRows = rows;
			this.engine.send({ type: "resize", width, height: rows });
		}
		const out: string[] = [];
		for (let i = 0; i < rows; i++) {
			out.push(truncateToWidth(this.engine.lines[i] ?? "", width, ""));
		}
		return out;
	}

	invalidate(): void {}
}

type ViewportTui = TUI & { setLayoutRoot(c: Component | undefined): void };

interface Active {
	engine: Engine;
	tui: TUI;
	column: Column;
	muted: boolean;
	split?: { tui: ViewportTui; original: Component; columns: number };
	closeOverlay?: () => void;
	text: LineBuffer;
	thinking: LineBuffer;
}

let active: Active | undefined;

// Extensions aren't handed the TUI directly; a widget factory is.
function captureTui(ctx: ExtensionContext): Promise<TUI | undefined> {
	return new Promise((resolveTui) => {
		const key = "clanker-probe";
		const timeout = setTimeout(() => resolveTui(undefined), 1000);
		ctx.ui.setWidget(key, (tui) => {
			clearTimeout(timeout);
			queueMicrotask(() => ctx.ui.setWidget(key, undefined));
			resolveTui(tui);
			return { render: () => [], invalidate: () => {} };
		});
	});
}

/** Wraps pi's fullscreen layout in [conversation | column]. False if unavailable. */
function applySplit(a: Active): boolean {
	const tui = a.tui;
	if (!isViewportTUI(tui)) return false;
	const original = a.split?.original ?? (tui as unknown as { layoutRoot?: Component }).layoutRoot;
	if (!original) return false;
	const columns = tui.terminal.columns;
	const split = new HStack(
		[
			{ component: original, basis: 0, grow: 1, shrink: 1 },
			{
				component: a.column,
				basis: panelWidth(columns),
				grow: 0,
				shrink: 0,
				visible: (viewport) => viewport.width >= MIN_SPLIT_COLUMNS,
			},
		],
		{ gap: 1 },
	);
	(tui as ViewportTui).setLayoutRoot(split);
	a.split = { tui: tui as ViewportTui, original, columns };
	return true;
}

function showOverlay(ctx: ExtensionContext, a: Active): void {
	void ctx.ui.custom<void>(
		(_tui, _theme, _kb, done) => {
			a.closeOverlay = () => done();
			return a.column;
		},
		{
			overlay: true,
			overlayOptions: () => ({
				anchor: "right-center",
				width: panelWidth(a.tui.terminal.columns),
				maxHeight: "100%",
				nonCapturing: true,
				visible: (w) => w >= MIN_SPLIT_COLUMNS,
			}),
		},
	);
}

function teardown(): void {
	const a = active;
	if (!a) return;
	active = undefined;
	if (a.split) {
		a.split.tui.setLayoutRoot(a.split.original);
		a.split.tui.requestRender();
	}
	a.closeOverlay?.();
	a.engine.stop();
}

// --- engine binary ---

function newestGoSource(root: string): number {
	let newest = 0;
	for (const name of readdirSync(root)) {
		if (name.endsWith(".go") || name === "go.mod" || name === "go.sum") {
			newest = Math.max(newest, statSync(join(root, name)).mtimeMs);
		}
	}
	return newest;
}

/**
 * Finds the engine: the prebuilt one shipped for this platform, or this repo's
 * Go source, built when it's missing or stale.
 */
async function engineBinary(ctx: ExtensionContext): Promise<string> {
	const loc = locateEngine(REPO_ROOT, process.env, process.platform, process.arch);
	switch (loc.kind) {
		case "missing":
			throw new Error(loc.reason);
		case "env":
			return loc.bin;
		case "prebuilt":
			try {
				accessSync(loc.bin, constants.X_OK);
			} catch {
				chmodSync(loc.bin, 0o755); // some installs drop the executable bit
			}
			return loc.bin;
	}

	const bin = loc.bin;
	if (existsSync(bin) && statSync(bin).mtimeMs >= newestGoSource(REPO_ROOT)) return bin;

	ctx.ui.notify("clanker-musician: building the engine…", "info");
	await new Promise<void>((resolveBuild, rejectBuild) => {
		execFile("go", ["build", "-o", bin, "."], { cwd: REPO_ROOT }, (err, _out, stderr) => {
			if (err) rejectBuild(new Error(`go build failed: ${stderr.trim() || err.message}`));
			else resolveBuild();
		});
	});
	return bin;
}

// --- on / off ---

async function enable(ctx: ExtensionContext): Promise<void> {
	const engine = new Engine();
	await engine.start(await engineBinary(ctx));

	const tui = await captureTui(ctx);
	if (!tui) {
		engine.stop();
		throw new Error("could not reach pi's TUI");
	}
	const a: Active = {
		engine,
		tui,
		column: new Column(tui, engine),
		muted: false,
		text: new LineBuffer(),
		thinking: new LineBuffer(),
	};
	active = a;

	engine.onFrame = () => {
		if (a.split && a.tui.terminal.columns !== a.split.columns) applySplit(a); // resize: new width
		a.tui.requestRender();
	};
	engine.onExit = (message) => {
		if (active !== a) return;
		teardown();
		ctx.ui.notify(`clanker-musician stopped: ${message}`, "error");
	};

	sendPower(ctx);
	if (applySplit(a)) {
		ctx.ui.notify("clanker-musician on", "info");
	} else {
		showOverlay(ctx, a);
		ctx.ui.notify("clanker-musician on (overlay; run pi with --tui-mode fullscreen for a real split)", "info");
	}
	a.tui.requestRender();
}

/** POWER is the context window left: 100% minus pi's context usage. */
function sendPower(ctx: ExtensionContext): void {
	const percent = ctx.getContextUsage()?.percent;
	if (percent != null) active?.engine.send({ type: "power", percent: 100 - percent });
}

function send(kind: EngineKind, text = ""): void {
	const event: EngineEvent = { type: "event", kind, text };
	active?.engine.send(event);
}

export default function (pi: ExtensionAPI) {
	pi.registerCommand("clanker-musician", {
		description: "Toggle the clanker-musician column (add mute/unmute to silence it)",
		handler: async (args, ctx) => {
			const arg = args.trim().toLowerCase();
			if (arg === "mute" || arg === "unmute") {
				if (!active) {
					ctx.ui.notify("clanker-musician is off", "info");
					return;
				}
				active.muted = arg === "mute";
				active.engine.send({ type: "mute", on: active.muted });
				ctx.ui.notify(active.muted ? "clanker-musician muted" : "clanker-musician unmuted", "info");
				return;
			}
			if (active) {
				teardown();
				ctx.ui.notify("clanker-musician off", "info");
				return;
			}
			if (ctx.mode !== "tui") {
				ctx.ui.notify("clanker-musician needs the interactive TUI", "error");
				return;
			}
			try {
				await enable(ctx);
			} catch (err) {
				teardown();
				ctx.ui.notify(`clanker-musician: ${err instanceof Error ? err.message : String(err)}`, "error");
			}
		},
	});

	pi.on("agent_start", () => send("thinking", "prompt"));

	pi.on("message_update", (event) => {
		if (!active) return;
		const e = event.assistantMessageEvent;
		switch (e.type) {
			case "text_delta":
				for (const line of active.text.push(e.delta)) send("output", line);
				break;
			case "thinking_delta":
				for (const line of active.thinking.push(e.delta)) send("thinking", line);
				break;
			case "text_end":
				for (const line of active.text.flush()) send("output", line);
				break;
			case "thinking_end":
				for (const line of active.thinking.flush()) send("thinking", line);
				break;
		}
	});

	pi.on("tool_execution_start", (event) => send("tool", toolLine(event.toolName, event.args)));

	pi.on("tool_execution_end", (event) => {
		if (event.isError) send("error", `error: ${event.toolName}`);
	});

	pi.on("agent_settled", (_event, ctx) => {
		send("idle");
		sendPower(ctx);
	});

	// Context usage changes after each response and drops after a compaction.
	pi.on("message_end", (_event, ctx) => sendPower(ctx));
	pi.on("session_compact", (_event, ctx) => sendPower(ctx));

	pi.on("session_shutdown", () => teardown());
}
