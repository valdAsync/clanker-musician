// Pure helpers that turn pi's agent events into engine event lines. No pi
// imports, so they can be tested with plain Node.

export type EngineKind = "output" | "thinking" | "tool" | "error" | "idle";

export interface EngineEvent {
	type: "event";
	kind: EngineKind;
	text: string;
}

const MAX_LINE = 200;

function clean(text: string, max: number): string {
	const s = text.replace(/\s+/g, " ").trim();
	return s.length > max ? `${s.slice(0, max - 1)}…` : s;
}

/**
 * LineBuffer collects streamed deltas and hands back whole lines, since the
 * engine turns each line into one note.
 */
export class LineBuffer {
	private pending = "";

	/** Adds a delta and returns the lines it completed. */
	push(delta: string): string[] {
		this.pending += delta;
		const parts = this.pending.split("\n");
		this.pending = parts.pop() ?? "";
		return parts.map((l) => clean(l, MAX_LINE)).filter((l) => l !== "");
	}

	/** Returns whatever is left once the stream ends. */
	flush(): string[] {
		const rest = clean(this.pending, MAX_LINE);
		this.pending = "";
		return rest === "" ? [] : [rest];
	}
}

// The argument that says the most about a tool call, in order of preference.
const ARG_KEYS = ["path", "file_path", "command", "pattern", "query", "url", "prompt"];

/** Summarises a tool call for the log and the melody: "tool: edit main.go". */
export function toolLine(toolName: string, args: unknown): string {
	let detail = "";
	if (args && typeof args === "object") {
		const rec = args as Record<string, unknown>;
		for (const key of ARG_KEYS) {
			if (typeof rec[key] === "string" && rec[key] !== "") {
				detail = rec[key] as string;
				break;
			}
		}
	}
	return clean(`tool: ${toolName} ${detail}`, 80);
}

/** Panel width for a terminal: about a third of it, within 44–70 columns. */
export function panelWidth(columns: number): number {
	return Math.max(44, Math.min(70, Math.round(columns * 0.35)));
}
