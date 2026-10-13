// jaed live link — makes pi passively aware of what the user is editing
// in the jaed workspace editor pane.
//
// jaed continuously publishes the active buffer's absolute path to
// $TMPDIR/jaed-active-<sha256(cwd)[:16]>. This extension reads it on
// every prompt, injects it as a live system-prompt section, and exposes
// a tool the model can call anytime. It is inert when pi runs outside
// jaed (no link file, no notifications).
//
// It also exposes the workspace's agent-authored tools: JavaScript
// scripts the model writes into .jaed/tools and runs in a sandboxed Node
// process (permission model + module allowlist; see assets/jaed-tools).
//
// Install (manual, one time):
//   cp assets/jaed.ts ~/.pi/agent/extensions/jaed.ts

import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { spawn } from "node:child_process";
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

// --- .jaed tools -----------------------------------------------------------

/** The personal, never-committed .jaed folder for the workspace. */
function jaedRoot(): string {
  return path.join(process.cwd(), ".jaed");
}

/** Where agent-authored tools live. */
function toolsDir(): string {
  return path.join(jaedRoot(), "tools");
}

/** Where agent-authored skills live. */
function skillsDir(): string {
  return path.join(jaedRoot(), "skills");
}

/** The installed sandbox runtime, or null when it is not present. */
function runtimeDir(): string | null {
  const candidates: string[] = [];
  if (process.env.JAED_RUNTIME) candidates.push(process.env.JAED_RUNTIME);
  candidates.push(path.join(os.homedir(), ".config", "jaed", "runtime"));
  for (const dir of candidates) {
    if (fs.existsSync(path.join(dir, "run.mjs"))) return dir;
  }
  return null;
}

/** A tool's name, description, and source path. */
type ToolInfo = { name: string; description: string; path: string };

/** Pull the @description out of a tool's leading block comment. */
function describe(src: string): string {
  const s = src.replace(/^\uFEFF/, "");
  const start = s.indexOf("/**");
  if (start >= 0 && s.slice(0, start).trim() === "") {
    const end = s.indexOf("*/", start + 3);
    if (end >= 0) {
      const value = tagValue(s.slice(start + 3, end), "description");
      if (value) return value;
    }
  }
  for (const raw of s.split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    if (line.startsWith("//")) return line.slice(2).trim();
    break;
  }
  return "";
}

/** Read a JSDoc-style tag's value, joining continuation lines. */
function tagValue(body: string, tag: string): string {
  const prefix = "@" + tag;
  const out: string[] = [];
  let capturing = false;
  for (const raw of body.split("\n")) {
    const line = raw.replace(/^\s*\*?\s?/, "").trim();
    if (!line && !capturing) continue;
    const isTag = line.startsWith(prefix) && (line.length === prefix.length || /\s/.test(line[prefix.length]));
    if (isTag) {
      capturing = true;
      const value = line.slice(prefix.length).trim();
      if (value) out.push(value);
      continue;
    }
    if (capturing) {
      if (line.startsWith("@")) break;
      out.push(line);
    }
  }
  return out.join(" ").trim();
}

/** The tools in the workspace, sorted by name. */
function listTools(): ToolInfo[] {
  const out: ToolInfo[] = [];
  let names: string[];
  try {
    names = fs.readdirSync(toolsDir());
  } catch {
    return out;
  }
  for (const name of names.sort()) {
    if (!name.endsWith(".js") && !name.endsWith(".mjs")) continue;
    const file = path.join(toolsDir(), name);
    try {
      const code = fs.readFileSync(file, "utf8");
      out.push({ name: name.replace(/\.m?js$/, ""), description: describe(code), path: file });
    } catch {
      // a tool that vanished mid-scan is skipped
    }
  }
  return out;
}

/** Write a tool file, synthesizing a description header when absent. */
function writeTool(name: string, description: string, code: string): string {
  if (!/^[A-Za-z0-9._-]+$/.test(name) || name.includes("..")) {
    throw new Error("jaed: tool name may only contain letters, digits, dots, dashes and underscores");
  }
  const stem = name.replace(/\.m?js$/, "");
  if (!stem) throw new Error("jaed: tool name is empty");
  const target = path.join(toolsDir(), stem + ".js");
  let src = code;
  if (!/@description\b/.test(src)) {
    src = `/**\n * @description ${description || "TODO: describe what this tool does"}\n */\n` + src;
  }
  fs.mkdirSync(toolsDir(), { recursive: true });
  fs.writeFileSync(target, src);
  return target;
}

/** Read a skill's markdown, or null when it does not exist. */
function readSkill(name: string): string | null {
  try {
    return fs.readFileSync(path.join(skillsDir(), name.replace(/\.md$/, "") + ".md"), "utf8");
  } catch {
    return null;
  }
}

