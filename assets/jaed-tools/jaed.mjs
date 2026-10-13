// The `jaed` helper module injected into every tool.
//
// A tool imports it with `import jaed from "jaed"` (or named imports).
// The runtime passes workspace, scratch dir, and the active-file link
// through JAED_CONTEXT before loading the tool.

import * as fs from "node:fs";
import * as path from "node:path";

const ctx = (() => {
  try {
    return JSON.parse(process.env.JAED_CONTEXT || "{}");
  } catch {
    return {};
  }
})();

/** The workspace the tool runs in (its process working directory). */
export const workspace = ctx.workspace || process.cwd();

/** The only directory a tool may write to: <workspace>/.jaed/tmp. */
export const tmpDir = ctx.tmp || path.join(workspace, ".jaed", "tmp");

/** The runtime directory, for reading the allowlist or helper source. */
export const runtimeDir = ctx.runtime || null;

/** The absolute path of the file the user is editing, or null. */
export function activeFile() {
  return ctx.activeFile || null;
}

/** Read a workspace-relative (or absolute) file as utf8 text. */
export function readFile(file, encoding = "utf8") {
  return fs.readFileSync(path.resolve(workspace, file), encoding);
}

/**
 * Write to the scratch directory. Name may be relative (resolved inside
 * .jaed/tmp) or absolute (must still land inside it). Returns the path.
 */
export function writeTmp(name, data) {
  const dest = path.isAbsolute(name) ? name : path.join(tmpDir, name);
  const rel = path.relative(tmpDir, dest);
  if (rel === "" || rel.startsWith("..") || path.isAbsolute(rel)) {
    throw new Error("jaed: writeTmp must stay inside .jaed/tmp");
  }
  fs.mkdirSync(path.dirname(dest), { recursive: true });
  fs.writeFileSync(dest, data);
  return dest;
}

/** Notify the user; surfaced with the tool's logs. */
export function notify(message) {
  process.stderr.write(`[jaed] ${message}\n`);
}

export default {
  workspace,
  tmpDir,
  runtimeDir,
  activeFile,
  readFile,
  writeTmp,
  notify,
};
