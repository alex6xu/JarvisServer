package remotecontrol

import (
	"net"
	"testing"
)

// withIfaceLister swaps the package ifaceLister for the duration of a test.
func withIfaceLister(t *testing.T, fn func() ([]ifaceInfo, error)) {
	t.Helper()
	orig := ifaceLister
	ifaceLister = fn
	t.Cleanup(func() { ifaceLister = orig })
}

// ipnets wraps IPs as *net.IPNet addresses for an ifaceInfo.
func ipnets(ips ...string) []net.Addr {
	out := make([]net.Addr, 0, len(ips))
	for _, s := range ips {
		out = append(out, &net.IPNet{IP: net.ParseIP(s)})
	}
	return out
}

func TestDetectRoutableIPPicksRoutableV4(t *testing.T) {
	withIfaceLister(t, func() ([]ifaceInfo, error) {
		return []ifaceInfo{{
			flags: net.FlagUp | net.FlagBroadcast | net.FlagMulticast,
			addrs: ipnets("fe80::1", "127.0.0.1", "169.254.1.5", "192.168.1.42", "10.0.0.9"),
		}}, nil
	})
	ip, err := DetectRoutableIP()
	if err != nil {
		t.Fatalf("DetectRoutableIP: %v", err)
	}
	if ip != "192.168.1.42" {
		t.Fatalf("ip = %q, want 192.168.1.42 (first private v4)", ip)
	}
}

// A VPN utun tunnel (point-to-point) whose address sorts before Wi-Fi must be
// skipped so the QR URL points at the LAN address the phone can reach — the
// white-screen bug this replaces.
func TestDetectRoutableIPSkipsVPNPointToPoint(t *testing.T) {
	withIfaceLister(t, func() ([]ifaceInfo, error) {
		return []ifaceInfo{
			{ // loopback
				flags: net.FlagUp | net.FlagLoopback,
				addrs: ipnets("127.0.0.1"),
			},
			{ // VPN tunnel — routable but unreachable from the LAN
				flags: net.FlagUp | net.FlagPointToPoint | net.FlagMulticast,
				addrs: ipnets("172.31.201.147"),
			},
			{ // Wi-Fi
				flags: net.FlagUp | net.FlagBroadcast | net.FlagMulticast,
				addrs: ipnets("192.168.3.54"),
			},
		}, nil
	})
	ip, err := DetectRoutableIP()
	if err != nil {
		t.Fatalf("DetectRoutableIP: %v", err)
	}
	if ip != "192.168.3.54" {
		t.Fatalf("ip = %q, want 192.168.3.54 (Wi-Fi, VPN skipped)", ip)
	}
}

// A down interface must not be chosen even if it carries a private address.
func TestDetectRoutableIPSkipsDownInterface(t *testing.T) {
	withIfaceLister(t, func() ([]ifaceInfo, error) {
		return []ifaceInfo{
			{ // down: skipped despite the private address
				flags: net.FlagBroadcast | net.FlagMulticast,
				addrs: ipnets("192.168.9.9"),
			},
			{ // up
				flags: net.FlagUp | net.FlagBroadcast | net.FlagMulticast,
				addrs: ipnets("10.1.2.3"),
			},
		}, nil
	})
	ip, err := DetectRoutableIP()
	if err != nil {
		t.Fatalf("DetectRoutableIP: %v", err)
	}
	if ip != "10.1.2.3" {
		t.Fatalf("ip = %q, want 10.1.2.3 (down iface skipped)", ip)
	}
}

// With no private address, a routable public address is used as a fallback.
func TestDetectRoutableIPFallsBackToPublic(t *testing.T) {
	withIfaceLister(t, func() ([]ifaceInfo, error) {
		return []ifaceInfo{{
			flags: net.FlagUp | net.FlagBroadcast | net.FlagMulticast,
			addrs: ipnets("203.0.113.7"),
		}}, nil
	})
	ip, err := DetectRoutableIP()
	if err != nil {
		t.Fatalf("DetectRoutableIP: %v", err)
	}
	if ip != "203.0.113.7" {
		t.Fatalf("ip = %q, want 203.0.113.7 (public fallback)", ip)
	}
}

func TestDetectRoutableIPSkipsLoopbackAndLinkLocal(t *testing.T) {
	withIfaceLister(t, func() ([]ifaceInfo, error) {
		return []ifaceInfo{{
			flags: net.FlagUp | net.FlagMulticast,
			addrs: ipnets("127.0.0.1", "169.254.1.5"),
		}}, nil
	})
	if _, err := DetectRoutableIP(); err != ErrNoLAN {
		t.Fatalf("err = %v, want ErrNoLAN", err)
	}
}

func TestDetectRoutableIPNoInterfaces(t *testing.T) {
	withIfaceLister(t, func() ([]ifaceInfo, error) {
		return nil, nil
	})
	if _, err := DetectRoutableIP(); err != ErrNoLAN {
		t.Fatalf("err = %v, want ErrNoLAN", err)
	}
}

func TestListenFreePortAutoAssign(t *testing.T) {
	ln, port, err := ListenFreePort("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("ListenFreePort: %v", err)
	}
	defer ln.Close()
	if port <= 0 {
		t.Fatalf("port = %d, want > 0", port)
	}
	if got := ln.Addr().(*net.TCPAddr).Port; got != port {
		t.Fatalf("listener port %d != returned %d", got, port)
	}
}

func TestListenFreePortFallsBackWhenOccupied(t *testing.T) {
	// Occupy a port, then ask ListenFreePort for that same port; it must fall
	// back to a different, free one instead of failing.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy: %v", err)
	}
	defer occupied.Close()
	busyPort := occupied.Addr().(*net.TCPAddr).Port

	ln, port, err := ListenFreePort("127.0.0.1", busyPort)
	if err != nil {
		t.Fatalf("ListenFreePort: %v", err)
	}
	defer ln.Close()
	if port == busyPort {
		t.Fatalf("port = %d, expected fallback away from occupied %d", port, busyPort)
	}
}

func TestListenFreePortUsesRequestedWhenFree(t *testing.T) {
	// Find a free port, release it, then request it explicitly.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	want := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	ln, port, err := ListenFreePort("127.0.0.1", want)
	if err != nil {
		t.Fatalf("ListenFreePort: %v", err)
	}
	defer ln.Close()
	if port != want {
		t.Fatalf("port = %d, want requested %d", port, want)
	}
}