/** Write a skill with front matter. */
function writeSkill(name: string, description: string, content: string): string {
  const stem = name.replace(/\.md$/, "");
  if (!/^[A-Za-z0-9._-]+$/.test(stem) || stem.includes("..")) {
    throw new Error("jaed: skill name may only contain letters, digits, dots, dashes and underscores");
  }
  const target = path.join(skillsDir(), stem + ".md");
  const front = `---\nname: ${stem}\ndescription: ${description || "TODO: describe this skill"}\n---\n\n`;
  const body = /^---\n/.test(content) ? content : front + content;
  fs.mkdirSync(skillsDir(), { recursive: true });
  fs.writeFileSync(target, body);
  return target;
}

/** The result line the sandbox runtime prints. */
type RunResult = { ok: boolean; output?: string; error?: string; logs?: string[]; durationMs?: number };

/**
 * Run a tool in the sandbox: a Node process under the permission model,
 * reading only the workspace and runtime and writing only .jaed/tmp. The
 * flags live here, not in the runtime file, so the sandbox travels with
 * the extension.
 */
function runTool(name: string, args: unknown, timeoutMs = 30000): Promise<RunResult> {
  const runtime = runtimeDir();
  if (!runtime) {
    return Promise.resolve({
      ok: false,
      error:
        "jaed tools runtime is not installed. Run ./install.sh, or set JAED_RUNTIME to the assets/jaed-tools directory.",
    });
  }
  let workspace = process.cwd();
  try {
    workspace = fs.realpathSync(workspace);
  } catch {
    // keep the raw path
  }
  const argv = [
    "--permission",
    `--allow-fs-read=${workspace}`,
    `--allow-fs-read=${runtime}`,
    `--allow-fs-write=${path.join(workspace, ".jaed", "tmp")}`,
    "--max-old-space-size=256",
    path.join(runtime, "run.mjs"),
    name,
    JSON.stringify(args ?? {}),
  ];
  const link = linkPath();
  if (fs.existsSync(link)) argv.splice(3, 0, `--allow-fs-read=${link}`);

  return new Promise((resolve) => {
    let child;
    try {
      child = spawn("node", argv, {
        cwd: workspace,
        env: { ...process.env, JAED_RUNTIME: runtime },
        stdio: ["ignore", "pipe", "pipe"],
      });
    } catch (err) {
      resolve({ ok: false, error: String(err) });
      return;
    }

    let stdout = "";
    let stderr = "";
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      try {
        child.kill("SIGKILL");
      } catch {
        // already gone
      }
    }, timeoutMs);

    child.stdout.on("data", (chunk) => (stdout += chunk));
    child.stderr.on("data", (chunk) => (stderr += chunk));
    child.on("error", (err) => {
      clearTimeout(timer);
      resolve({ ok: false, error: String(err) });
    });
    child.on("close", (code) => {
      clearTimeout(timer);
      if (timedOut) {
        resolve({ ok: false, error: `jaed: tool "${name}" exceeded ${timeoutMs / 1000}s and was stopped` });
        return;
      }
      const line = stdout.trim().split("\n").pop() || "";
      try {
        resolve(JSON.parse(line) as RunResult);
      } catch {
        resolve({ ok: false, error: `jaed tools runtime exited ${code}\n${(stderr || stdout).trim()}` });
      }
    });
  });
}

/** Format a run result as the text a tool call returns. */
function formatRun(name: string, result: RunResult): string {
  const logs = result.logs && result.logs.length ? `\n--- logs ---\n${result.logs.join("\n")}` : "";
  if (!result.ok) return `tool ${name} failed: ${result.error}${logs}`;
  const output = result.output === undefined || result.output === "" ? "(no output)" : result.output;
  return `${output}${logs}`;
}

