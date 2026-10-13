// jaed live link for opencode — V2 plugin API (opencode >= 2.0).
//
// jaed continuously publishes the active buffer's absolute path to
// $TMPDIR/jaed-active-<sha256(workspace)[:16]>. This plugin injects it
// as a system-prompt line on every model call (fresh each time,
// invisible in the transcript) and registers a jaed_active_file tool the
// model can call anytime. Inert when opencode runs outside jaed.
//
// Install (manual, one time):
//   cp assets/jaed-opencode.ts ~/.config/opencode/plugins/jaed.ts
//
// Written for a local plugin file: opencode installs no dependencies
// for local plugins, so there are no @opencode/plugin imports here —
// the default export is the plain V2 definition object.

import { createHash } from "node:crypto"
import * as fs from "node:fs"
import * as os from "node:os"
import * as path from "node:path"

/** The live-link file for a workspace directory, matching jaed's path. */
function linkPath(directory: string): string {
  const hash = createHash("sha256").update(directory).digest("hex").slice(0, 16)
  return path.join(os.tmpdir(), `jaed-active-${hash}`)
}

/** Resolve to the canonical form jaed hashes (it uses EvalSymlinks). */
function canonical(dir: string): string {
  try {
    return fs.realpathSync(dir)
  } catch {
    return dir
  }
}

/** The file the user is currently editing, or null when there is none. */
function readActiveFile(directory: string): { path: string; mtimeMs: number } | null {
  try {
    const active = fs.readFileSync(linkPath(canonical(directory)), "utf8").trim()
    if (!path.isAbsolute(active) || !fs.existsSync(active)) return null
    return { path: active, mtimeMs: fs.statSync(active).mtimeMs }
  } catch {
    return null
  }
}

export default {
  id: "jaed",

  async setup(ctx) {
    // Fallback only: the plugin instance's own location. Sessions resolve
    // their true workspace per call below — a shared background service
    // serves many directories, and its own location can be any of them.
    const fallback: string =
      (ctx.location && ctx.location.directory) || process.cwd()

    // Sessions never move; cache their directory per sessionID.
    const sessionDirs = new Map<string, string>()
    const sessionDirectory = async (sessionID: string | undefined): Promise<string> => {
      if (!sessionID) return fallback
      const cached = sessionDirs.get(sessionID)
      if (cached) return cached
      try {
        const session = await ctx.session.get({ sessionID })
        const dir =
          (session && ((session.location && session.location.directory) || session.directory)) ||
          fallback
        if (sessionDirs.size > 500) sessionDirs.clear()
        sessionDirs.set(sessionID, dir)
        return dir
      } catch {
        return fallback
      }
    }

    // Live context: injected as a system line on every model call, so it
    // is always fresh and never pollutes the user's transcript.
    await ctx.session.hook("context", async (event) => {
      const active = readActiveFile(await sessionDirectory(event.sessionID))
      if (!active) return
      event.system.push({
        type: "text",
        text:
          `[jaed] The user is currently editing ${active.path} in the jaed editor pane. ` +
          `Treat it as the primary context for phrases like "this file", ` +
          `"what I'm working on", or "the code" — read it with file tools when relevant.`,
      })
    })

    // A tool the model can call to check the live state, including
    // freshness (the file's modification time).
    await ctx.tool.transform((editor) => {
      editor.add({
        name: "jaed_active_file",
        description:
          "Report the file the user is currently editing in the jaed editor " +
          "pane, with its last modification time. Use it when context refers " +
          'to "this file" or "what I\'m editing".',
        input: {
          type: "object",
          properties: {},
          additionalProperties: false,
        },
        execute: async (_input, context) => {
          const dir = await sessionDirectory(context && context.sessionID)
          const active = readActiveFile(dir)
          if (!active) {
            return {
              content:
                "No jaed link: no file is open in the editor pane, or opencode is not running inside jaed.",
            }
          }
          const age = Math.round((Date.now() - active.mtimeMs) / 1000)
          return {
            content: `Currently editing: ${active.path} (last modified ${age}s ago)`,
          }
        },
      })
    })
  },
}