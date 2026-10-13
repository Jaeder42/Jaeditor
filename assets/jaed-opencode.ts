// jaed live link for opencode — V2 plugin API (opencode >= 2.0).
//
// jaed continuously publishes the active buffer's absolute path to
// $TMPDIR/jaed-active-<sha256(workspace)[:16]>. This plugin injects it
// as a system-prompt line on every model call (fresh each time,
// invisible in the transcript) and registers a jaed_active_file tool the
// model can call anytime. Inert when opencode runs outside jaed.
//
// It also exposes the workspace's agent-authored tools: JavaScript
// scripts the model writes into .jaed/tools and runs in a sandboxed Node
// process (permission model + module allowlist; see assets/jaed-tools).
//
// Install (manual, one time):
//   cp assets/jaed-opencode.ts ~/.config/opencode/plugins/jaed.ts
//   cp assets/jaed-tools/SKILL.md ~/.config/opencode/skills/jaed-tools/SKILL.md
//
// Written for a local plugin file: opencode installs no dependencies for
// local plugins, so there are no @opencode/plugin imports here — the
// default export is the plain V2 definition object.

import { createHash } from "node:crypto"
import { spawn } from "node:child_process"
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

// --- .jaed tools -----------------------------------------------------------

/** The personal, never-committed .jaed folder for a workspace. */
function jaedRoot(dir: string): string {
  return path.join(dir, ".jaed")
}

/** Where agent-authored tools live. */
function toolsDir(dir: string): string {
  return path.join(jaedRoot(dir), "tools")
}

/** Where agent-authored skills live. */
function skillsDir(dir: string): string {
  return path.join(jaedRoot(dir), "skills")
}

/** Whether a session's workspace is jaed-aware. */
function hasJaed(dir: string): boolean {
  try {
    return fs.statSync(jaedRoot(dir)).isDirectory()
  } catch {
    return false
  }
}

/** The installed sandbox runtime, or null when it is not present. */
function runtimeDir(): string | null {
  const candidates: string[] = []
  if (process.env.JAED_RUNTIME) candidates.push(process.env.JAED_RUNTIME)
  candidates.push(path.join(os.homedir(), ".config", "jaed", "runtime"))
  for (const dir of candidates) {
    if (fs.existsSync(path.join(dir, "run.mjs"))) return dir
  }
  return null
}

/** A tool's name, description, and source path. */
type ToolInfo = { name: string; description: string; path: string }

/** Pull the @description out of a tool's leading block comment. */
function describe(src: string): string {
  const s = src.replace(/^\uFEFF/, "")
  const start = s.indexOf("/**")
  if (start >= 0 && s.slice(0, start).trim() === "") {
    const end = s.indexOf("*/", start + 3)
    if (end >= 0) {
      const value = tagValue(s.slice(start + 3, end), "description")
      if (value) return value
    }
  }
  for (const raw of s.split("\n")) {
    const line = raw.trim()
    if (!line) continue
    if (line.startsWith("//")) return line.slice(2).trim()
    break
  }
  return ""
}

/** Read a JSDoc-style tag's value, joining continuation lines. */
function tagValue(body: string, tag: string): string {
  const prefix = "@" + tag
  const out: string[] = []
  let capturing = false
  for (const raw of body.split("\n")) {
    const line = raw.replace(/^\s*\*?\s?/, "").trim()
    if (!line && !capturing) continue
    const isTag = line.startsWith(prefix) && (line.length === prefix.length || /\s/.test(line[prefix.length]))
    if (isTag) {
      capturing = true
      const value = line.slice(prefix.length).trim()
      if (value) out.push(value)
      continue
    }
    if (capturing) {
      if (line.startsWith("@")) break
      out.push(line)
    }
  }
  return out.join(" ").trim()
}

