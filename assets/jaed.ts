// jaed live link — makes pi passively aware of what the user is editing
// in the jaed workspace editor pane.
//
// jaed continuously publishes the active buffer's absolute path to
// $TMPDIR/jaed-active-<sha256(cwd)[:16]>. This extension reads it on
// every prompt, injects it as a live system-prompt section, and exposes
// a tool the model can call anytime. It is inert when pi runs outside
// jaed (no link file, no notifications).
//
// Install (manual, one time):
//   cp assets/jaed.ts ~/.pi/agent/extensions/jaed.ts

import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { createHash } from "node:crypto";
import { Type } from "typebox";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

/** The live-link file for the current workspace, matching jaed's path. */
function linkPath(): string {
  const hash = createHash("sha256").update(process.cwd()).digest("hex").slice(0, 16);
  return path.join(os.tmpdir(), `jaed-active-${hash}`);
}

/** The file the user is currently editing, or null when there is none. */
function readActiveFile(): { path: string; mtimeMs: number } | null {
  try {
    const active = fs.readFileSync(linkPath(), "utf8").trim();
    if (!path.isAbsolute(active) || !fs.existsSync(active)) return null;
    return { path: active, mtimeMs: fs.statSync(active).mtimeMs };
  } catch {
    return null;
  }
}

export default function (pi: ExtensionAPI) {
  // Announce the link when a session starts inside jaed.
  pi.on("session_start", (_event, ctx) => {
    const active = readActiveFile();
    if (active) {
      ctx.ui.notify(`jaed link: editing ${active.path}`, "info");
    }
  });

  // Inject the active file as a live system-prompt section on every
  // prompt; remove the section when no file is open.
  pi.on("before_agent_start", (event) => {
    const active = readActiveFile();
    if (active) {
      event.systemPromptOptions.sections.jaed =
        `The user is currently editing ${active.path} in the jaed editor pane. ` +
        `Treat it as the primary context for phrases like "this file", ` +
        `"what I'm working on", or "the code" — read it with file tools when relevant.`;
    } else {
      delete event.systemPromptOptions.sections.jaed;
    }
  });

  // A tool the model can call to check the live state, including
  // freshness (the file's modification time).
  pi.registerTool({
    name: "jaed_active_file",
    label: "jaed active file",
    description:
      "Report the file the user is currently editing in the jaed editor " +
      "pane, with its last modification time. Use it when context refers " +
      'to "this file" or "what I\'m editing".',
    promptSnippet:
      "jaed_active_file: report which file the user is editing right now.",
    parameters: Type.Object({}),
    async execute(_toolCallId, _params, _signal, _onUpdate, _ctx) {
      const active = readActiveFile();
      if (!active) {
        return {
          content: [
            {
              type: "text",
              text: "No jaed link: no file is open in the editor pane, or pi is not running inside jaed.",
            },
          ],
        };
      }
      const age = Math.round((Date.now() - active.mtimeMs) / 1000);
      return {
        content: [
          {
            type: "text",
            text: `Currently editing: ${active.path} (last modified ${age}s ago)`,
          },
        ],
      };
    },
  });
}