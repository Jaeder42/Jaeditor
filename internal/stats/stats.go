// Package stats reports machine resource usage for the workspace's top
// resource bar: whole-machine CPU utilisation and memory used/total.
//
// There is no portable, cgo-free API for machine-wide CPU ticks, so the
// platforms that have one use their own sources: macOS shells out to
// top(1) and vm_stat(1), Linux reads /proc. A Collector keeps whatever state
// a platform needs between samples (Linux derives CPU% from tick deltas), so
// one Collector should back a whole session.
package stats

import "time"

// Usage is one resource sample. CPUPercent is machine-wide utilisation across
// all cores (0-100), matching what top(1) reports. Memory is in bytes.
// NetRx/NetTx are network throughput in bytes per second, valid only when
// NetOK is set (the first sample has no delta to report). Battery is valid
// only when BatteryOK is set — a machine without one (a desktop) leaves it
// unset.
type Usage struct {
	CPUPercent float64
	MemUsed    uint64
	MemTotal   uint64
	NetRx      uint64
	NetTx      uint64
	NetOK      bool

	BatteryPercent  int
	BatteryCharging bool
	BatteryOK       bool
}

// Collector samples machine usage.
type Collector struct {
	hasPrev             bool
	prevIdle, prevTotal uint64

	hasNet         bool
	prevRx, prevTx uint64
	prevNetAt      time.Time
}

// New returns a Collector ready for sampling.
func New() *Collector { return &Collector{} }

// netRates turns cumulative interface counters into per-second rates since
// the previous call, updating the collector's stored counters. ok is false on
// the first call, if counters went backwards (interface reset), or if too
// little time has passed to divide safely.
func (c *Collector) netRates(rx, tx uint64, now time.Time) (drx, dtx uint64, ok bool) {
	if c.hasNet && rx >= c.prevRx && tx >= c.prevTx {
		if secs := now.Sub(c.prevNetAt).Seconds(); secs > 0.05 {
			drx = uint64(float64(rx-c.prevRx) / secs)
			dtx = uint64(float64(tx-c.prevTx) / secs)
			ok = true
		}
	}
	c.prevRx, c.prevTx, c.prevNetAt, c.hasNet = rx, tx, now, true
	return drx, dtx, ok
}
