package proxy

import "net"

// clientIsLocal reports whether a request came from this PC (loopback).
func clientIsLocal(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isSelf reports whether a is one of this PC's own addresses. LAN clients must
// not be able to reach the PC's local-only services through the proxy.
func isSelf(a net.Addr) bool {
	tcp, ok := a.(*net.TCPAddr)
	if !ok {
		return false
	}
	if tcp.IP.IsLoopback() || tcp.IP.IsUnspecified() {
		return true
	}
	addrs, _ := net.InterfaceAddrs()
	for _, ad := range addrs {
		if n, ok := ad.(*net.IPNet); ok && n.IP.Equal(tcp.IP) {
			return true
		}
	}
	return false
}
