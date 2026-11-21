//go:build windows

package proxy

import "golang.org/x/sys/windows"

// getHops and setHops read and set the hop limit (TTL) of the packets a socket sends.
func getHops(fd uintptr, v6 bool) (int, error) {
	if v6 {
		return windows.GetsockoptInt(windows.Handle(fd), windows.IPPROTO_IPV6, windows.IPV6_UNICAST_HOPS)
	}
	return windows.GetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, windows.IP_TTL)
}

func setHops(fd uintptr, v6 bool, n int) error {
	if v6 {
		return windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IPV6, windows.IPV6_UNICAST_HOPS, n)
	}
	return windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, windows.IP_TTL, n)
}
