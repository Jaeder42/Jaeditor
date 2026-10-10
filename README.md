# aw — an AI workspace TUI

An editor, a terminal, and a bring-your-own agent side panel, composed as
embedded terminal panes in one Bubble Tea TUI. The host terminal (Ghostty)
does the rendering; `aw` is just text.

```
┌──────────────────┬───────────┐
│  nvim             │  agent    │
│                   │  (pi)     │
├──────────────────┤           │
│  terminal         │           │
└──────────────────┴───────────┘
 aw · chess · everforest  ● nvim  ○ zsh  ● pi      ctrl+hjkl focus · ctrl+q chrome
```

## Usage

```sh
go build -o aw .
./aw                       # workspace in the current directory
./aw ~/dev/chess           # a project directory
./aw -a opencode ~/dev/chess   # bring your own agent
./aw --theme everforest ~/dev/chess
```

## Keys

| Key | Mode | Effect |
|---|---|---|
| `ctrl+h/j/k/l` | anywhere | switch pane focus |
| `ctrl+shift+q` | anywhere | quit |
| anything else (focused pane) | pane | goes to the pane's process |
| `ctrl+q` | pane | release the pane → chrome mode |
| `h/j/k/l` or arrows | chrome | focus panes |
| `t` | chrome | hide/show the terminal pane (process keeps running) |
| `a` | chrome | hide/show the agent pane (process keeps running) |
| `s` | chrome | agent picker popup |
| `g` | chrome | fullscreen git view (panes keep running) |
| `H/J/K/L` | chrome | resize the grid (agent width / terminal height) |
| `Q` | chrome | quit (always works; legacy terminals can't report `ctrl+shift+q`) |
| `j/k` · enter · esc | picker | move · swap agent · close |
| `j/k` · `ctrl+d/u` · `r` · `g`/`esc` | git | file · scroll diff · refresh · close |

The git view shows changed files grouped **staged / unstaged /
untracked**, the selected file's diff colored to the theme, and the
branch in its header. `Q` quits from there too.

Hidden panes show as `◇` in the status bar. Hiding the focused pane
releases focus to chrome mode; hidden panes keep running and re-layout
when shown again. Hide both for a fullscreen editor; `t`/`a` bring the
workspace back.

Panes whose process exits (e.g. `:q` in nvim) respawn with the original
command; a pane that dies within 3s of its own spawn is marked `✕ exited`
instead of looping.

The status bar shows each pane's live guest title (nvim names the file it
edits; pi sets `π - workspace`). The agent picker swaps the agent pane's
process live — `ctrl+q` `s` `j/k` `enter` — without leaving the session.

## Bring your own agent

Builtin registry: `pi` (default), `opencode`, `shell`. Add your own in
`~/.config/aw/agents.conf`:

```
# name = command [args...]
claude = /usr/local/bin/claude
aider = aider --model sonnet
```

They appear in the picker and in `-a`.

## Layout

```
main.go                   entry: dir, --theme, -a agent, --editor
internal/agents/          BYO agent registry (pi, opencode, shell)
internal/git/             repository state via the git CLI
internal/pane/            raw-PTY terminal (spanreed + windrunner)
internal/theme/           semantic palette + everforest
internal/workspace/       grid layout, focus graph, respawn, status bar,
                          agent picker, git view
```

Guests run with `TERM=xterm-256color` on raw PTYs and inherit your
environment. Panes paint their own everforest background, so the
workspace looks identical in any host terminal. pi's `system` theme
queries the pane's terminal and adapts automatically.

## Roadmap

- **M4** native file-tree pane, project tabs, more themes (catppuccin,
  gruvbox, tokyonight), auto theme from nvim; deeper pi state via RPC

## Stack

[Bubble Tea v2](https://charm.land) · [spanreed](https://github.com/trentkm/spanreed)
(embeddable terminal, VT emulation via `charmbracelet/x/vt`, PTY via
`windrunner`/`creack/pty`) · lipgloss for chrome.