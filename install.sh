#!/bin/sh
# install.sh — build jaed and put it on your PATH.
#
# Usage:
#   ./install.sh              # install to ~/.local/bin (default)
#   ./install.sh --uninstall  # remove the installed binary
#
# PATH is wired up for zsh, bash, and fish by adding a guarded export to
# each existing rc file; new rc files are only created for shells you
# actually use. Safe to run repeatedly: existing lines are left alone.
set -eu

INSTALL_DIR="${HOME}/.local/bin"
BIN="${INSTALL_DIR}/jaed"
REPO="$(cd "$(dirname "$0")" && pwd)"

if [ "${1:-}" = "--uninstall" ]; then
    if [ -f "$BIN" ]; then
        rm -f "$BIN"
        echo "removed $BIN"
    else
        echo "nothing to remove: $BIN"
    fi
    rm -rf "${HOME}/.config/jaed/runtime" "${HOME}/.config/opencode/skills/jaed-tools"
    echo "removed the agent tools runtime and skill"
    echo "rc PATH lines can stay; they are harmless without the binary."
    exit 0
fi

if ! command -v go >/dev/null 2>&1; then
    echo "error: go is required to build jaed (https://go.dev/dl/)" >&2
    exit 1
fi

echo "building jaed…"
cd "$REPO"
mkdir -p "$INSTALL_DIR"
# Build aside and move into place so a running jaed never sees a
# half-written binary (rename works on macOS even while executing).
go build -o "${BIN}.tmp" .
mv -f "${BIN}.tmp" "$BIN"
echo "installed $BIN"

# --- agent tools runtime ---------------------------------------------------
# The sandboxed Node runtime the agent's tools run in, plus the skill that
# documents the conventions. jaed works without it; only the agent's
# jaed_tools_* tools need it.
RUNTIME_DIR="${HOME}/.config/jaed/runtime"
if [ -d "${REPO}/assets/jaed-tools" ]; then
    mkdir -p "$RUNTIME_DIR"
    cp "${REPO}"/assets/jaed-tools/* "$RUNTIME_DIR"/
    if command -v node >/dev/null 2>&1 && command -v npm >/dev/null 2>&1; then
        echo "installing the tool runtime packages…"
        if ! (cd "$RUNTIME_DIR" && npm install --omit=dev --no-audit --no-fund --silent); then
            echo "warning: npm install failed; tools will only have Node's builtins"
        fi
    else
        echo "note: node/npm not found — install Node 22+ so the sandboxed tools can run"
        echo "      tools will otherwise have only Node's builtins"
    fi
    echo "  installed the agent tools runtime in $RUNTIME_DIR"

    # The skill, for opencode's on-demand skill system. pi gets the same
    # text injected by its extension.
    SKILL_DIR="${HOME}/.config/opencode/skills/jaed-tools"
    mkdir -p "$SKILL_DIR"
    cp "${REPO}/assets/jaed-tools/SKILL.md" "$SKILL_DIR/SKILL.md"
    echo "  installed the jaed-tools skill for opencode"
fi

# --- PATH wiring -----------------------------------------------------------

export_line='export PATH="$HOME/.local/bin:$PATH"'
fish_line='fish_add_path ~/.local/bin'

sh_added=0
sh_created=0

# ensure_sh_rc RC: append the PATH line if the file exists and lacks it.
ensure_sh_rc() {
    rc="$1"
    if [ ! -f "$rc" ]; then
        return
    fi
    # Only an active (non-comment) line counts as wired up: rc files
    # often carry commented-out PATH examples mentioning the same dir.
    if grep -qs '^[^#]*\.local/bin' "$rc"; then
        return
    fi
    printf '\n# added by jaed install\n%s\n' "$export_line" >> "$rc"
    sh_added=$((sh_added + 1))
    echo "  PATH added to $rc"
}

ensure_sh_rc "${HOME}/.zshrc"
ensure_sh_rc "${HOME}/.bashrc"
ensure_sh_rc "${HOME}/.bash_profile"

# No rc file at all: create one for the login shell so PATH persists.
if [ "$sh_added" -eq 0 ] && ! [ -f "${HOME}/.zshrc" ] && ! [ -f "${HOME}/.bashrc" ] && ! [ -f "${HOME}/.bash_profile" ]; then
    case "${SHELL:-}" in
    */zsh) rc="${HOME}/.zshrc" ;;
    *)     rc="${HOME}/.bashrc" ;;
    esac
    printf '# added by jaed install\n%s\n' "$export_line" > "$rc"
    sh_created=1
    echo "  created $rc with the PATH line"
fi

# Fish, if present.
fish_rc="${HOME}/.config/fish/config.fish"
if [ -d "${HOME}/.config/fish" ] || case "${SHELL:-}" in */fish) true ;; *) false ;; esac; then
    if ! grep -qs '^[^#]*\.local/bin' "$fish_rc" 2>/dev/null; then
        mkdir -p "${HOME}/.config/fish"
        printf '\n# added by jaed install\n%s\n' "$fish_line" >> "$fish_rc"
        echo "  PATH added to $fish_rc"
    fi
fi

# Optional pi live link (manual by design — see README).
if [ -f "${REPO}/assets/jaed.ts" ] && ! [ -f "${HOME}/.pi/agent/extensions/jaed.ts" ]; then
    echo "optional pi live link: cp ${REPO}/assets/jaed.ts ~/.pi/agent/extensions/jaed.ts"
fi
if [ -f "${REPO}/assets/jaed-opencode.ts" ] && ! [ -f "${HOME}/.config/opencode/plugins/jaed.ts" ]; then
    echo "optional opencode live link: cp ${REPO}/assets/jaed-opencode.ts ~/.config/opencode/plugins/jaed.ts"
fi

# Current shell already knows the dir? Then nothing to source.
case ":${PATH}:" in
*":${INSTALL_DIR}:"*)
    echo "done — $INSTALL_DIR is on PATH in this shell."
    ;;
*)
    echo "done — restart your terminal, or run:"
    echo "  source ~/.zshrc   # (or your shell's rc)"
    ;;
esac
echo "run: jaed ~/dev/chess"