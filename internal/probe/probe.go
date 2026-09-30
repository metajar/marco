// Package probe decides whether a device is present on the local network.
//
// Phones are hard to detect: when the screen is off they usually ignore ICMP
// pings and close most ports, but their Wi-Fi chip still answers ARP (often
// the access point answers on its behalf). A probe therefore fires several
// signals at once — ICMP echo, TCP connects (a refused connection still proves
// the host is there), and a UDP nudge that forces the kernel to ARP for the
// address — and finally consults the kernel's neighbor table.
package probe

import (
	"context"
	"errors"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	probing "github.com/prometheus-community/pro-bing"
)

type Result struct {
	Online  bool
	Method  string // icmp, tcp/<port>, arp
	Latency time.Duration
	MAC     string // learned from the neighbor table when available
}

// Options control how a single probe behaves.
type Options struct {
	Ports   []int
	Timeout time.Duration
	UseARP  bool
}

// Probe checks whether ip is currently present on the network.
func Probe(ctx context.Context, ip string, opt Options) Result {
	if net.ParseIP(ip) == nil {
		return Result{}
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 4 * time.Second
	}
	start := time.Now()

	// A fresh neighbor entry is only trustworthy if the stale one is gone first.
	flushed := false
	if opt.UseARP && canFlush() {
		flushed = flushNeighbor(ip) == nil
	}

	pctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()
	found := make(chan Result, len(opt.Ports)+2)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if rtt, ok := ping(pctx, ip, opt.Timeout); ok {
			found <- Result{Online: true, Method: "icmp", Latency: rtt}
		}
	}()
	for _, port := range opt.Ports {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			t0 := time.Now()
			var d net.Dialer
			conn, err := d.DialContext(pctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
			if err == nil {
				conn.Close()
			}
			if err == nil || errors.Is(err, syscall.ECONNREFUSED) {
				found <- Result{Online: true, Method: "tcp/" + strconv.Itoa(port), Latency: time.Since(t0)}
			}
		}(port)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		nudge(pctx, ip)
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	var res Result
	select {
	case res = <-found:
	case <-done:
		select {
		case res = <-found:
		default:
		}
	}
	cancel()

	if !opt.UseARP {
		return res
	}
	if res.Online {
		if n, ok := lookupNeighbor(ip); ok {
			res.MAC = n.MAC
		}
		return res
	}
	// Give ARP a little longer to resolve than the other probes had.
	deadline := time.Now().Add(3 * time.Second)
	for {
		n, ok := lookupNeighbor(ip)
		if ok && n.confirmed(flushed) {
			return Result{Online: true, Method: "arp", Latency: time.Since(start), MAC: n.MAC}
		}
		if (ok && n.State == "failed") || time.Now().After(deadline) || ctx.Err() != nil {
			return Result{}
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func ping(ctx context.Context, ip string, timeout time.Duration) (time.Duration, bool) {
	p, err := probing.NewPinger(ip)
	if err != nil {
		return 0, false
	}
	p.Count = 3
	p.Interval = 500 * time.Millisecond
	p.Timeout = timeout
	// Unprivileged (UDP) ICMP works on macOS and on Linux when
	// net.ipv4.ping_group_range allows it; root can use raw sockets.
	p.SetPrivileged(runtime.GOOS == "linux" && os.Geteuid() == 0)
	p.SetLogger(probing.NoopLogger{})
	p.OnRecv = func(*probing.Packet) { p.Stop() }
	if err := p.RunWithContext(ctx); err != nil {
		return 0, false
	}
	st := p.Statistics()
	return st.AvgRtt, st.PacketsRecv > 0
}

// nudge sends harmless UDP datagrams to the discard port. The payload doesn't
// matter; sending it makes the kernel resolve the address with ARP.
func nudge(ctx context.Context, ip string) {
	conn, err := net.Dial("udp4", net.JoinHostPort(ip, "9"))
	if err != nil {
		return
	}
	defer conn.Close()
	for i := 0; i < 3; i++ {
		conn.Write([]byte("marco"))
		select {
		case <-ctx.Done():
			return
		case <-time.After(700 * time.Millisecond):
		}
	}
}

func canFlush() bool { return os.Geteuid() == 0 }

// FindIPByMAC looks up the current address of a MAC in the neighbor table,
// which lets marco follow a device whose DHCP lease changed.
func FindIPByMAC(mac string) string {
	mac = NormalizeMAC(mac)
	if mac == "" {
		return ""
	}
	neighbors, err := readNeighbors()
	if err != nil {
		return ""
	}
	for _, n := range neighbors {
		if n.MAC == mac && n.State != "failed" && n.State != "incomplete" {
			return n.IP
		}
	}
	return ""
}
