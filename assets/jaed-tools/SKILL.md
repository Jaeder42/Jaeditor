---
name: jaed-tools
description: Write and run sandboxed JavaScript tools in a jaed workspace. Use when the user asks to create a reusable script, automate a repo task, or run one of the workspace's existing tools.
---

# jaed tools

A jaed workspace has a personal `.jaed/` folder (never committed — it is
in `.git/info/exclude`). It is where you keep small JavaScript tools the
user can run from the chat:

```
.jaed/
  tools/     runnable scripts, one per task   (my-tool.js)
  skills/    markdown reference material
  tmp/       the only place a tool may write
```

## Authoring a tool

Each tool is one ESM file in `.jaed/tools/` named after the tool. Give it
a leading block comment with an `@description`; jaed shows it in the
tools view (`ctrl+q` `T`).

```js
/**
 * @description List TODO and FIXME comments in the workspace.
 */
import { readFile } from "jaed";

export default async function run(input, jaed) {
  // input is the JSON arguments passed on the command line
  // return a string, or any JSON-serialisable value
  return `scanned ${jaed.workspace}`;
}
```

- Name = filename stem: `.jaed/tools/list-todos.js` → `list-todos`.
- `export default async function run(input, jaed)` is called with the
  parsed arguments and the helper (below).
- Returning a string prints it verbatim; anything else is pretty-printed.
- `console.log` and stray writes are captured and shown as logs.

Write one with `jaed_tools_write` (it validates the name and path), or
edit the file directly.

## Running a tool

Call `jaed_tools_run` with `{ "name": "list-todos", "args": { ... } }`.
It spawns the sandbox and returns the tool's output (and its logs). Use
`jaed_tools_list` to see what already exists before writing a new one.

## The sandbox

Tools run in Node with the permission model on. They **cannot**:

- reach the network (`--allow-net` is off);
- spawn processes or workers (`child_process`, `worker_threads`,
  `cluster` are denied);
- read outside the workspace (and the runtime);
- write outside `.jaed/tmp`.

They *can* read the workspace, including the file the user is editing.

### The `jaed` helper

```js
import jaed from "jaed"; // or: import { workspace, activeFile } from "jaed"

jaed.workspace   // absolute path of the workspace
jaed.tmpDir      // .jaed/tmp — the only writable place
jaed.activeFile() // path of the file open in the editor, or null
jaed.readFile(p)  // read a workspace-relative file
jaed.writeTmp(n, data) // write inside .jaed/tmp; returns the path
jaed.notify(msg)  // a note shown with the tool's logs
```

### Available packages

Only these third-party modules resolve; everything else is rejected:

`zod`, `yaml`, `date-fns`, `nanoid`, `marked`, `lodash-es`

Standard `node:` builtins work, except the capability modules above.

## Skills

Longer reference material goes in `.jaed/skills/<name>.md`, with a
`name` and `description` in the front matter so it can be listed and
loaded on demand. Write them with `jaed_skills_write` and read them with
`jaed_skills_read`.
