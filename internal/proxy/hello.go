package proxy

import (
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
func readFirst(c net.Conn) []byte {
	buf := make([]byte, 32<<10)
	c.SetReadDeadline(time.Now().Add(firstReplyTimeout))
	n, _ := c.Read(buf)
	c.SetReadDeadline(time.Time{})
	if n == 0 {
		return nil
	}
	return buf[:n]
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
// read the full SNI from one packet.
func writeSplit(up net.Conn, hello []byte) error {
	body := hello[5:]
	if len(body) <= splitAt {
		_, err := up.Write(hello)
		return err
	}
	for i, part := range [][]byte{body[:splitAt], body[splitAt:]} {
		rec := append([]byte{hello[0], hello[1], hello[2], byte(len(part) >> 8), byte(len(part))}, part...)
		if _, err := up.Write(rec); err != nil {
			return err
		}
		if i == 0 {
			time.Sleep(2 * time.Millisecond)
		}
	}
	return nil
}
