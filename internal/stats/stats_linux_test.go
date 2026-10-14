//go:build linux

package stats

import "testing"

func TestParseProcStat(t *testing.T) {
	// user nice system idle iowait irq softirq steal
	idle, total, ok := parseProcStat("cpu  100 2 50 800 30 5 5 0 0 0")
	if !ok {
		t.Fatal("expected a cpu line to parse")
	}
	if idle != 830 { // idle + iowait
		t.Errorf("idle = %d, want 830", idle)
	}
	if total != 992 { // sum of the first eight fields
		t.Errorf("total = %d, want 992", total)
	}
}

func TestParseProcStatRejectsAggregateMismatch(t *testing.T) {
	if _, _, ok := parseProcStat("cpu0 1 2 3"); ok {
		t.Error("a per-cpu line without the aggregate prefix should not parse")
	}
}

func TestParseMeminfo(t *testing.T) {
	data := "MemTotal:       16384000 kB\nMemFree:         1000000 kB\nMemAvailable:    8192000 kB\n"
	used, total, ok := parseMeminfo(data)
	if !ok {
		t.Fatal("expected meminfo to parse")
	}
	if total != 16384000*1024 {
		t.Errorf("total = %d", total)
	}
	if used != (16384000-8192000)*1024 {
		t.Errorf("used = %d", used)
	}
}

func TestParseMeminfoMissingAvailable(t *testing.T) {
	if _, _, ok := parseMeminfo("MemTotal: 100 kB\n"); ok {
		t.Error("expected missing MemAvailable to fail")
	}
}

func TestParseProcNetDev(t *testing.T) {
	data := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 9999999    100    0    0    0     0          0         0  9999999    100    0    0    0     0       0          0
  eth0: 1048576   2000    0    0    0     0          0         0     2048   2000    0    0    0     0       0          0
 wlan0:    4096     50    0    0    0     0          0         0     1024     50    0    0    0     0       0          0
`
	rx, tx, ok := parseProcNetDev(data)
	if !ok {
		t.Fatal("expected /proc/net/dev to parse")
	}
	if rx != 1048576+4096 {
		t.Errorf("rx = %d, want %d (loopback excluded)", rx, 1048576+4096)
	}
	if tx != 2048+1024 {
		t.Errorf("tx = %d, want %d", tx, 2048+1024)
	}
}

func TestParseBatterySysfs(t *testing.T) {
	pct, charging, ok := parseBatterySysfs("82\n", "Discharging\n")
	if !ok || pct != 82 || charging {
		t.Errorf("discharging = %d/%v/%v, want 82/false/true", pct, charging, ok)
	}
	if pct, charging, ok = parseBatterySysfs("100\n", "Full\n"); !ok || pct != 100 || !charging {
		t.Errorf("full = %d/%v/%v, want 100/true/true", pct, charging, ok)
	}
	if pct, charging, ok = parseBatterySysfs("55\n", "Not charging\n"); !ok || pct != 55 || !charging {
		t.Errorf("plugged not charging = %d/%v/%v, want 55/true/true", pct, charging, ok)
	}
	if _, _, ok = parseBatterySysfs("garbage\n", "Discharging\n"); ok {
		t.Error("an unparseable capacity should not report a battery")
	}
}
