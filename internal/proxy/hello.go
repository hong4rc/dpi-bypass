package proxy

import (
	"errors"
	"io"
	"net"
	"time"
)

const splitAt = 40 // inside the ClientHello, before the SNI extension

// isAlert reports whether b starts with a TLS alert record.
func isAlert(b []byte) bool { return len(b) > 0 && b[0] == 0x15 }

// isPrivate reports whether host is a loopback, LAN or link-local IP literal.
func isPrivate(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())
}

// isPrivateAddr reports whether a connection's far end is a loopback, LAN or
// link-local address, whatever name was used to reach it (host.docker.internal
// and other names that only exist on this network resolve to such addresses).
func isPrivateAddr(a net.Addr) bool {
	t, ok := a.(*net.TCPAddr)
	return ok && (t.IP.IsPrivate() || t.IP.IsLoopback() || t.IP.IsLinkLocalUnicast())
}

// readFirst waits briefly for the server's first bytes; nil means no reply.
// silent is true when nothing at all came back in time, the way DPI drops a
// hello, as opposed to the connection being cut or closed.
func readFirst(c net.Conn) (reply []byte, silent bool) {
	buf := make([]byte, 32<<10)
	c.SetReadDeadline(time.Now().Add(firstReplyTimeout))
	n, err := c.Read(buf)
	c.SetReadDeadline(time.Time{})
	if n == 0 {
		var ne net.Error
		return nil, errors.As(err, &ne) && ne.Timeout()
	}
	return buf[:n], false
}

// readHello reads the client's first TLS record (header + body). If the first
// byte isn't a TLS handshake it returns just the 5 bytes read.
func readHello(cl net.Conn) ([]byte, error) {
	hello := make([]byte, 5)
	if _, err := io.ReadFull(cl, hello); err != nil {
		return nil, err
	}
	if hello[0] != 0x16 {
		return hello, nil
	}
	body := make([]byte, int(hello[3])<<8|int(hello[4]))
	if _, err := io.ReadFull(cl, body); err != nil {
		return nil, err
	}
	return append(hello, body...), nil
}

// writeSplit sends hello as two TLS records in two TCP segments, so DPI can't
// read the full SNI from one packet. Some DPI puts the two segments back together,
// so on a TCP connection the first one also leaves with a hop limit of 1: it dies
// at the first router, the DPI sees the second segment before the first and can
// not read the hello, and the system sends the first segment again (with the
// normal hop limit) a moment later, so the server still gets both, in order.
func writeSplit(up net.Conn, hello []byte) error {
	body := hello[5:]
	if len(body) <= splitAt {
		_, err := up.Write(hello)
		return err
	}
	restore := shortHop(up)
	for i, part := range [][]byte{body[:splitAt], body[splitAt:]} {
		rec := append([]byte{hello[0], hello[1], hello[2], byte(len(part) >> 8), byte(len(part))}, part...)
		if _, err := up.Write(rec); err != nil {
			if restore != nil {
				restore()
			}
			return err
		}
		if i == 0 {
			time.Sleep(2 * time.Millisecond)
			if restore != nil {
				restore() // before the resend, or the first segment would never arrive
			}
		}
	}
	return nil
}

// shortHop sets the hop limit of c to 1 and returns what puts it back, or nil
// when c is not a TCP connection or the system does not allow the change.
func shortHop(c net.Conn) func() {
	tc, ok := c.(*net.TCPConn)
	if !ok {
		return nil
	}
	addr, _ := tc.RemoteAddr().(*net.TCPAddr)
	v6 := addr != nil && addr.IP.To4() == nil
	rc, err := tc.SyscallConn()
	if err != nil {
		return nil
	}
	orig, set := 0, false
	rc.Control(func(fd uintptr) {
		n, err := getHops(fd, v6)
		if err == nil && setHops(fd, v6, 1) == nil {
			orig, set = n, true
		}
	})
	if !set {
		return nil
	}
	return func() { rc.Control(func(fd uintptr) { setHops(fd, v6, orig) }) }
}
