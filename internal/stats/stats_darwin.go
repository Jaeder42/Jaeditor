//go:build darwin

package stats

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Sample reports CPU utilisation from top(1), memory from vm_stat(1) plus
// hw.memsize, and network throughput from netstat(1). macOS exposes no
// cgo-free API for whole-machine CPU ticks, so the platform tools are the
// source of truth.
func (c *Collector) Sample() (Usage, error) {
	var u Usage
	cpu, err := darwinCPU()
	if err != nil {
		return u, err
	}
	used, total, err := darwinMem()
	if err != nil {
		return u, err
	}
	u.CPUPercent = cpu
	u.MemUsed, u.MemTotal = used, total

	if rx, tx, ok := darwinNet(); ok {
		u.NetRx, u.NetTx, u.NetOK = c.netRates(rx, tx, time.Now())
	}

	// Best-effort: a desktop has no battery and pmset still succeeds with no
	// percentage line, so a parse failure just leaves the field unset.
	if pct, charging, ok := darwinBattery(); ok {
		u.BatteryPercent, u.BatteryCharging, u.BatteryOK = pct, charging, true
	}
	return u, nil
}

// darwinBattery reads the battery charge and whether it is on the charger
// from pmset(1).
func darwinBattery() (percent int, charging, ok bool) {
	out, err := exec.Command("pmset", "-g", "batt").Output()
	if err != nil {
		return 0, false, false
	}
	return parsePmsetBatt(string(out))
}

// parsePmsetBatt extracts the level and charging state from `pmset -g batt`.
// The battery line looks like " -InternalBattery-0 (id=…)	82%; charging;
// 0:30 remaining present: true"; the "Now drawing from …" header carries no
// percentage and is skipped.
func parsePmsetBatt(out string) (percent int, charging, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		pct := strings.IndexByte(line, '%')
		if pct < 0 {
			continue
		}
		start := pct
		for start > 0 && line[start-1] >= '0' && line[start-1] <= '9' {
			start--
		}
		n, err := strconv.Atoi(line[start:pct])
		if err != nil {
			continue
		}
		// Plugged in reads "charging", "charged", or "AC attached"; only
		// "discharging" means the battery is running the machine.
		return n, !strings.Contains(line, "discharging"), true
	}
	return 0, false, false
}

// darwinCPU runs top twice (the first sample is the average since boot, the
// second is over the interval) and returns 100 - idle.
func darwinCPU() (float64, error) {
	out, err := exec.Command("top", "-l", "2", "-n", "0").Output()
	if err != nil {
		return 0, err
	}
	idle, ok := parseTopIdle(string(out))
	if !ok {
		return 0, errors.New("stats: could not parse top(1) output")
	}
	return 100 - idle, nil
}

// parseTopIdle reads the last "CPU usage:" line and extracts the idle
// percentage. "CPU usage: 12.16% user, 4.70% sys, 83.12% idle".
func parseTopIdle(out string) (float64, bool) {
	idle, found := 0.0, false
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "CPU usage:") {
			continue
		}
		fields := strings.Fields(line)
		for i, f := range fields {
			if f != "idle" || i == 0 {
				continue
			}
			if n, err := strconv.ParseFloat(strings.TrimSuffix(fields[i-1], "%"), 64); err == nil {
				idle, found = n, true
			}
		}
	}
	return idle, found
}

// darwinNet reads cumulative receive/transmit byte counters from netstat(1),
// summed over physical interfaces (loopback excluded).
func darwinNet() (rx, tx uint64, ok bool) {
	out, err := exec.Command("netstat", "-ib").Output()
	if err != nil {
		return 0, 0, false
	}
	return parseNetstatIB(string(out))
}

// parseNetstatIB sums Ibytes/Obytes over the physical interfaces in netstat
// -ib output. Each interface's real counters appear on its "<Link#N>" row;
// the rows that follow repeat them per address, so only "<Link#N>" rows
// count. A row may or may not carry an Address column, so the counters are
// read from the end of the line (… Ipkts Ierrs Ibytes Opkts Oerrs Obytes
// Coll).
func parseNetstatIB(out string) (rx, tx uint64, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || !strings.HasPrefix(f[2], "<Link#") {
			continue
		}
		if strings.HasPrefix(f[0], "lo") { // skip loopback
			continue
		}
		ibytes, err1 := strconv.ParseUint(f[len(f)-5], 10, 64)
		obytes, err2 := strconv.ParseUint(f[len(f)-2], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		rx, tx, ok = rx+ibytes, tx+obytes, true
	}
	return rx, tx, ok
}

// darwinMem returns used and total bytes. Used is total minus reclaimable
// pages (free + inactive + speculative), close to Activity Monitor's figure.
func darwinMem() (used, total uint64, err error) {
	total, err = unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0, 0, err
	}
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0, 0, err
	}
	free, inactive, speculative, pageSize, ok := parseVMStat(string(out))
	if !ok {
		return 0, 0, errors.New("stats: could not parse vm_stat(1) output")
	}
	avail := (free + inactive + speculative) * pageSize
	if avail > total {
		avail = total
	}
	return total - avail, total, nil
}

// parseVMStat reads the page size from the header and the free, inactive, and
// speculative page counts.
func parseVMStat(out string) (free, inactive, speculative, pageSize uint64, ok bool) {
	lines := strings.Split(out, "\n")
	if len(lines) == 0 {
		return 0, 0, 0, 0, false
	}
	if i := strings.Index(lines[0], "page size of "); i >= 0 {
		if fields := strings.Fields(lines[0][i+len("page size of "):]); len(fields) > 0 {
			pageSize, _ = strconv.ParseUint(fields[0], 10, 64)
		}
	}
	if pageSize == 0 {
		return 0, 0, 0, 0, false
	}
	counts := []struct {
		label string
		dst   *uint64
	}{
		{"Pages free:", &free},
		{"Pages inactive:", &inactive},
		{"Pages speculative:", &speculative},
	}
	for _, line := range lines[1:] {
		for _, c := range counts {
			if strings.HasPrefix(line, c.label) {
				v := strings.TrimRight(strings.TrimSpace(strings.TrimPrefix(line, c.label)), ".")
				if n, err := strconv.ParseUint(v, 10, 64); err == nil {
					*c.dst = n
				}
			}
		}
	}
	return free, inactive, speculative, pageSize, true
}
