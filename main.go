// dpi: local proxy that beats SNI-based DPI blocking (e.g. Steam) by splitting
// the TLS ClientHello. User-mode only: no driver, no admin needed.
//
// Files next to the exe (recreated on each start):
//
//	blacklist.txt  domains that skip the bypass and connect directly
//	dpi-info.log   start/stop + failures
//	dpi-debug.log  everything, incl. every connection
package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const addr = "127.0.0.1:18080"

const defaultBlacklist = `# Domains that skip the DPI bypass (connect directly). One per line.
# A domain also covers its subdomains. Restart dpi.exe after editing.
riotgames.com
riotcdn.net
leagueoflegends.com
pvp.net
`

var info, debug *log.Logger

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-restore" {
		restoreSystemProxy()
		fmt.Println("Windows proxy settings restored.")
		return
	}
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	initLogs(dir)
	blacklist := loadBlacklist(filepath.Join(dir, "blacklist.txt"))

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		info.Println("Cannot listen (already running?):", err)
		os.Exit(1)
	}
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, blacklist)
	}))
	setSystemProxy("http://" + addr + "/proxy.pac")
	info.Printf("running on %s, blacklist=%v. Keep this window open; close it or press Ctrl+C to stop.", addr, blacklist)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	restoreSystemProxy()
	info.Println("stopped, proxy settings restored")
}

// initLogs: info goes to console + both files; debug goes to the debug file only.
func initLogs(dir string) {
	fi, _ := os.Create(filepath.Join(dir, "dpi-info.log"))
	fd, _ := os.Create(filepath.Join(dir, "dpi-debug.log"))
	info = log.New(io.MultiWriter(os.Stdout, fi, fd), "INFO  ", log.Ltime)
	debug = log.New(fd, "DEBUG ", log.Ltime|log.Lmicroseconds)
}

func loadBlacklist(path string) []string {
	if _, err := os.Stat(path); err != nil {
		os.WriteFile(path, []byte(defaultBlacklist), 0o644)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var list []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if d := strings.ToLower(strings.TrimSpace(sc.Text())); d != "" && !strings.HasPrefix(d, "#") {
			list = append(list, d)
		}
	}
	return list
}

func handle(w http.ResponseWriter, r *http.Request, blacklist []string) {
	if r.Method != http.MethodConnect {
		// Only the PAC file is served: https:// goes to this proxy, the rest (and the blacklist) DIRECT.
		debug.Printf("PAC requested by %s", r.RemoteAddr)
		fmt.Fprintf(w, `var skip=[%s];
function FindProxyForURL(u,h){
  if(u.indexOf("https:")!=0)return "DIRECT";
  for(var i=0;i<skip.length;i++)if(h==skip[i]||dnsDomainIs(h,"."+skip[i]))return "DIRECT";
  return "PROXY %s; DIRECT";
}`, quoteAll(blacklist), addr)
		return
	}
	start := time.Now()
	debug.Printf("CONNECT %s from %s", r.Host, r.RemoteAddr)
	up, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		info.Printf("FAIL dial %s: %v", r.Host, err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	cl, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		up.Close()
		return
	}
	cl.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))

	if err := sendSplitHello(cl, up); err != nil {
		info.Printf("FAIL hello %s: %v", r.Host, err)
		cl.Close()
		up.Close()
		return
	}
	debug.Printf("hello sent %s (dial+hello %s)", r.Host, time.Since(start).Round(time.Millisecond))

	upBytes := make(chan int64, 1)
	go func() { n, _ := io.Copy(up, cl); upBytes <- n }()
	down, err := io.Copy(cl, up)
	cl.Close()
	up.Close()
	debug.Printf("closed %s up=%d down=%d dur=%s err=%v", r.Host, <-upBytes, down, time.Since(start).Round(time.Millisecond), err)
	if down == 0 {
		info.Printf("NO REPLY from %s (reset or blocked?) - consider adding it to blacklist.txt", r.Host)
	}
}

func quoteAll(list []string) string {
	q := make([]string, len(list))
	for i, d := range list {
		q[i] = fmt.Sprintf("%q", d)
	}
	return strings.Join(q, ",")
}

// sendSplitHello forwards the client's first TLS record as two TLS records in
// two TCP segments, so DPI can't read the full SNI from one packet.
func sendSplitHello(cl, up net.Conn) error {
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(cl, hdr); err != nil {
		return err
	}
	if hdr[0] != 0x16 { // not a TLS handshake: pass through untouched
		debug.Printf("non-TLS first byte 0x%02x, passed through", hdr[0])
		_, err := up.Write(hdr)
		return err
	}
	body := make([]byte, int(hdr[3])<<8|int(hdr[4]))
	if _, err := io.ReadFull(cl, body); err != nil {
		return err
	}
	const cut = 40 // inside the ClientHello, before the SNI extension
	if len(body) <= cut {
		_, err := up.Write(append(hdr, body...))
		return err
	}
	for i, part := range [][]byte{body[:cut], body[cut:]} {
		rec := append([]byte{hdr[0], hdr[1], hdr[2], byte(len(part) >> 8), byte(len(part))}, part...)
		if _, err := up.Write(rec); err != nil {
			return err
		}
		if i == 0 {
			time.Sleep(2 * time.Millisecond)
		}
	}
	return nil
}
