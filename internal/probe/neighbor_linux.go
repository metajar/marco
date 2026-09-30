package probe

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"strings"
)

// readNeighbors uses `ip neigh`, which reports NUD states, falling back to
// /proc/net/arp when iproute2 isn't installed.
func readNeighbors() ([]Neighbor, error) {
	out, err := exec.Command("ip", "-4", "neigh", "show").Output()
	if err != nil {
		return readProcARP()
	}
	var res []Neighbor
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		n := Neighbor{IP: f[0], State: strings.ToLower(f[len(f)-1])}
		for i := 0; i < len(f)-1; i++ {
			if f[i] == "lladdr" {
				n.MAC = NormalizeMAC(f[i+1])
			}
		}
		res = append(res, n)
	}
	return res, nil
}

func readProcARP() ([]Neighbor, error) {
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var res []Neighbor
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		// IP address  HW type  Flags  HW address  Mask  Device
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		n := Neighbor{IP: f[0], MAC: NormalizeMAC(f[3]), State: "incomplete"}
		if f[2] == "0x2" || f[2] == "0x6" {
			n.State = "complete"
		}
		res = append(res, n)
	}
	return res, nil
}

func flushNeighbor(ip string) error {
	return exec.Command("ip", "neigh", "flush", "to", ip).Run()
}
