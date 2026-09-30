package probe

import (
	"fmt"
	"strconv"
	"strings"
)

// Neighbor is an entry from the kernel's ARP / neighbor table.
type Neighbor struct {
	IP    string
	MAC   string
	State string // reachable, stale, delay, probe, failed, incomplete, permanent, complete
}

// confirmed reports whether the entry proves the device answered recently.
func (n Neighbor) confirmed(flushed bool) bool {
	if n.MAC == "" {
		return false
	}
	switch n.State {
	case "reachable", "complete":
		return true
	case "stale", "delay", "probe":
		// Only meaningful if we deleted the old entry before probing.
		return flushed
	}
	return false
}

func lookupNeighbor(ip string) (Neighbor, bool) {
	neighbors, err := readNeighbors()
	if err != nil {
		return Neighbor{}, false
	}
	for _, n := range neighbors {
		if n.IP == ip {
			return n, true
		}
	}
	return Neighbor{}, false
}

// NormalizeMAC returns a lower-case, zero-padded, colon separated MAC
// ("3c:22:fb:05:0a:01"), or "" if s isn't a MAC address.
func NormalizeMAC(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ':' || r == '-' || r == '.' })
	if len(parts) == 1 && len(s) == 12 {
		parts = []string{s[0:2], s[2:4], s[4:6], s[6:8], s[8:10], s[10:12]}
	}
	if len(parts) != 6 {
		return ""
	}
	out := make([]string, 6)
	for i, p := range parts {
		v, err := strconv.ParseUint(p, 16, 8)
		if err != nil {
			return ""
		}
		out[i] = fmt.Sprintf("%02x", v)
	}
	mac := strings.Join(out, ":")
	if mac == "00:00:00:00:00:00" || mac == "ff:ff:ff:ff:ff:ff" {
		return ""
	}
	return mac
}

// IsPrivateMAC reports whether the MAC is locally administered, which is what
// iOS and Android use for "private Wi-Fi address" randomization.
func IsPrivateMAC(mac string) bool {
	mac = NormalizeMAC(mac)
	if mac == "" {
		return false
	}
	b, _ := strconv.ParseUint(mac[0:2], 16, 8)
	return b&0x02 != 0
}
