package probe

import (
	"context"
	"encoding/binary"
	"net"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Found is a device discovered on the local network.
type Found struct {
	IP         string
	MAC        string
	Hostname   string
	PrivateMAC bool
}

// LocalSubnets returns the IPv4 networks this host is attached to, limited to
// /22 or smaller so a sweep stays quick.
func LocalSubnets() []*net.IPNet {
	var out []*net.IPNet
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			ones, bits := ipn.Mask.Size()
			if bits != 32 || ones < 22 || ones > 30 {
				continue
			}
			sn := &net.IPNet{IP: ipn.IP.Mask(ipn.Mask).To4(), Mask: ipn.Mask}
			if !slices.ContainsFunc(out, func(o *net.IPNet) bool { return o.String() == sn.String() }) {
				out = append(out, sn)
			}
		}
	}
	return out
}

func hosts(n *net.IPNet) []string {
	ones, _ := n.Mask.Size()
	base := binary.BigEndian.Uint32(n.IP.To4())
	size := uint32(1) << (32 - ones)
	var out []string
	for i := uint32(1); i < size-1; i++ {
		b := make(net.IP, 4)
		binary.BigEndian.PutUint32(b, base+i)
		out = append(out, b.String())
	}
	return out
}

// Scan sweeps the local subnets so the kernel ARPs every address, then reports
// everything that answered.
func Scan(ctx context.Context) []Found {
	subnets := LocalSubnets()
	sem := make(chan struct{}, 128)
	var wg sync.WaitGroup
	for _, sn := range subnets {
		for _, ip := range hosts(sn) {
			wg.Add(1)
			sem <- struct{}{}
			go func(ip string) {
				defer wg.Done()
				defer func() { <-sem }()
				if conn, err := net.Dial("udp4", net.JoinHostPort(ip, "9")); err == nil {
					conn.Write([]byte("marco"))
					conn.Close()
				}
			}(ip)
		}
	}
	wg.Wait()
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(3 * time.Second):
	}

	neighbors, _ := readNeighbors()
	inSubnet := func(ip net.IP) bool {
		for _, sn := range subnets {
			if sn.Contains(ip) {
				return true
			}
		}
		return false
	}
	var out []Found
	for _, n := range neighbors {
		ip := net.ParseIP(n.IP)
		if n.MAC == "" || ip == nil || !inSubnet(ip) || n.State == "failed" || n.State == "incomplete" {
			continue
		}
		out = append(out, Found{IP: n.IP, MAC: n.MAC, PrivateMAC: IsPrivateMAC(n.MAC)})
	}

	// Reverse DNS often yields names like "Emmas-iPhone.lan" from the router.
	var mu sync.Mutex
	resolver := &net.Resolver{}
	for i := range out {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			defer cancel()
			if names, err := resolver.LookupAddr(lctx, out[i].IP); err == nil && len(names) > 0 {
				mu.Lock()
				out[i].Hostname = strings.TrimSuffix(names[0], ".")
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	sort.Slice(out, func(a, b int) bool {
		return binary.BigEndian.Uint32(net.ParseIP(out[a].IP).To4()) < binary.BigEndian.Uint32(net.ParseIP(out[b].IP).To4())
	})
	return out
}
