// Package pane wraps a spanreed terminal: a process on a raw PTY plus the
// emulator view. One Pane is one live terminal.
package pane

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/trentkm/spanreed"
	"github.com/trentkm/spanreed/ptyadapter"
	"github.com/trentkm/windrunner"
)

// Spec describes the process a pane runs.
type Spec struct {
	Command    string
	Args       []string
	Dir        string
	Env        []string
	Scrollback int
	// Background is an optional hex color painted over cells the guest
	// leaves at the terminal default, making the pane an opaque surface
	// independent of the host terminal's theme.
	Background string
}

// Pane is one embedded terminal. Value copies stay coherent: the live
// state lives behind shared pointers (see spanreed.Model). Focus,
// however, is a value field on spanreed.Model — mutate it only through a
// Pane you keep (pointer receivers) or through Update's returned copy.
type Pane struct {
	term      spanreed.Model
	transport spanreed.Transport
	session   *windrunner.Session
	title     string
	// modes tracks the terminal modes the guest toggles (bracketed paste,
	// mouse); a pointer because Pane is copied around and the watcher
	// goroutine writes it concurrently.
	modes *guestModes
}

// ExitMsg reports that a pane's process exited.
type ExitMsg struct {
	PaneID int64
	Code   int
}

// sessionProvider is satisfied by ptyadapter's concrete transport; the
// spanreed.Transport interface does not expose the session, but the
// concrete type does, and this assertion finds it without needing
// ptyadapter to export its struct.
type sessionProvider interface{ Session() *windrunner.Session }

// Spawn starts the process on a fresh PTY sized cols x rows. The child
// inherits the environment; windrunner sets TERM=xterm-256color unless
// the spec provides one.
func Spawn(ctx context.Context, spec Spec, cols, rows int) (Pane, error) {
	var opts []spanreed.Option
	if spec.Background != "" {
		opts = append(opts, spanreed.WithDefaultBackground(lipgloss.Color(spec.Background)))
	}
	transport, err := ptyadapter.Spawn(ctx, ptyadapter.Spec{
		Command:    spec.Command,
		Args:       spec.Args,
		Dir:        spec.Dir,
		Env:        spec.Env,
		Scrollback: spec.Scrollback,
	}, cols, rows)
	if err != nil {
		return Pane{}, err
	}
	var session *windrunner.Session
	if sp, ok := transport.(sessionProvider); ok {
		session = sp.Session()
	}
	p := Pane{
		term:      spanreed.New(transport, cols, rows, opts...),
		transport: transport,
		session:   session,
		title:     filepath.Base(spec.Command),
		modes:     &guestModes{},
	}
	if session != nil {
		go watchModes(session, p.modes)
	}
	return p, nil
}

// guestModes is the set of terminal modes the guest toggles via
// DECSET/DECRST that jaed must honor when relaying input: bracketed paste
// (2004) and the mouse tracking and encoding modes (1000/1002/1003/1006).
// The mode watcher writes it from a second subscription; the UI reads it.
type guestModes struct {
	mu sync.Mutex
	on map[int]bool
}

func (g *guestModes) set(mode int, enabled bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.on == nil {
		g.on = make(map[int]bool)
	}
	if enabled {
		g.on[mode] = true
	} else {
		delete(g.on, mode)
	}
}

func (g *guestModes) enabled(mode int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.on[mode]
}

// BracketedPaste reports DECSET 2004.
func (g *guestModes) BracketedPaste() bool { return g.enabled(2004) }

// MouseTracking reports any of DECSET 1000/1002/1003 — the guest wants to
// receive mouse events rather than have the wheel scroll pane history.
func (g *guestModes) MouseTracking() bool {
	return g.enabled(1000) || g.enabled(1002) || g.enabled(1003)
}

// SGRMouse reports DECSET 1006, the extended mouse encoding.
func (g *guestModes) SGRMouse() bool { return g.enabled(1006) }

