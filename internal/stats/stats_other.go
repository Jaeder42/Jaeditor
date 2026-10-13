//go:build !darwin && !linux

package stats

import "errors"

// Sample is unsupported on this platform; the resource bar shows placeholders.
func (c *Collector) Sample() (Usage, error) {
	return Usage{}, errors.New("stats: unsupported platform")
}
