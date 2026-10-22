package config

// Whether the console's port is reachable by anything but this machine.
//
// It is a security decision, so it is tested as one, including the addresses
// that LOOK local and are not.
//
// The two that matter most are the empty host (":8443"), which is every
// interface rather than this machine, and an unparsable address, which is not
// evidence of loopback at all.

import "testing"

func TestIsLoopbackListener(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want bool
		why  string
	}{
		{"127.0.0.1:8443", true, "the IPv4 loopback"},
		{"[::1]:8443", true, "the IPv6 loopback"},
		{"localhost:8443", true, "the name for this machine"},
		{"LOCALHOST:8443", true, "the name, in a different case"},
		{"0.0.0.0:8443", false, "every interface, which is every machine this one can reach"},
		{"[::]:8443", false, "every interface, in IPv6"},
		{":8443", false, "no host at all, which is every interface"},
		{"", false, "no address"},
		{"not-an-address", false, "an address that does not parse is not evidence of loopback"},
		{"192.168.1.10:8443", false, "a routable address"},
	} {
		if got := isLoopbackListener(tc.addr); got != tc.want {
			t.Errorf("isLoopbackListener(%q) = %t, want %t: %s", tc.addr, got, tc.want, tc.why)
		}
	}
}
