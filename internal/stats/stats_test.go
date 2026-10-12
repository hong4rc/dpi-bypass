package stats

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseARPLayouts(t *testing.T) {
	windows := "Interface: 192.168.1.10 --- 0x6\n  Internet Address      Physical Address      Type\n  192.168.1.1           aa-bb-cc-dd-ee-0f     dynamic\n  192.168.1.20          11-22-33-44-55-66     dynamic\n"
	linux := "IP address       HW type     Flags       HW address            Mask     Device\n192.168.1.1      0x1         0x2         aa:bb:cc:dd:ee:0f     *        eth0\n"
	mac := "? (192.168.1.1) at a:bb:c:dd:ee:f on en0 ifscope [ethernet]\n"
	for name, tc := range map[string]struct{ out, ip, want string }{
		"windows":      {windows, "192.168.1.1", "AA:BB:CC:DD:EE:0F"},
		"windows-2nd":  {windows, "192.168.1.20", "11:22:33:44:55:66"},
		"linux":        {linux, "192.168.1.1", "AA:BB:CC:DD:EE:0F"},
		"macos":        {mac, "192.168.1.1", "0A:BB:0C:DD:EE:0F"},
		"unknown":      {windows, "192.168.1.99", ""},
		"not-a-prefix": {windows, "192.168.1.2", ""}, // must not match 192.168.1.20
	} {
		if got := parseARP(tc.out, tc.ip); got != tc.want {
			t.Errorf("%s: parseARP(%s)=%q want %q", name, tc.ip, got, tc.want)
		}
	}
}

