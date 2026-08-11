package remotecontrol

import (
	"errors"
	"fmt"
	"net"
)

// ErrNoLAN is returned by DetectRoutableIP when no routable, non-loopback IPv4
// address can be found. Binding loopback would produce a URL the phone cannot
// reach, so the caller must surface this rather than fall back silently.
var ErrNoLAN = errors.New("remotecontrol: no routable LAN address found; are you connected to Wi-Fi?")

// ifaceInfo pairs a network interface's flags with its addresses. DetectRoutableIP
// consumes these so it can skip down / loopback / point-to-point (VPN tunnel)
// interfaces before considering their addresses.
type ifaceInfo struct {
	flags net.Flags
	addrs []net.Addr
}

// ifaceLister returns the host's interfaces with their addresses. It is a package
// variable so tests can inject a fake set without real hardware.
var ifaceLister = defaultIfaceLister

func defaultIfaceLister() ([]ifaceInfo, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]ifaceInfo, 0, len(ifaces))
	for _, ifi := range ifaces {
		addrs, err := ifi.Addrs()
		if err != nil {
			continue // an interface whose addresses can't be read is unusable
		}
		out = append(out, ifaceInfo{flags: ifi.Flags, addrs: addrs})
	}
	return out, nil
}

// DetectRoutableIP returns a routable IPv4 address suitable for embedding in the
// printed pairing URL — one a phone on the same Wi-Fi can actually reach.
//
// It iterates interfaces (not bare addresses) so it can skip the ones that would
// yield an unreachable URL: interfaces that are down, loopback, or point-to-point
// (VPN utun / tunnel links, whose address the phone cannot route to). Among the
// rest it prefers a private LAN address (RFC1918: 10/8, 172.16/12, 192.168/16),
// which is what home/office Wi-Fi hands out; a non-private routable address is
// used only as a fallback when no private one exists. Callers that need a specific
// interface should override via Config.Host. It returns ErrNoLAN when nothing
// routable exists.
func DetectRoutableIP() (string, error) {
	ifaces, err := ifaceLister()
	if err != nil {
		return "", fmt.Errorf("remotecontrol: list interfaces: %w", err)
	}
	var fallback string
	for _, ifi := range ifaces {
		if ifi.flags&net.FlagUp == 0 {
			continue // interface is down
		}
		if ifi.flags&net.FlagLoopback != 0 {
			continue
		}
		if ifi.flags&net.FlagPointToPoint != 0 {
			continue // VPN / tunnel link — its address isn't reachable from the LAN
		}
		for _, a := range ifi.addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			default:
				continue
			}
			v4 := ip.To4()
			if v4 == nil {
				continue // skip IPv6 for the LAN URL
			}
			if v4.IsLoopback() || v4.IsLinkLocalUnicast() || v4.IsLinkLocalMulticast() || v4.IsUnspecified() {
				continue
			}
			if v4.IsPrivate() {
				return v4.String(), nil
			}
			if fallback == "" {
				fallback = v4.String()
			}
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", ErrNoLAN
}

// ListenFreePort binds a TCP listener on host. It first tries the requested
// port; if port is 0 or already in use it falls back to a kernel-assigned free
// port (:0). It returns the listener and the actual port bound.
func ListenFreePort(host string, port int) (net.Listener, int, error) {
	if port != 0 {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
		if err == nil {
			return ln, ln.Addr().(*net.TCPAddr).Port, nil
		}
		// Requested port unavailable — fall through to an auto-assigned one.
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, 0, fmt.Errorf("remotecontrol: bind %s: %w", host, err)
	}
	return ln, ln.Addr().(*net.TCPAddr).Port, nil
}
