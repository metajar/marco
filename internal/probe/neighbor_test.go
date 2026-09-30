package probe

import "testing"

func TestNormalizeMAC(t *testing.T) {
	cases := map[string]string{
		"3c:22:fb:5:a:1":    "3c:22:fb:05:0a:01", // macOS arp drops leading zeros
		"3C-22-FB-05-0A-01": "3c:22:fb:05:0a:01",
		"3c22fb050a01":      "3c:22:fb:05:0a:01",
		"ff:ff:ff:ff:ff:ff": "",
		"(incomplete)":      "",
		"":                  "",
		"zz:22:fb:05:0a:01": "",
	}
	for in, want := range cases {
		if got := NormalizeMAC(in); got != want {
			t.Errorf("NormalizeMAC(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsPrivateMAC(t *testing.T) {
	if !IsPrivateMAC("da:a1:19:00:00:01") {
		t.Error("da:… has the locally administered bit set")
	}
	if IsPrivateMAC("3c:22:fb:05:0a:01") {
		t.Error("3c:… is a vendor-assigned MAC")
	}
}

func TestNeighborConfirmed(t *testing.T) {
	cases := []struct {
		n       Neighbor
		flushed bool
		want    bool
	}{
		{Neighbor{MAC: "aa:bb:cc:dd:ee:ff", State: "reachable"}, false, true},
		{Neighbor{MAC: "aa:bb:cc:dd:ee:ff", State: "complete"}, false, true},
		{Neighbor{MAC: "aa:bb:cc:dd:ee:ff", State: "stale"}, false, false}, // could be minutes old
		{Neighbor{MAC: "aa:bb:cc:dd:ee:ff", State: "stale"}, true, true},
		{Neighbor{State: "failed"}, true, false},
		{Neighbor{State: "incomplete"}, false, false},
	}
	for _, c := range cases {
		if got := c.n.confirmed(c.flushed); got != c.want {
			t.Errorf("%+v flushed=%v: got %v, want %v", c.n, c.flushed, got, c.want)
		}
	}
}
