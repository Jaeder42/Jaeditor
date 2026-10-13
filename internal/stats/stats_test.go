package stats

import (
	"testing"
	"time"
)

func TestNetRates(t *testing.T) {
	c := New()
	now := time.Unix(1000, 0)
	if _, _, ok := c.netRates(1000, 2000, now); ok {
		t.Error("the first sample has no delta to report")
	}
	drx, dtx, ok := c.netRates(4000, 5000, now.Add(2*time.Second))
	if !ok {
		t.Fatal("the second sample should produce a rate")
	}
	if drx != 1500 || dtx != 1500 {
		t.Errorf("rates = %d/%d, want 1500/1500 bytes per second", drx, dtx)
	}
	// A counter reset (interface down/up) must not produce a huge rate.
	if _, _, ok := c.netRates(10, 10, now.Add(4*time.Second)); ok {
		t.Error("a counter reset should not produce a rate")
	}
}
