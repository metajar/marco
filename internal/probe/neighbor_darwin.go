package probe

import (
	"net"
	"os/exec"
	"syscall"

	"golang.org/x/net/route"
)

// readNeighbors reads the ARP table straight from the kernel routing sysctl
// (what `arp -an` does under the hood). macOS ARP has no NUD states, so a
// resolved entry is reported as "complete".
func readNeighbors() ([]Neighbor, error) {
	rib, err := route.FetchRIB(syscall.AF_INET, route.RIBTypeRoute, 0)
	if err != nil {
		return nil, err
	}
	msgs, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return nil, err
	}
	byIP := map[string]int{}
	var res []Neighbor
	for _, m := range msgs {
		rm, ok := m.(*route.RouteMessage)
		if !ok || rm.Flags&syscall.RTF_LLINFO == 0 || len(rm.Addrs) <= syscall.RTAX_GATEWAY {
			continue
		}
		dst, ok := rm.Addrs[syscall.RTAX_DST].(*route.Inet4Addr)
		if !ok {
			continue
		}
		n := Neighbor{IP: net.IP(dst.IP[:]).String(), State: "incomplete"}
		if ll, ok := rm.Addrs[syscall.RTAX_GATEWAY].(*route.LinkAddr); ok && len(ll.Addr) == 6 {
			if n.MAC = NormalizeMAC(net.HardwareAddr(ll.Addr).String()); n.MAC != "" {
				n.State = "complete"
				if rm.Flags&syscall.RTF_STATIC != 0 {
					n.State = "permanent"
				}
			}
		}
		// The same address can appear once per interface (Wi-Fi and Ethernet
		// on one LAN); keep the resolved one.
		if i, seen := byIP[n.IP]; seen {
			if res[i].MAC == "" && n.MAC != "" {
				res[i] = n
			}
			continue
		}
		byIP[n.IP] = len(res)
		res = append(res, n)
	}
	return res, nil
}

func flushNeighbor(ip string) error {
	return exec.Command("arp", "-d", ip).Run()
}
