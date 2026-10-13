//go:build linux

package stats

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// Sample reports CPU utilisation from the /proc/stat tick deltas since the
// previous sample, memory from /proc/meminfo, and network throughput from
// /proc/net/dev.
func (c *Collector) Sample() (Usage, error) {
	var u Usage
	idle, total, ok := readProcStat()
	if !ok {
		return u, errors.New("stats: could not read /proc/stat")
	}
	if c.hasPrev && total > c.prevTotal {
		dTotal := total - c.prevTotal
		dIdle := idle - c.prevIdle
		busy := dTotal - min(dIdle, dTotal)
		u.CPUPercent = 100 * float64(busy) / float64(dTotal)
	}
	c.prevIdle, c.prevTotal, c.hasPrev = idle, total, true

	used, memTotal, ok := readMeminfo()
	if !ok {
		return u, errors.New("stats: could not read /proc/meminfo")
	}
	u.MemUsed, u.MemTotal = used, memTotal

	if rx, tx, ok := readNetDev(); ok {
		u.NetRx, u.NetTx, u.NetOK = c.netRates(rx, tx, time.Now())
	}
	return u, nil
}

// readProcStat returns the idle (idle+iowait) and total tick counts from the
// aggregate "cpu" line of /proc/stat.
func readProcStat() (idle, total uint64, ok bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		return parseProcStat(line)
	}
	return 0, 0, false
}

// parseProcStat sums the tick fields of an aggregate "cpu" line: user, nice,
// system, idle, iowait, irq, softirq, steal.
func parseProcStat(line string) (idle, total uint64, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 6 || fields[0] != "cpu" {
		return 0, 0, false
	}
	vals := make([]uint64, 0, 8)
	for _, f := range fields[1:] {
		n, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			break
		}
		vals = append(vals, n)
	}
	if len(vals) < 4 {
		return 0, 0, false
	}
	for i := 0; i < len(vals) && i < 8; i++ {
		total += vals[i]
	}
	idle = vals[3]
	if len(vals) > 4 {
		idle += vals[4] // iowait counts as idle
	}
	return idle, total, true
}

// readMeminfo returns used and total bytes from /proc/meminfo, using
// MemAvailable as the reclaimable figure.
func readMeminfo() (used, total uint64, ok bool) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	return parseMeminfo(string(data))
}

// readNetDev returns the summed receive/transmit byte counters over every
// non-loopback interface from /proc/net/dev.
func readNetDev() (rx, tx uint64, ok bool) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0, false
	}
	return parseProcNetDev(string(data))
}

// parseProcNetDev sums the rx and tx byte columns of /proc/net/dev rows,
// skipping the loopback interface. Each row is "iface: rxbytes … txbytes …".
func parseProcNetDev(data string) (rx, tx uint64, ok bool) {
	for _, line := range strings.Split(data, "\n") {
		name, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" || name == "lo" {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, err1 := strconv.ParseUint(f[0], 10, 64)
		t, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		rx, tx, ok = rx+r, tx+t, true
	}
	return rx, tx, ok
}

// parseMeminfo reads MemTotal and MemAvailable (both in kB).
func parseMeminfo(data string) (used, total uint64, ok bool) {
	var available uint64
	var gotTotal, gotAvail bool
	for _, line := range strings.Split(data, "\n") {
		key, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			total, gotTotal = n*1024, true
		case "MemAvailable":
			available, gotAvail = n*1024, true
		}
	}
	if !gotTotal || !gotAvail || available > total {
		return 0, 0, false
	}
	return total - available, total, true
}