// watchModes tracks the guest's terminal modes from a second subscription
// on the session, so pane input (pastes, mouse) is relayed exactly as the
// guest expects it.
func watchModes(session *windrunner.Session, modes *guestModes) {
	_, sub := session.Attach(8)
	var carry []byte
	for chunk := range sub.Output() {
		carry = append(carry, chunk...)
		carry = scanModes(carry, modes)
	}
}

// scanModes consumes complete DECSET/DECRST sequences from buf, applies
// them to modes, and returns the unconsumed tail. A sequence split across
// chunks is kept whole and finished on the next call.
func scanModes(buf []byte, modes *guestModes) []byte {
	const prefix = "\x1b[?"
	for {
		i := bytes.Index(buf, []byte(prefix))
		if i < 0 {
			// Hold back a possible partial prefix for the next chunk.
			if len(buf) > len(prefix)-1 {
				return buf[len(buf)-(len(prefix)-1):]
			}
			return buf
		}
		rest := buf[i+len(prefix):]
		end := 0
		for end < len(rest) && rest[end] != 'h' && rest[end] != 'l' {
			if c := rest[end]; (c < '0' || c > '9') && c != ';' {
				break
			}
			end++
		}
		switch {
		case end == len(rest):
			// Incomplete: wait for the h/l terminator.
			return buf[i:]
		case rest[end] != 'h' && rest[end] != 'l':
			// Not a mode sequence we know; drop the prefix and rescan.
			buf = rest
			continue
		}
		enabled := rest[end] == 'h'
		for _, field := range bytes.Split(rest[:end], []byte(";")) {
			if n, err := strconv.Atoi(string(field)); err == nil {
				modes.set(n, enabled)
			}
		}
		buf = rest[end+1:]
	}
}

// Paste delivers pasted text to the guest: wrapped in bracketed-paste
// markers when the guest enabled DECSET 2004 (TUIs like nvim and pi),
// raw bytes otherwise, so plain programs never see stray escapes.
func (p Pane) Paste(text string) error {
	if p.modes != nil && p.modes.BracketedPaste() {
		return p.Write([]byte("\x1b[200~" + text + "\x1b[201~"))
	}
	return p.Write([]byte(text))
}

// BracketedPaste reports whether the guest asked for bracketed paste.
func (p Pane) BracketedPaste() bool {
	return p.modes != nil && p.modes.BracketedPaste()
}

// MouseTracking reports whether the guest enabled a mouse tracking mode,
// and therefore wants wheel and click events instead of pane scrollback.
func (p Pane) MouseTracking() bool {
	return p.modes != nil && p.modes.MouseTracking()
}

// Mouse relays one mouse event to the guest in the encoding it enabled.
// x and y are pane-local, zero-based. It is a no-op unless the guest has
// enabled mouse tracking.
func (p Pane) Mouse(msg tea.MouseMsg, x, y int) error {
	if p.modes == nil || !p.modes.MouseTracking() {
		return nil
	}
	m := msg.Mouse()
	motion := false
	release := false
	switch msg.(type) {
	case tea.MouseMotionMsg:
		motion = true
	case tea.MouseReleaseMsg:
		release = true
	}
	data := encodeMouse(p.modes.SGRMouse(), ansi.MouseButton(m.Button), motion, release,
		m.Mod&tea.ModShift != 0, m.Mod&tea.ModAlt != 0, m.Mod&tea.ModCtrl != 0, x, y)
	if data == nil {
		return nil
	}
	return p.Write(data)
}

