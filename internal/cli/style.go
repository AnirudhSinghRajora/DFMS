package cli

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/fatih/color"
	"golang.org/x/term"
)

// Color policy values for the global --color flag.
const (
	colorAuto   = "auto"
	colorAlways = "always"
	colorNever  = "never"
)

// styler applies ANSI styling to strings using fatih/color as the underlying
// color engine. fatih/color automatically respects the NO_COLOR environment
// variable and TERM=dumb, and the --color flag (auto|always|never) provides
// explicit user control on top of that.
//
// Two flavours of output are supported:
//
//   - Plain methods (Bold, Red, …) delegate to fatih/color SprintFunc closures.
//     Use these for free-flowing output such as error and confirmation messages.
//   - The *Cell methods produce tabwriter-safe colored text by wrapping ANSI
//     escape sequences in tabwriter.Escape bytes, so text/tabwriter excludes
//     them from column-width calculations. Use these inside a tabwriter so
//     colored cells stay aligned.
type styler struct {
	enabled bool

	// fatih/color-backed sprint functions for plain (non-tabwriter) output.
	boldFn   func(a ...interface{}) string
	faintFn  func(a ...interface{}) string
	redFn    func(a ...interface{}) string
	greenFn  func(a ...interface{}) string
	yellowFn func(a ...interface{}) string

	// Pre-computed SGR opening sequences for cell (tabwriter-safe) output.
	headerSGR string // bold;cyan
	greenSGR  string // green
	yellowSGR string // yellow
	redSGR    string // red
}

// newStyler resolves the color policy for a specific destination writer. Under
// the default "auto" policy, fatih/color's built-in NO_COLOR and TERM=dumb
// detection is combined with a terminal check on w. "always" and "never"
// override all automatic detection.
func newStyler(mode string, w io.Writer) *styler {
	enabled := resolveColorPolicy(mode, w)

	// mkColor creates a fatih/color.Color with explicit enable/disable,
	// overriding the global color.NoColor so our per-invocation policy wins.
	mkColor := func(attrs ...color.Attribute) *color.Color {
		c := color.New(attrs...)
		if enabled {
			c.EnableColor()
		} else {
			c.DisableColor()
		}
		return c
	}

	return &styler{
		enabled:  enabled,
		boldFn:   mkColor(color.Bold).SprintFunc(),
		faintFn:  mkColor(color.Faint).SprintFunc(),
		redFn:    mkColor(color.FgRed).SprintFunc(),
		greenFn:  mkColor(color.FgGreen).SprintFunc(),
		yellowFn: mkColor(color.FgYellow).SprintFunc(),
		// Pre-compute SGR sequences for cell methods (used only when enabled).
		headerSGR: buildSGR(color.Bold, color.FgCyan),
		greenSGR:  buildSGR(color.FgGreen),
		yellowSGR: buildSGR(color.FgYellow),
		redSGR:    buildSGR(color.FgRed),
	}
}

// resolveColorPolicy determines whether color output should be enabled,
// combining the --color flag with fatih/color's automatic environment detection
// (NO_COLOR, TERM=dumb) and a terminal check on the destination writer.
func resolveColorPolicy(mode string, w io.Writer) bool {
	switch mode {
	case colorNever:
		return false
	case colorAlways:
		return true
	default: // auto
		// fatih/color initialises color.NoColor at package init from NO_COLOR
		// and TERM=dumb; honour that before our own terminal check.
		if color.NoColor {
			return false
		}
		return isTerminalWriter(w)
	}
}

// isTerminalWriter reports whether w is an interactive terminal. Non-file
// writers (such as the bytes.Buffer used in tests, or a pipe) are never
// terminals, which is exactly what keeps captured and piped output clean.
func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// buildSGR assembles an ANSI SGR (Select Graphic Rendition) opening sequence
// from fatih/color attribute constants. Used to pre-compute sequences for the
// cell methods, where tabwriter escaping prevents delegating to fatih/color's
// Sprint directly.
func buildSGR(attrs ...color.Attribute) string {
	seq := "\x1b["
	for i, a := range attrs {
		if i > 0 {
			seq += ";"
		}
		seq += fmt.Sprintf("%d", a)
	}
	return seq + "m"
}

// sgrReset is the ANSI SGR reset sequence that restores default terminal state.
const sgrReset = "\x1b[0m"

// ── Plain methods (delegate to fatih/color) ─────────────────────

func (s *styler) Bold(str string) string   { return s.boldFn(str) }
func (s *styler) Faint(str string) string  { return s.faintFn(str) }
func (s *styler) Red(str string) string    { return s.redFn(str) }
func (s *styler) Green(str string) string  { return s.greenFn(str) }
func (s *styler) Yellow(str string) string { return s.yellowFn(str) }

// ── Cell methods (tabwriter-safe) ───────────────────────────────
// These wrap the ANSI escape sequences in tabwriter.Escape bytes so
// text/tabwriter excludes them from column-width calculations.

func (s *styler) HeaderCell(str string) string { return s.cell(s.headerSGR, str) }
func (s *styler) GreenCell(str string) string  { return s.cell(s.greenSGR, str) }
func (s *styler) YellowCell(str string) string { return s.cell(s.yellowSGR, str) }
func (s *styler) RedCell(str string) string    { return s.cell(s.redSGR, str) }

// cell wraps str in the given SGR opening sequence and a reset, bracketing each
// escape in tabwriter.Escape bytes. When color is disabled the string is
// returned unchanged.
func (s *styler) cell(open, str string) string {
	if !s.enabled || str == "" {
		return str
	}
	esc := string([]byte{tabwriter.Escape})
	return esc + open + esc + str + esc + sgrReset + esc
}
