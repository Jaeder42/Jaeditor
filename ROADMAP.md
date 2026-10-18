# Roadmap

Working notes: what's shipped, what's next, and the design groundwork
for the remaining M4 items. The README stays the user-facing summary;
this file carries the detail.

## Shipped

### M4 progress

- **More themes** — everforest, catppuccin, gruvbox, tokyonight, all
  applied live from the settings popup (`S`) or `--theme`.
- **Auto theme from nvim** — nvim reports `g:colors_name` over the
  live link (a tiny per-workspace file, see `internal/workspace/link.go`):
  once at startup and on every `:colorscheme`. In auto mode jaed maps
  the name onto the built-in palette it matches — `gruvbox-material` →
  gruvbox, `catppuccin-mocha` → catppuccin, `TokyoNight_Storm` →
  tokyonight. Unknown schemes leave the theme alone. Surfaces: settings
  row `auto (nvim)` (shows the applied palette), `--theme auto`,
  status bar label `auto:<theme>`.
- **Deeper agent state** — both plugins (`assets/jaed.ts` for pi,
  `assets/jaed-opencode.ts` for opencode) publish
  `{status, model, activity?, context?}` to
  `$TMPDIR/jaed-state-<hash-of-workspace>`: pi on session start, model
  switches, turn and tool edges, and settle; opencode off its
  `session.status` / `session.idle` / `session.model.selected` /
  `session.step.*` / `session.tool.*` event stream. The status bar
  appends the model, the tool in flight (else `running`, or `retrying`
  in the warn color), and `· <n>%` context use (pi), so the agent can
  be monitored without focusing it. Swapping the agent clears the
  report; agents without a plugin (a shell) show nothing.

### Recent fixes

- **Terminal copy** — jaed takes mouse reporting, so a drag over a plain
  shell used to do nothing (the host terminal never saw it). Grid panes
  whose guest has not enabled mouse tracking now get the same drag-to-copy
  selection as the git/tools views, in pane-local coordinates and clipped
  to the box; the status bar confirms the copy.
- **Mouse hitbox** — mouse events arrive in zero-based screen rows but
  the grid starts below the resource bar: the hit test now shifts by
  `topRows`, so clicks land on the line under the cursor (previously
  everything was one line low when the resource bar was on, which is
  the default).
- **Click-to-focus** — a click on a pane focuses it (any button press;
  motion and releases never steal focus). Tracking guests (nvim,
  opencode) still receive the event with pane-local coordinates.
- **`ctrl+t` scoping** — the new-shell-tab chord is consumed only while
  the terminal is focused (or from chrome mode, which has no guest), so
  nvim keeps its own `ctrl+t` (tag jump), like its `ctrl+w`.

## Next: native file-tree pane

The headline remaining item. Design groundwork:

- **Form factor** — two options:
  - *Fullscreen toggle view*, like the git (`g`) and tools (`T`) views
    — cheap, zero layout changes, matches `gitview.go`/`toolsview.go`
    patterns. Recommended first step.
  - *4th grid slot* (IDE-style sidebar) — real pane, but bigger blast
    radius: `Layout` has two fractions, and `Neighbor`, `paneAt`,
    resize, and the status bar assume exactly three slots.
- **Opening files is the hard part.** Typing `:e path\r` into the pane
  is mode-dependent (insert mode would eat it as text). The robust
  route, consistent with the no-socket link philosophy: inject a small
  timer-poll autocmd at launch — nvim polls a `jaed-cmd-<hash>` file,
  executes one line, truncates it. That gives the tree a reliable
  `open` action and future features a free editor-command channel.
- **Tree state** — lazy walk of the workspace, expand/collapse,
  skip `.git`; gitignore filtering and dirty markers could reuse
  `internal/git`.

## Later: project tabs

Workspaces-as-tabs: each tab owns a cwd, its panes, and view state.
The terminal-tab machinery (`tabs.go`: park processes, mirror the
active one into the slot arrays) is the precedent, but `App` *is* the
workspace today — this needs a workspace extraction first, then a
top-level tab bar and key routing. Consider deferring to M5.

## Known warts (small, pick up anytime)

- Pane backgrounds don't follow live theme switches: the background is
  a spawn-time `spec.Background` and spanreed has no runtime setter.
  nvim repaints itself, but the terminal and agent panes keep the
  launch theme's background until they respawn.
- `assets/jaed.ts` tool results predate a pi type update: tsc against
  the installed pi wants a `details` field on the six
  `registerTool().execute()` returns (the runtime is tolerant).
- Auto theme and the agent state channel ride on per-workspace files in
  `$TMPDIR`; they are best-effort and inert outside jaed, but stale
  files from crashed sessions linger there (harmless, never read once
  the writer restarts).