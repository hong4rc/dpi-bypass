package main

import (
	"bytes"
	"io"
	"log"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	info = log.New(io.Discard, "", 0)
	debug = log.New(io.Discard, "", 0)
	os.Exit(m.Run())
}

// fakeHello builds a TLS handshake record with an n-byte body.
func fakeHello(n int) []byte {
	body := bytes.Repeat([]byte{0xAB}, n)
	return append([]byte{0x16, 0x03, 0x01, byte(n >> 8), byte(n)}, body...)
}

// readRecords parses all TLS records from b, returning their bodies.
func readRecords(t *testing.T, b []byte) [][]byte {
	var out [][]byte
	for len(b) > 0 {
		if len(b) < 5 {
			t.Fatalf("truncated record header: %d bytes", len(b))
		}
		n := int(b[3])<<8 | int(b[4])
		if len(b) < 5+n {
			t.Fatalf("truncated record body: want %d have %d", n, len(b)-5)
		}
		out = append(out, b[5:5+n])
		b = b[5+n:]
	}
	return out
}

func TestSendSplitHelloSplitsIntoTwoRecords(t *testing.T) {
	hello := fakeHello(300)
	clIn, clOut := net.Pipe()
	upIn, upOut := net.Pipe()
	go func() { clOut.Write(hello); clOut.Close() }()
	got := make(chan []byte)
	go func() { b, _ := io.ReadAll(upOut); got <- b }()

	if err := sendSplitHello(clIn, upIn); err != nil {
		t.Fatal(err)
	}
	upIn.Close()

	recs := readRecords(t, <-got)
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	if !bytes.Equal(bytes.Join(recs, nil), hello[5:]) {
		t.Fatal("reassembled payload differs from original")
	}
}

func TestSendSplitHelloPassesNonTLS(t *testing.T) {
	clIn, clOut := net.Pipe()
	upIn, upOut := net.Pipe()
	go func() { clOut.Write([]byte("GET /")); clOut.Close() }()
	got := make(chan []byte)
	go func() { b, _ := io.ReadAll(upOut); got <- b }()

	if err := sendSplitHello(clIn, upIn); err != nil {
		t.Fatal(err)
	}
	upIn.Close()
	if b := <-got; string(b) != "GET /" {
		t.Fatalf("non-TLS bytes altered: %q", b)
	}
}

func TestLoadBlacklistCreatesDefaultAndSkipsComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blacklist.txt")
	list := loadBlacklist(path)
	if len(list) == 0 || list[0] != "riotgames.com" {
		t.Fatalf("default blacklist not loaded: %v", list)
	}
	for _, d := range list {
		if strings.HasPrefix(d, "#") {
			t.Fatalf("comment leaked into list: %q", d)
		}
	}
}

func TestPACRoutesBlacklistDirect(t *testing.T) {
	rec := httptest.NewRecorder()
	handle(rec, httptest.NewRequest("GET", "/proxy.pac", nil), []string{"example.com"})
	body := rec.Body.String()
	for _, want := range []string{`"example.com"`, "PROXY " + addr, `return "DIRECT"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("PAC missing %q:\n%s", want, body)
		}
	}
}
