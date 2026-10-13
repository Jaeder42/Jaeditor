// jaed tools runtime — the sandbox entry point.
//
// The agent's plugins spawn this file *with the Node permission model
// enabled* (see assets/jaed-opencode.ts for the exact flags). It is not
// safe to run directly: the flags, not this file, are the sandbox.
//
//   node --permission \
//     --allow-fs-read=<workspace> \
//     --allow-fs-read=<runtime> \
//     --allow-fs-read=<active-link-file> \
//     --allow-fs-write=<workspace>/.jaed/tmp \
//     --max-old-space-size=256 \
//     <runtime>/run.mjs <tool-name> '<json-args>'
//
// It loads .jaed/tools/<name>.js through an allowlist loader, calls its
// default export as run(input, jaed), and prints one JSON line:
//
//   { "ok": true,  "output": "...", "logs": ["..."], "durationMs": 12 }
//   { "ok": false, "error": "...", "logs": [...], "durationMs": 12 }
//
// Everything the tool writes to stdout/stderr or console is captured into
// "logs" so it cannot corrupt that JSON line.

import { registerHooks } from "node:module";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { createHash } from "node:crypto";
import { fileURLToPath, pathToFileURL } from "node:url";
import { inspect } from "node:util";

const runtime = path.dirname(fileURLToPath(import.meta.url));
process.env.JAED_RUNTIME = runtime;

// Capture stray output before anything else can write it.
const rawWrite = process.stdout.write.bind(process.stdout);
const logs = [];
const record = (...parts) => {
  logs.push(parts.map((p) => (typeof p === "string" ? p : inspect(p))).join(" "));
};
for (const level of ["log", "info", "warn", "error", "debug"]) {
  console[level] = (...parts) => record(...parts);
}
process.stdout.write = (chunk, encoding, callback) => {
  logs.push(Buffer.isBuffer(chunk) ? chunk.toString("utf8") : String(chunk));
  if (typeof encoding === "function") encoding();
  else if (typeof callback === "function") callback();
  return true;
};
process.stderr.write = (chunk, encoding, callback) => {
  logs.push(Buffer.isBuffer(chunk) ? chunk.toString("utf8") : String(chunk));
  if (typeof encoding === "function") encoding();
  else if (typeof callback === "function") callback();
  return true;
};

const started = Date.now();

function finish(result) {
  result.durationMs = Date.now() - started;
  if (logs.length > 0) result.logs = logs;
  // Flush before exiting so a piped stdout is not truncated.
  rawWrite(JSON.stringify(result) + "\n", () => process.exit(result.ok ? 0 : 1));
}

/** The live-link file jaed publishes the active buffer to. */
function activeLinkPath(workspace) {
  let resolved = workspace;
  try {
    resolved = fs.realpathSync(workspace);
  } catch {
    // keep the raw path
  }
  const hash = createHash("sha256").update(resolved).digest("hex").slice(0, 16);
  return path.join(os.tmpdir(), `jaed-active-${hash}`);
}

/** The file the user is editing, or null when there is none. */
function readActiveFile(workspace) {
  try {
    const active = fs.readFileSync(activeLinkPath(workspace), "utf8").trim();
    return path.isAbsolute(active) ? active : null;
  } catch {
    return null;
  }
}

async function main() {
  const [name, argsJSON] = process.argv.slice(2);
  if (!name) {
    finish({ ok: false, error: "usage: run.mjs <tool-name> [json-args]" });
    return;
  }
  if (name.includes("/") || name.includes("\\") || name.includes("..")) {
    finish({ ok: false, error: `jaed sandbox: invalid tool name "${name}"` });
    return;
  }

  const workspace = process.cwd();
  const toolDir = path.join(workspace, ".jaed", "tools");
  const source = path.join(toolDir, `${name}.js`);
  if (!fs.existsSync(source)) {
    finish({ ok: false, error: `jaed: no tool "${name}" in ${toolDir}` });
    return;
  }

  let input = {};
  if (argsJSON) {
    try {
      input = JSON.parse(argsJSON);
    } catch (err) {
      finish({ ok: false, error: `jaed: tool arguments are not JSON: ${err}` });
      return;
    }
  }

  // The context the injected `jaed` helper module reads.
  process.env.JAED_CONTEXT = JSON.stringify({
    workspace,
    tmp: path.join(workspace, ".jaed", "tmp"),
    runtime,
    tool: name,
    activeFile: readActiveFile(workspace),
  });

  // Allowlist loader: registered in-thread before any tool import. A
  // worker-thread loader would need --allow-worker, which the sandbox
  // withholds, so module.registerHooks (synchronous) is the right tool.
  const { resolve } = await import(pathToFileURL(path.join(runtime, "loader.mjs")).href);
  registerHooks({ resolve });

  const jaed = (await import(pathToFileURL(path.join(runtime, "jaed.mjs")).href)).default;
  const mod = await import(pathToFileURL(source).href);

  if (typeof mod.default !== "function") {
    finish({ ok: false, error: `jaed: ${name}.js has no default export function` });
    return;
  }

  const value = await mod.default(input, jaed);
  let output;
  if (typeof value === "string") output = value;
  else if (value === undefined || value === null) output = "";
  else output = JSON.stringify(value, null, 2);
  finish({ ok: true, output });
}

try {
  await main();
} catch (err) {
  finish({ ok: false, error: err && err.stack ? String(err.stack) : String(err) });
}
