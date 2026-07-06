package cli

import (
	"io"
	"time"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

// progressMeter renders transfer progress using vbauerster/mpb with support for
// both determinate (known total) and indeterminate (unknown total) progress.
// When disabled (non-TTY, --quiet, --no-progress, or non-table output), every
// method is a no-op and the reader/writer wrappers return the underlying stream
// unchanged, so there is zero overhead off the terminal.
type progressMeter struct {
	progress *mpb.Progress
	bar      *mpb.Bar
	enabled  bool
	done     bool
}

// newProgressMeter creates a meter that draws to w. Enablement is decided by the
// caller (see appEnv.progressMeter) so this stays a pure rendering helper.
//
// A meter with a known total (> 0) renders a determinate bar with percentage,
// byte counters, and transfer speed. With an unknown total (<= 0) it renders a
// spinner with a byte counter and transfer speed.
func newProgressMeter(w io.Writer, label string, total int64, enabled bool) *progressMeter {
	if !enabled {
		return &progressMeter{}
	}

	p := mpb.New(
		mpb.WithOutput(w),
		mpb.WithRefreshRate(100*time.Millisecond),
	)

	var bar *mpb.Bar
	if total > 0 {
		// Determinate bar: shows a filling bar, percentage, byte counters,
		// and EWMA-smoothed transfer speed. mpb auto-detects terminal width.
		bar = p.AddBar(total,
			mpb.PrependDecorators(
				decor.Name(label, decor.WCSyncSpaceR),
			),
			mpb.AppendDecorators(
				decor.Percentage(decor.WCSyncWidth),
				decor.Name("  "),
				decor.CountersKibiByte("%.1f / %.1f"),
				decor.Name("  "),
				decor.EwmaSpeed(decor.SizeB1024(0), "%.1f", 60),
			),
		)
	} else {
		// Indeterminate spinner: shows a cycling animation, the current
		// transferred byte count, and EWMA-smoothed transfer speed. Used
		// for downloads where total size is unknown until streaming finishes.
		bar = p.AddSpinner(0,
			mpb.PrependDecorators(
				decor.Name(label, decor.WCSyncSpaceR),
			),
			mpb.AppendDecorators(
				decor.Any(func(s decor.Statistics) string {
					return formatBytes(s.Current)
				}),
				decor.Name("  "),
				decor.EwmaSpeed(decor.SizeB1024(0), "%.1f", 60),
			),
		)
	}

	return &progressMeter{progress: p, bar: bar, enabled: true}
}

// reader wraps r so that reading from it advances the meter. Used for uploads,
// where the client pulls bytes from the source file. mpb's ProxyReader provides
// EWMA timing data for accurate speed calculations and signals bar completion
// on EOF.
func (m *progressMeter) reader(r io.Reader) io.Reader {
	if !m.enabled {
		return r
	}
	return m.bar.ProxyReader(r)
}

// writer wraps w so that writing to it advances the meter. Used for downloads,
// where the client pushes bytes to the destination file. mpb's ProxyWriter
// provides EWMA timing data for accurate speed calculations.
func (m *progressMeter) writer(w io.Writer) io.Writer {
	if !m.enabled {
		return w
	}
	pw := m.bar.ProxyWriter(w)
	if pw == nil {
		// ProxyWriter returns nil if the bar's context is cancelled.
		return w
	}
	// The caller expects io.Writer (not io.WriteCloser); closing is handled
	// by Finish(). Wrap to satisfy the interface without exposing Close.
	return &writerOnly{pw}
}

// Finish signals the bar to stop and waits for the final render frame, so
// subsequent output starts on a clean line. It is safe to call more than once
// and on a disabled meter.
func (m *progressMeter) Finish() {
	if !m.enabled || m.done {
		return
	}
	m.done = true
	if m.bar != nil {
		// Abort(false) signals the bar to stop rendering while keeping its
		// last frame visible. For determinate bars that reached 100% this is
		// a no-op (ProxyReader already signalled completion on EOF). For
		// indeterminate bars (downloads) it stops the spinner at the final
		// byte count. mpb's sync.Once ensures multiple aborts are safe.
		m.bar.Abort(false)
	}
	// Wait blocks until every bar has completed its final render frame and
	// the terminal cursor has moved past the progress output.
	m.progress.Wait()
}

// writerOnly strips the Close method from an io.WriteCloser so the result
// satisfies io.Writer but not io.WriteCloser. This prevents callers from
// accidentally closing the proxy writer, which is the meter's responsibility.
type writerOnly struct{ io.Writer }