/** The tool guidance the workspace ships, injected as a prompt section. */
function toolGuidance(): string | null {
  const runtime = runtimeDir();
  if (!runtime) return null;
  try {
    return fs.readFileSync(path.join(runtime, "SKILL.md"), "utf8");
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
  // prompt; remove the section when no file is open. Also inject the
  // workspace's tool conventions so the model knows they exist.
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

    if (fs.existsSync(jaedRoot())) {
      const guidance = toolGuidance();
      if (guidance) {
        event.systemPromptOptions.sections.jaedTools = guidance;
      }
      const tools = listTools();
      if (tools.length) {
        event.systemPromptOptions.sections.jaedToolsAvailable =
          `Workspace tools (run with jaed_tools_run): ` +
          tools.map((t) => (t.description ? `${t.name} (${t.description})` : t.name)).join("; ");
      }
    } else {
      delete event.systemPromptOptions.sections.jaedTools;
      delete event.systemPromptOptions.sections.jaedToolsAvailable;
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

  pi.registerTool({
    name: "jaed_tools_list",
    label: "jaed tools list",
    description:
      "List the reusable JavaScript tools already written in this workspace " +
      "(.jaed/tools), with their descriptions. Check this before writing a new tool.",
    promptSnippet: "jaed_tools_list: what reusable tools does this workspace have?",
    parameters: Type.Object({}),
    async execute() {
      const tools = listTools();
      const text = tools.length
        ? tools.map((t) => `- ${t.name}: ${t.description || "(no description)"}\n  ${t.path}`).join("\n")
        : "No tools yet. Write one with jaed_tools_write.";
      return { content: [{ type: "text", text }] };
    },
  });

  pi.registerTool({
    name: "jaed_tools_write",
    label: "jaed tools write",
    description:
      "Write a reusable JavaScript tool into the workspace's .jaed/tools folder. " +
      "The tool is an ESM file exporting `default async function run(input, jaed)`. " +
      'Import the helper with `import jaed from "jaed"`. It runs sandboxed (no ' +
      "network or processes; reads the workspace; writes only .jaed/tmp) with a " +
      "small package allowlist (zod, yaml, date-fns, nanoid, marked, lodash-es). " +
      "Provide a concise `description`; it becomes the @description the tools " +
      "view and jaed_tools_list show.",
    promptSnippet: "jaed_tools_write: save a reusable script into .jaed/tools.",
    parameters: Type.Object({
      name: Type.String({ description: "Tool name, e.g. list-todos (a .js file is created)." }),
      description: Type.Optional(Type.String({ description: "One-line description of what the tool does." })),
      code: Type.String({ description: "Full ESM source of the tool." }),
    }),
    async execute(_toolCallId, params) {
      try {
        const file = writeTool(params.name, params.description ?? "", params.code);
        const stem = params.name.replace(/\.m?js$/, "");
        return {
          content: [
            {
              type: "text",
              text: `Wrote ${file}. Run it with jaed_tools_run { "name": "${stem}" }.`,
            },
          ],
        };
      } catch (err) {
        return { content: [{ type: "text", text: `Could not write tool: ${String(err)}` }] };
      }
    },
  });

  pi.registerTool({
    name: "jaed_tools_run",
    label: "jaed tools run",
    description:
      "Run a tool from the workspace's .jaed/tools folder in the sandbox and " +
      "return its output and logs. `args` is passed to the tool as its input object.",
    promptSnippet: "jaed_tools_run: run a workspace tool in the sandbox.",
    parameters: Type.Object({
      name: Type.String({ description: "Tool name, e.g. list-todos (no .js)." }),
      args: Type.Optional(
        Type.Object({}, { additionalProperties: true, description: "Arguments for the tool's run(input, jaed)." }),
      ),
    }),
    async execute(_toolCallId, params) {
      const result = await runTool(params.name, params.args ?? {});
      return { content: [{ type: "text", text: formatRun(params.name, result) }] };
    },
  });

  pi.registerTool({
    name: "jaed_skills_write",
    label: "jaed skills write",
    description:
      "Write a markdown skill into the workspace's .jaed/skills folder for longer " +
      "reference material. A name/description front matter block is added when absent.",
    promptSnippet: "jaed_skills_write: save reference notes into .jaed/skills.",
    parameters: Type.Object({
      name: Type.String({ description: "Skill name, e.g. release-checklist." }),
      description: Type.Optional(Type.String({ description: "When to use this skill." })),
      content: Type.String({ description: "Markdown body of the skill." }),
    }),
    async execute(_toolCallId, params) {
      try {
        const file = writeSkill(params.name, params.description ?? "", params.content);
        return { content: [{ type: "text", text: `Wrote ${file}.` }] };
      } catch (err) {
        return { content: [{ type: "text", text: `Could not write skill: ${String(err)}` }] };
      }
    },
  });

  pi.registerTool({
    name: "jaed_skills_read",
    label: "jaed skills read",
    description: "Read a markdown skill from the workspace's .jaed/skills folder.",
    promptSnippet: "jaed_skills_read: load a workspace skill.",
    parameters: Type.Object({
      name: Type.String({ description: "Skill name, e.g. release-checklist (no .md)." }),
    }),
    async execute(_toolCallId, params) {
      const text = readSkill(params.name);
      return {
        content: [
          {
            type: "text",
            text: text === null ? `No skill "${params.name}" in ${skillsDir()}.` : text,
          },
        ],
      };
    },
  });
}
