// Package pane wraps a spanreed terminal: a process on a raw PTY plus the
// emulator view. One Pane is one live terminal.
package pane

import (
	"bytes"
	"context"
	"path/filepath"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	// bracketedPaste tracks whether the guest enabled DECSET 2004; a
	// pointer because Pane is copied around and the watcher goroutine
	// writes it concurrently.
	bracketedPaste *atomic.Bool
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
		term:           spanreed.New(transport, cols, rows, opts...),
		transport:      transport,
		session:        session,
		title:          filepath.Base(spec.Command),
		bracketedPaste: &atomic.Bool{},
	}
	if session != nil {
		go watchPasteMode(session, p.bracketedPaste)
	}
	return p, nil
}

// watchPasteMode tracks the guest's bracketed-paste mode (DECSET 2004)
// from a second subscription on the session, so pane.Paste can bracket
// pastes exactly when the guest expects them.
func watchPasteMode(session *windrunner.Session, enabled *atomic.Bool) {
	_, sub := session.Attach(8)
	const (
		set2004 = "\x1b[?2004h"
		res2004 = "\x1b[?2004l"
	)
	var carry []byte
	for chunk := range sub.Output() {
		carry = append(carry, chunk...)
		for {
			i := bytes.Index(carry, []byte(set2004))
			j := bytes.Index(carry, []byte(res2004))
			if i < 0 && j < 0 {
				break
			}
			if i >= 0 && (j < 0 || i < j) {
				enabled.Store(true)
				carry = carry[i+len(set2004):]
			} else {
				enabled.Store(false)
				carry = carry[j+len(res2004):]
			}
		}
		if len(carry) > 16 {
			carry = carry[len(carry)-16:]
		}
	}
}

// Paste delivers pasted text to the guest: wrapped in bracketed-paste
// markers when the guest enabled DECSET 2004 (TUIs like nvim and pi),
// raw bytes otherwise, so plain programs never see stray escapes.
func (p Pane) Paste(text string) error {
	if p.bracketedPaste != nil && p.bracketedPaste.Load() {
		return p.Write([]byte("\x1b[200~" + text + "\x1b[201~"))
	}
	return p.Write([]byte(text))
}

// BracketedPaste reports whether the guest asked for bracketed paste.
func (p Pane) BracketedPaste() bool {
	return p.bracketedPaste != nil && p.bracketedPaste.Load()
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