/** The tools in a workspace, sorted by name. */
function listTools(dir: string): ToolInfo[] {
  const out: ToolInfo[] = []
  let names: string[]
  try {
    names = fs.readdirSync(toolsDir(dir))
  } catch {
    return out
  }
  for (const name of names.sort()) {
    if (!name.endsWith(".js") && !name.endsWith(".mjs")) continue
    const file = path.join(toolsDir(dir), name)
    try {
      const code = fs.readFileSync(file, "utf8")
      out.push({ name: name.replace(/\.m?js$/, ""), description: describe(code), path: file })
    } catch {
      // a tool that vanished mid-scan is skipped
    }
  }
  return out
}

/** The description from a skill's YAML front matter. */
function frontmatterDescription(text: string): string {
  const match = /^---\r?\n([\s\S]*?)\r?\n---/.exec(text)
  if (!match) return ""
  const line = match[1].split(/\r?\n/).find((l) => l.startsWith("description:"))
  if (!line) return ""
  return line.slice("description:".length).trim().replace(/^["']|["']$/g, "")
}

/** The skills in a workspace (name + description from front matter). */
function listSkills(dir: string): { name: string; description: string }[] {
  const out: { name: string; description: string }[] = []
  let names: string[]
  try {
    names = fs.readdirSync(skillsDir(dir))
  } catch {
    return out
  }
  for (const file of names.sort()) {
    if (!file.endsWith(".md")) continue
    try {
      const text = fs.readFileSync(path.join(skillsDir(dir), file), "utf8")
      const stem = file.replace(/\.md$/, "")
      out.push({ name: stem, description: frontmatterDescription(text) })
    } catch {
      // skip
    }
  }
  return out
}

/** Write a tool file, synthesizing a description header when absent. */
function writeTool(dir: string, name: string, description: string, code: string): string {
  if (!/^[A-Za-z0-9._-]+$/.test(name) || name.includes("..")) {
    throw new Error("jaed: tool name may only contain letters, digits, dots, dashes and underscores")
  }
  const stem = name.replace(/\.m?js$/, "")
  if (!stem) throw new Error("jaed: tool name is empty")
  const target = path.join(toolsDir(dir), stem + ".js")
  let src = code
  if (!/@description\b/.test(src)) {
    src = `/**\n * @description ${description || "TODO: describe what this tool does"}\n */\n` + src
  }
  fs.mkdirSync(toolsDir(dir), { recursive: true })
  fs.writeFileSync(target, src)
  return target
}

/** Read a skill's markdown, or null when it does not exist. */
function readSkill(dir: string, name: string): string | null {
  const stem = name.replace(/\.md$/, "")
  try {
    return fs.readFileSync(path.join(skillsDir(dir), stem + ".md"), "utf8")
  } catch {
    return null
  }
}

/** Write a skill with front matter. */
function writeSkill(dir: string, name: string, description: string, content: string): string {
  const stem = name.replace(/\.md$/, "")
  if (!/^[A-Za-z0-9._-]+$/.test(stem) || stem.includes("..")) {
    throw new Error("jaed: skill name may only contain letters, digits, dots, dashes and underscores")
  }
  const target = path.join(skillsDir(dir), stem + ".md")
  const front = `---\nname: ${stem}\ndescription: ${description || "TODO: describe this skill"}\n---\n\n`
  const body = /^---\n/.test(content) ? content : front + content
  fs.mkdirSync(skillsDir(dir), { recursive: true })
  fs.writeFileSync(target, body)
  return target
}

/** The result line the sandbox runtime prints. */
type RunResult = { ok: boolean; output?: string; error?: string; logs?: string[]; durationMs?: number }

/**
 * Run a tool in the sandbox: a Node process under the permission model,
 * reading only the workspace and runtime and writing only .jaed/tmp.
 * The flags live here, not in the runtime file, so the sandbox travels
 * with the plugin.
 */
function runTool(dir: string, name: string, args: unknown, timeoutMs = 30000): Promise<RunResult> {
  const runtime = runtimeDir()
  if (!runtime) {
    return Promise.resolve({
      ok: false,
      error:
        "jaed tools runtime is not installed. Run ./install.sh, or set JAED_RUNTIME to the assets/jaed-tools directory.",
    })
  }
  const workspace = canonical(dir)
  const argv = [
    "--permission",
    `--allow-fs-read=${workspace}`,
    `--allow-fs-read=${runtime}`,
    `--allow-fs-write=${path.join(workspace, ".jaed", "tmp")}`,
    "--max-old-space-size=256",
    path.join(runtime, "run.mjs"),
    name,
    JSON.stringify(args ?? {}),
  ]
  // The active-file link, when jaed has published one, so tools can read
  // "the file the user is editing".
  const link = linkPath(workspace)
  if (fs.existsSync(link)) argv.splice(3, 0, `--allow-fs-read=${link}`)

  return new Promise((resolve) => {
    let child
    try {
      child = spawn("node", argv, {
        cwd: workspace,
        env: { ...process.env, JAED_RUNTIME: runtime },
        stdio: ["ignore", "pipe", "pipe"],
      })
    } catch (err) {
      resolve({ ok: false, error: String(err) })
      return
    }

    let stdout = ""
    let stderr = ""
    let timedOut = false
    const timer = setTimeout(() => {
      timedOut = true
      try {
        child.kill("SIGKILL")
      } catch {
        // already gone
      }
    }, timeoutMs)

    child.stdout.on("data", (chunk) => (stdout += chunk))
    child.stderr.on("data", (chunk) => (stderr += chunk))
    child.on("error", (err) => {
      clearTimeout(timer)
      resolve({ ok: false, error: String(err) })
    })
    child.on("close", (code) => {
      clearTimeout(timer)
      if (timedOut) {
        resolve({ ok: false, error: `jaed: tool "${name}" exceeded ${timeoutMs / 1000}s and was stopped` })
        return
      }
      const line = stdout.trim().split("\n").pop() || ""
      try {
        resolve(JSON.parse(line) as RunResult)
      } catch {
        resolve({ ok: false, error: `jaed tools runtime exited ${code}\n${(stderr || stdout).trim()}` })
      }
    })
  })
}

/** Format a run result as the text a tool call returns. */
function formatRun(name: string, result: RunResult): string {
  const logs = result.logs && result.logs.length ? `\n--- logs ---\n${result.logs.join("\n")}` : ""
  if (!result.ok) return `tool ${name} failed: ${result.error}${logs}`
  const output = result.output === undefined || result.output === "" ? "(no output)" : result.output
  return `${output}${logs}`
}

// --- plugin ----------------------------------------------------------------

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
      const dir = await sessionDirectory(event.sessionID)
      const active = readActiveFile(dir)
      if (active) {
        event.system.push({
          type: "text",
          text:
            `[jaed] The user is currently editing ${active.path} in the jaed editor pane. ` +
            `Treat it as the primary context for phrases like "this file", ` +
            `"what I'm working on", or "the code" — read it with file tools when relevant.`,
        })
      }
      // Advertise the workspace's own tools so the model knows to reuse
      // them instead of writing a fresh script.
      if (hasJaed(dir)) {
        const tools = listTools(dir)
        const skills = listSkills(dir)
        if (tools.length) {
          const names = tools.map((t) => (t.description ? `${t.name} (${t.description})` : t.name)).join("; ")
          event.system.push({
            type: "text",
            text: `[jaed] Workspace tools (run with jaed_tools_run): ${names}`,
          })
        }
        if (skills.length) {
          event.system.push({
            type: "text",
            text: `[jaed] Workspace skills (read with jaed_skills_read): ${skills.map((s) => s.name).join(", ")}`,
          })
        }
      }
    })

    // Tools the model can call.
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

      editor.add({
        name: "jaed_tools_list",
        description:
          "List the reusable JavaScript tools already written in this workspace " +
          "(.jaed/tools), with their descriptions. Check this before writing a new tool.",
        input: { type: "object", properties: {}, additionalProperties: false },
        execute: async (_input, context) => {
          const dir = await sessionDirectory(context && context.sessionID)
          const tools = listTools(dir)
          if (!tools.length) {
            return { content: "No tools yet. Write one with jaed_tools_write." }
          }
          const lines = tools.map((t) => `- ${t.name}: ${t.description || "(no description)"}\n  ${t.path}`)
          return { content: lines.join("\n") }
        },
      })

      editor.add({
        name: "jaed_tools_write",
        description:
          "Write a reusable JavaScript tool into the workspace's .jaed/tools folder. " +
          "The tool is an ESM file exporting `default async function run(input, jaed)`. " +
          "Import the helper with `import jaed from \"jaed\"`. It runs sandboxed (no " +
          "network or processes; reads the workspace; writes only .jaed/tmp) with a " +
          "small package allowlist (zod, yaml, date-fns, nanoid, marked, lodash-es). " +
          "Provide a concise `description`; it becomes the @description the tools " +
          "view and jaed_tools_list show.",
        input: {
          type: "object",
          properties: {
            name: { type: "string", description: "Tool name, e.g. list-todos (a .js file is created)." },
            description: { type: "string", description: "One-line description of what the tool does." },
            code: { type: "string", description: "Full ESM source of the tool." },
          },
          required: ["name", "code"],
          additionalProperties: false,
        },
        execute: async (input, context) => {
          const dir = await sessionDirectory(context && context.sessionID)
          try {
            const file = writeTool(dir, input.name, input.description || "", input.code)
            return { content: `Wrote ${file}. Run it with jaed_tools_run { "name": "${input.name.replace(/\.m?js$/, "")}" }.` }
          } catch (err) {
            return { content: `Could not write tool: ${String(err)}` }
          }
        },
      })

      editor.add({
        name: "jaed_tools_run",
        description:
          "Run a tool from the workspace's .jaed/tools folder in the sandbox and " +
          "return its output and logs. `args` is passed to the tool as its input object.",
        input: {
          type: "object",
          properties: {
            name: { type: "string", description: "Tool name, e.g. list-todos (no .js)." },
            args: {
              type: "object",
              description: "Arguments passed to the tool's run(input, jaed).",
              additionalProperties: true,
            },
          },
          required: ["name"],
          additionalProperties: false,
        },
        execute: async (input, context) => {
          const dir = await sessionDirectory(context && context.sessionID)
          const result = await runTool(dir, input.name, input.args)
          return { content: formatRun(input.name, result) }
        },
      })

      editor.add({
        name: "jaed_skills_write",
        description:
          "Write a markdown skill into the workspace's .jaed/skills folder for longer " +
          "reference material. A name/description front matter block is added when absent.",
        input: {
          type: "object",
          properties: {
            name: { type: "string", description: "Skill name, e.g. release-checklist." },
            description: { type: "string", description: "When to use this skill." },
            content: { type: "string", description: "Markdown body of the skill." },
          },
          required: ["name", "content"],
          additionalProperties: false,
        },
        execute: async (input, context) => {
          const dir = await sessionDirectory(context && context.sessionID)
          try {
            const file = writeSkill(dir, input.name, input.description || "", input.content)
            return { content: `Wrote ${file}.` }
          } catch (err) {
            return { content: `Could not write skill: ${String(err)}` }
          }
        },
      })

      editor.add({
        name: "jaed_skills_read",
        description: "Read a markdown skill from the workspace's .jaed/skills folder.",
        input: {
          type: "object",
          properties: {
            name: { type: "string", description: "Skill name, e.g. release-checklist (no .md)." },
          },
          required: ["name"],
          additionalProperties: false,
        },
        execute: async (input, context) => {
          const dir = await sessionDirectory(context && context.sessionID)
          const text = readSkill(dir, input.name)
          return { content: text === null ? `No skill "${input.name}" in ${skillsDir(dir)}.` : text }
        },
      })
    })
  },
}