// encodeMouse renders one mouse event in the requested encoding: SGR
// (DECSET 1006) when sgr is set, legacy X10 otherwise. x and y are
// pane-local, zero-based; they are converted to the protocol's 1-based
// cells. It returns nil when the event cannot be represented.
func encodeMouse(sgr bool, btn ansi.MouseButton, motion, release, shift, alt, ctrl bool, x, y int) []byte {
	code := ansi.EncodeMouseButton(btn, motion, shift, alt, ctrl)
	x, y = max(0, x)+1, max(0, y)+1
	if sgr {
		final := byte('M')
		if release {
			final = 'm'
		}
		return []byte(fmt.Sprintf("\x1b[<%d;%d;%d%c", code, x, y, final))
	}
	// Legacy X10 encoding: coordinates are limited to 223.
	if x > 223 || y > 223 {
		return nil
	}
	return []byte{0x1b, '[', 'M', 32 + code, 32 + byte(x), 32 + byte(y)}
}

// QueueScroll scrolls the pane's own history by delta lines (positive is
// up into the past), coalesced like a wheel event. The workspace uses it
// when the wheel lands on a pane whose guest did not enable mouse
// tracking.
func (p Pane) QueueScroll(delta int) tea.Cmd {
	return p.term.QueueScroll(delta)
}

// Init starts the frame pump.
func (p Pane) Init() tea.Cmd { return p.term.Init() }

// Update forwards messages to the terminal widget. While focused, every
// key except the release key becomes terminal input.
func (p Pane) Update(msg tea.Msg) (Pane, tea.Cmd) {
	var cmd tea.Cmd
	p.term, cmd = p.term.Update(msg)
	return p, cmd
}

// SetSize resizes the box, the emulator, and the underlying PTY.
func (p Pane) SetSize(cols, rows int) (Pane, tea.Cmd) {
	var cmd tea.Cmd
	p.term, cmd = p.term.SetSize(cols, rows)
	return p, cmd
}

// View renders the terminal, exactly the box size set by SetSize.
func (p Pane) View() string { return p.term.View() }

// Size reports the terminal box dimensions.
func (p Pane) Size() (int, int) { return p.term.Size() }

// Cursor reports the cursor relative to the pane's top-left cell. ok is
// false while scrolled back or when the cursor is outside the box.
func (p Pane) Cursor() (int, int, bool) { return p.term.Cursor() }

// Focus routes the keyboard to the pane; Blur hands it back. Use these
// on a Pane you keep by pointer; in the bubble flow, the release key
// blurs the terminal inside Update.
func (p *Pane) Focus() { p.term.Focus() }
func (p *Pane) Blur()  { p.term.Blur() }

// Focused reports whether the keyboard currently reaches the pane.
func (p Pane) Focused() bool { return p.term.Focused() }

// ReleaseKey is the key a focused pane gives back to the app.
func (p Pane) ReleaseKey() string { return p.term.ReleaseKey() }

// Write sends bytes to the pane's process directly, independent of focus.
func (p Pane) Write(data []byte) error { return p.term.Write(data) }

// Output exposes the transport's post-attach output stream (raw guest
// bytes). It closes when the session ends.
func (p Pane) Output() <-chan []byte { return p.transport.Output() }

// ID is the pane's stable identity, used to route ExitMsg.
func (p Pane) ID() int64 { return p.term.ID() }

// Title is the pane's display name: the command's base name.
func (p Pane) Title() string { return p.title }

// Alive reports whether the process is still running. Panes without a
// session handle are assumed alive (cannot happen with ptyadapter today).
func (p Pane) Alive() bool { return p.session == nil || p.session.Alive() }

// ExitCmd waits for the process to exit and then returns ExitMsg. Run it
// alongside Init. Panes without a session handle return a nil Cmd.
func (p Pane) ExitCmd() tea.Cmd {
	if p.session == nil {
		return nil
	}
	id := p.term.ID()
	done := p.session.Done()
	session := p.session
	return func() tea.Msg {
		<-done
		return ExitMsg{PaneID: id, Code: session.ExitCode()}
	}
}

// Close kills the process and detaches the widget. Safe to call more than
// once.
func (p Pane) Close() { p.term.Close() }

// GuestTitle is the title the guest set via OSC, if any. nvim sets the
// file it edits; shells set the running command.
func (p Pane) GuestTitle() string {
	if p.session == nil {
		return ""
	}
	return p.session.Title()
}