func TestClientIPNormalizesLoopback(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:5000": "127.0.0.1", "[::1]:5000": "127.0.0.1", "192.168.1.9:7": "192.168.1.9",
	} {
		if got := ClientIP(in); got != want {
			t.Errorf("ClientIP(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDevicesAndEvents(t *testing.T) {
	s := New("")
	s.SeenPAC("192.168.1.50")
	s.AddConn(Event{Client: "192.168.1.50", Host: "a:443", Mode: "bypass", Up: 10, Down: 100})
	s.AddConn(Event{Client: "192.168.1.50", Host: "b:443", Mode: "failed"})
	s.AddConn(Event{Client: "127.0.0.1", Host: "c:443", Mode: "direct", Up: 1, Down: 2})

	devs := s.Devices()
	if len(devs) != 2 || !devs[0].Self || devs[1].IP != "192.168.1.50" {
		t.Fatalf("devices (this PC must be first): %+v", devs)
	}
	phone := devs[1]
	if phone.Conns != 2 || phone.PACFetches != 1 || phone.Up != 10 || phone.Down != 100 || !phone.Active {
		t.Fatalf("phone counters wrong: %+v", phone)
	}

	if tot := s.Totals(); tot.Conns != 3 || tot.Bypass != 1 || tot.Failed != 1 || tot.Direct != 1 || tot.Up != 11 || tot.Down != 102 {
		t.Fatalf("totals wrong: %+v", tot)
	}

	ev := s.Recent(2)
	if len(ev) != 2 || ev[0].Host != "c:443" || ev[1].Host != "b:443" {
		t.Fatalf("Recent must be newest first: %+v", ev)
	}
}

func TestAgentsDescribeTheDeviceWithoutDuplicates(t *testing.T) {
	s := New("")
	android := "Dalvik/2.1.0 (Linux; U; Android 14; SM-S918B Build/UP1A.231005.007)"
	s.SeenAgent("192.168.1.55", android)
	s.SeenAgent("192.168.1.55", android) // the same phone connects many times
	s.SeenAgent("192.168.1.55", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15")
	s.SeenAgent("192.168.1.55", "curl/8.4.0") // says nothing about the device
	s.SeenAgent("192.168.1.55", "")

	got := s.Devices()[0].Agents
	if len(got) != 2 || got[0] != "Android 14 · SM-S918B" || got[1] != "iPhone · iOS 17.2" {
		t.Fatalf("agents: %q", got)
	}
}

func TestAgentsAreBoundedAndNeverNil(t *testing.T) {
	s := New("")
	s.SeenPAC("10.0.0.2")
	if a := s.Devices()[0].Agents; a == nil || len(a) != 0 {
		t.Fatalf("a device without agents must have an empty list, got %#v", a)
	}
	for i := 0; i < 20; i++ {
		s.SeenAgent("10.0.0.2", "Mozilla/5.0 (Linux; Android 13; Phone"+string(rune('A'+i))+"X) Chrome")
	}
	if n := len(s.Devices()[0].Agents); n != maxAgents {
		t.Fatalf("kept %d agents, want %d", n, maxAgents)
	}
}

func TestAConnectionCountsAsSoonAsItOpensAndGetsItsSizeWhenItCloses(t *testing.T) {
	s := New("")
	id := s.Open(Event{Client: "192.168.1.13", Host: "www.google.com:443", Mode: "bypass"})

	dev := s.Devices()[0]
	if dev.Conns != 1 || dev.Up != 0 || dev.Down != 0 {
		t.Fatalf("an open connection must already count: %+v", dev)
	}
	if tot := s.Totals(); tot.Conns != 1 || tot.Open != 1 || tot.Bypass != 1 {
		t.Fatalf("totals while open: %+v", tot)
	}
	if ev := s.Recent(1)[0]; !ev.Open || ev.ID != id {
		t.Fatalf("event should be marked open: %+v", ev)
	}

	s.Close(id, 100, 2000, 1500, "")
	dev = s.Devices()[0]
	if dev.Conns != 1 || dev.Up != 100 || dev.Down != 2000 {
		t.Fatalf("device after close: %+v", dev)
	}
	if tot := s.Totals(); tot.Open != 0 || tot.Up != 100 || tot.Down != 2000 || tot.Conns != 1 {
		t.Fatalf("totals after close: %+v", tot)
	}
	if ev := s.Recent(1)[0]; ev.Open || ev.Up != 100 || ev.Down != 2000 || ev.Millis != 1500 {
		t.Fatalf("event after close: %+v", ev)
	}

	s.Close(id, 5, 5, 5, "") // closing twice, or an unknown id, must not double count
	s.Close(9999, 5, 5, 5, "")
	if tot := s.Totals(); tot.Up != 100 || tot.Open != 0 {
		t.Fatalf("double close changed totals: %+v", tot)
	}
}

func TestBytesStillCountWhenTheEventWasTrimmedAway(t *testing.T) {
	s := New("")
	first := s.Open(Event{Client: "192.168.1.13", Host: "long-lived:443", Mode: "bypass"})
	for i := 0; i < maxEvents+10; i++ {
		s.AddConn(Event{Client: "127.0.0.1", Host: "x:443", Mode: "direct"})
	}
	s.Close(first, 7, 9, 1, "")
	for _, d := range s.Devices() {
		if d.IP == "192.168.1.13" && (d.Up != 7 || d.Down != 9) {
			t.Fatalf("bytes lost for a long-lived connection: %+v", d)
		}
	}
	if tot := s.Totals(); tot.Open != 0 {
		t.Fatalf("still shows open connections: %+v", tot)
	}
}

func TestSeenAgentReportsEachDistinctHeaderOnce(t *testing.T) {
	s := New("")
	ua := "Mozilla/5.0 (Linux; Android 10; K) Chrome/120 Mobile Safari/537.36"
	label, isNew := s.SeenAgent("192.168.1.13", ua)
	if label != "Android" || !isNew {
		t.Fatalf("first sight: %q new=%v", label, isNew)
	}
	if _, isNew := s.SeenAgent("192.168.1.13", ua); isNew {
		t.Fatal("the same header must not be reported again")
	}
	if label, isNew := s.SeenAgent("192.168.1.13", "curl/8.4.0"); label != "" || !isNew {
		t.Fatalf("an unlabeled agent is still worth logging once: %q new=%v", label, isNew)
	}
	if raw := s.Devices()[0].RawAgents; len(raw) != 2 || raw[0] != ua {
		t.Fatalf("raw agents: %q", raw)
	}
}

func TestEventsAreCapped(t *testing.T) {
	s := New("")
	for i := 0; i < maxEvents+25; i++ {
		s.AddConn(Event{Client: "127.0.0.1", Host: "x:443", Millis: int64(i)})
	}
	all := s.Recent(0)
	if len(all) != maxEvents || all[0].Millis != int64(maxEvents+24) {
		t.Fatalf("len=%d newest=%d", len(all), all[0].Millis)
	}
}

// reload opens the store again, as a restart would, and returns a device's saved name and identities.
func reload(t *testing.T, path, ip string) (string, []Identity) {
	t.Helper()
	s := New(path)
	s.SeenPAC(ip) // a device only appears once it is seen
	for _, d := range s.Devices() {
		if d.IP == ip {
			return d.Name, d.Identities
		}
	}
	t.Fatalf("device %s missing", ip)
	return "", nil
}

func TestNamesPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	s := New(path)
	s.SeenPAC("192.168.1.50")
	if err := s.SetName("192.168.1.50", " My Android "); err != nil {
		t.Fatal(err)
	}
	if name, _ := reload(t, path, "192.168.1.50"); name != "My Android" {
		t.Fatalf("name not persisted: %q", name)
	}
	if n := s.Devices()[0].Name; n != "My Android" {
		t.Fatalf("Devices name %q", n)
	}
	s.SetName("192.168.1.50", "")
	if name, _ := reload(t, path, "192.168.1.50"); name != "" {
		t.Fatalf("name not cleared: %q", name)
	}
	time.Sleep(10 * time.Millisecond) // let the background lookups finish before temp dir cleanup
}

func TestIdentityIsStoredLabelledAndSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	s := New(path)
	err := s.SetIdentity("192.168.1.13", Identity{Model: "SHARK PAR-A0", Platform: "Android", PlatformVersion: "12.0.0", Mobile: true, Browser: "Google Chrome 154"})
	if err != nil {
		t.Fatal(err)
	}
	ids := s.Devices()[0].Identities
	if len(ids) != 1 || ids[0].Label != "SHARK PAR-A0 · Android 12" || ids[0].At.IsZero() {
		t.Fatalf("identity: %+v", ids)
	}
	if _, ids := reload(t, path, "192.168.1.13"); len(ids) != 1 || ids[0].Model != "SHARK PAR-A0" {
		t.Fatalf("identity lost on restart: %+v", ids)
	}
	time.Sleep(10 * time.Millisecond)
}

func TestTheSameModelReportingAgainReplacesItsEntry(t *testing.T) {
	s := New("")
	s.SetIdentity("10.0.0.2", Identity{Model: "Pixel 7", Platform: "Android", PlatformVersion: "13.0.0"})
	s.SetIdentity("10.0.0.2", Identity{Model: "Pixel 7", Platform: "Android", PlatformVersion: "14.0.0"})  // updated OS
	s.SetIdentity("10.0.0.2", Identity{Model: "SM-S918B", Platform: "Android", PlatformVersion: "14.0.0"}) // a second phone behind the same router
	ids := s.Devices()[0].Identities
	if len(ids) != 2 || ids[0].Label != "Pixel 7 · Android 14" || ids[1].Label != "SM-S918B · Android 14" {
		t.Fatalf("identities: %+v", ids)
	}
}

func TestIdentityLabelWithoutAModel(t *testing.T) {
	for name, tc := range map[string]struct {
		id   Identity
		want string
	}{
		"iphone safari (no client hints)": {Identity{UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15"}, "iPhone · iOS 17.2"},
		"desktop chrome":                  {Identity{Platform: "Windows", PlatformVersion: "15.0.0", UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/154"}, "Windows PC"},
		"model only":                      {Identity{Model: "Pixel 8"}, "Pixel 8"},
		"nothing useful":                  {Identity{Platform: "Linux"}, "Linux"},
	} {
		if got := tc.id.Describe(); got != tc.want {
			t.Errorf("%s: %q want %q", name, got, tc.want)
		}
	}
}

// The first version kept only names, in device-names.json.
func TestOldNamesFileMovesIntoDevicesJSON(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "device-names.json")
	os.WriteFile(legacy, []byte(`{"192.168.1.55": "Old name"}`), 0o644)

	path := filepath.Join(dir, "devices.json")
	if name, _ := reload(t, path, "192.168.1.55"); name != "Old name" {
		t.Fatalf("legacy name not imported: %q", name)
	}
	if _, err := os.Stat(legacy); err == nil {
		t.Fatal("legacy file should be removed after the move")
	}
	if name, _ := reload(t, path, "192.168.1.55"); name != "Old name" {
		t.Fatalf("name not kept in devices.json: %q", name)
	}
	time.Sleep(10 * time.Millisecond)
}
