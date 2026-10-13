//go:build darwin

package stats

import "testing"

func TestParseTopIdle(t *testing.T) {
	out := `Processes: 500 total
CPU usage: 10.41% user, 13.54% sys, 76.4% idle
CPU usage: 12.16% user, 4.70% sys, 83.14% idle
`
	idle, ok := parseTopIdle(out)
	if !ok {
		t.Fatal("expected to find a CPU usage line")
	}
	if idle != 83.14 {
		t.Errorf("idle = %v, want 83.14 (the last line wins)", idle)
	}
}

func TestParseTopIdleMissing(t *testing.T) {
	if _, ok := parseTopIdle("no cpu here"); ok {
		t.Error("expected no idle parse")
	}
}

func TestParseVMStat(t *testing.T) {
	out := `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                               7561.
Pages active:                           153313.
Pages inactive:                         151788.
Pages speculative:                         641.
Pages wired down:                        77660.
`
	free, inactive, speculative, pageSize, ok := parseVMStat(out)
	if !ok {
		t.Fatal("expected vm_stat to parse")
	}
	if pageSize != 16384 {
		t.Errorf("pageSize = %d, want 16384", pageSize)
	}
	if free != 7561 || inactive != 151788 || speculative != 641 {
		t.Errorf("counts = %d/%d/%d, want 7561/151788/641", free, inactive, speculative)
	}
}

func TestParseVMStatBadHeader(t *testing.T) {
	if _, _, _, _, ok := parseVMStat("garbage"); ok {
		t.Error("expected a headerless vm_stat to fail")
	}
}

func TestParseNetstatIB(t *testing.T) {
	// The real -ib shape: each interface's counters appear once on its
	// "<Link#N>" row, repeated per address below, and a row may omit the
	// Address column (gif0*) so columns shift.
	out := `Name       Mtu   Network       Address            Ipkts Ierrs     Ibytes    Opkts Oerrs     Obytes  Coll
lo0        16384 <Link#1>                       5289483     0 51991706154  5289483     0 51991706154     0
lo0        16384 127           localhost        5289483     - 51991706154  5289483     - 51991706154     -
gif0*      1280  <Link#2>                             0     0          0        0     0          0     0
en0        1500  <Link#6>    aa:bb:cc:dd:ee:ff   1000     0    1048576     2000     0      2048     0
en0        1500  192.168.1.5   192.168.1.5        1000     -    1048576     2000     -      2048     -
`
	rx, tx, ok := parseNetstatIB(out)
	if !ok {
		t.Fatal("expected netstat -ib to parse")
	}
	if rx != 1048576 {
		t.Errorf("rx = %d, want 1048576 (loopback and gif0 excluded, en0 once)", rx)
	}
	if tx != 2048 {
		t.Errorf("tx = %d, want 2048", tx)
	}
}
