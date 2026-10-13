// jaed tools allowlist loader.
//
// Installed by run.mjs with module.registerHooks (in-thread, no worker
// thread — worker threads are denied by the sandbox). This resolve hook
// is the second layer of the sandbox (the first being Node's
// --permission flags). It rejects every import that is not a relative
// file inside the tool, an allowlisted npm package, the injected `jaed`
// helper, or a permitted node: builtin. The permission flags already
// deny network, processes, and workers at the syscall level; this hook
// also removes the modules themselves so a tool cannot even reach for
// them.

import * as path from "node:path";
import { builtinModules } from "node:module";
import { pathToFileURL } from "node:url";

// Every Node builtin, by bare name (imports may use "fs" or "node:fs").
const BUILTINS = new Set(builtinModules);

// The npm packages the sandbox exposes. Vendored into the runtime's
// node_modules at install time (see install.sh and package.json).
const ALLOWED_PACKAGES = new Set([
  "zod",
  "yaml",
  "date-fns",
  "nanoid",
  "marked",
  "lodash-es",
]);

// Builtins that grant a capability the sandbox is meant to withhold.
// Denied here as defence in depth; the permission flags deny the actual
// operations regardless.
const DENIED_BUILTINS = new Set([
  "child_process",
  "worker_threads",
  "cluster",
  "net",
  "dgram",
  "http",
  "https",
  "http2",
  "tls",
  "inspector",
  "wasi",
  "repl",
]);

/** The package a bare specifier belongs to, scopes included. */
function packageOf(specifier) {
  if (specifier.startsWith("@")) {
    return specifier.split("/").slice(0, 2).join("/");
  }
  return specifier.split("/")[0];
}

export function resolve(specifier, context, nextResolve) {
  // The jaed helper lives in the runtime, keyed by its name alone.
  if (specifier === "jaed") {
    const runtime = process.env.JAED_RUNTIME;
    if (!runtime) {
      throw new Error("jaed sandbox: runtime directory is unknown");
    }
    return nextResolve(pathToFileURL(path.join(runtime, "jaed.mjs")).href, context);
  }

  const builtinName = specifier.startsWith("node:") ? specifier.slice("node:".length) : specifier;
  if (specifier.startsWith("node:") || BUILTINS.has(specifier)) {
    if (DENIED_BUILTINS.has(builtinName)) {
      throw new Error(`jaed sandbox: node:${builtinName} is not available to tools`);
    }
    return nextResolve(specifier, context);
  }

  // Relative and absolute paths resolve normally; the permission flags
  // decide which files may actually be read.
  if (
    specifier.startsWith(".") ||
    specifier.startsWith("/") ||
    specifier.startsWith("file:")
  ) {
    return nextResolve(specifier, context);
  }

  if (ALLOWED_PACKAGES.has(packageOf(specifier))) {
    // Resolve the package as if it were imported from the runtime, so
    // tools in any workspace share the runtime's vendored node_modules.
    // (Rewrite the parent URL rather than import.meta.resolve, which
    // would re-enter this hook.)
    const runtime = process.env.JAED_RUNTIME;
    if (!runtime) {
      throw new Error("jaed sandbox: runtime directory is unknown");
    }
    const parentURL = pathToFileURL(path.join(runtime, "package.json")).href;
    try {
      return nextResolve(specifier, { ...context, parentURL });
    } catch {
      throw new Error(
        `jaed sandbox: "${packageOf(specifier)}" is allowlisted but not installed — run ./install.sh`,
      );
    }
  }

  throw new Error(`jaed sandbox: module "${specifier}" is not on the allowlist`);
}
