//go:build unix

package proxy

import "golang.org/x/sys/unix"

// getHops and setHops read and set the hop limit (TTL) of the packets a socket sends.
func getHops(fd uintptr, v6 bool) (int, error) {
	if v6 {
		return unix.GetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS)
	}
	return unix.GetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_TTL)
}

func setHops(fd uintptr, v6 bool, n int) error {
	if v6 {
		return unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS, n)
	}
	return unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_TTL, n)
}
