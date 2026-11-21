package proxy

import (
	"bytes"
	"io"
	"net"
	"testing"
)

func hops(t *testing.T, c *net.TCPConn) int {
	t.Helper()
	rc, err := c.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var n int
	var gerr error
	rc.Control(func(fd uintptr) { n, gerr = getHops(fd, false) })
	if gerr != nil {
		t.Fatal(gerr)
	}
	return n
}

func TestASplitOverTCPArrivesWholeAndPutsTheHopLimitBack(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- nil
			return
		}
		d, _ := io.ReadAll(c)
		got <- d
	}()

	c, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	tc := c.(*net.TCPConn)
	before := hops(t, tc)
	hello := fakeHello(300)
	if err := writeSplit(c, hello); err != nil {
		t.Fatal(err)
	}
	if after := hops(t, tc); after != before {
		t.Fatalf("hop limit is %d after the split, want it back at %d", after, before)
	}
	c.Close()

	raw := <-got
	first := append([]byte{hello[0], hello[1], hello[2], 0, splitAt}, hello[5:5+splitAt]...)
	if !bytes.HasPrefix(raw, first) {
		t.Fatal("the first record must arrive first and unchanged")
	}
	rest := raw[len(first):]
	if !bytes.Equal(rest[5:], hello[5+splitAt:]) || len(raw) != len(hello)+5 {
		t.Fatalf("the server must get the whole hello as two records: got %d bytes, want %d", len(raw), len(hello)+5)
	}
}

func TestShortHopLeavesOtherConnectionsAlone(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if shortHop(a) != nil {
		t.Fatal("a connection that is not TCP has no hop limit to change")
	}
}
