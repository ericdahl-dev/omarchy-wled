package daemon

import (
	"time"

	"github.com/ericdahl-dev/omarchy-wled/internal/source"
	"github.com/ericdahl-dev/omarchy-wled/internal/transport"
)

// PollFallback is the fsnotify alternative: it polls source.Sentinel() on a fixed interval.
type PollFallback struct {
	// TickInterval is the sleep between sentinel checks (default 1s if zero).
	TickInterval time.Duration
}

// Run blocks forever, calling PollTick when the sentinel string changes.
func (p *PollFallback) Run(t transport.Transport, brightness int, saturation float64, src source.Source, opts PushOptions) {
	iv := p.TickInterval
	if iv <= 0 {
		iv = time.Second
	}
	tracker := &DedupeTracker{}
	var previousSentinel string
	for {
		PollTick(t, brightness, saturation, src, tracker, &previousSentinel, opts)
		time.Sleep(iv)
	}
}

// RunSentinelPollLoop is the default poll fallback (1s interval).
func RunSentinelPollLoop(t transport.Transport, brightness int, saturation float64, src source.Source, opts PushOptions) {
	(&PollFallback{}).Run(t, brightness, saturation, src, opts)
}
