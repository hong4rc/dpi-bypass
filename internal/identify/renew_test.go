package identify

import (
	"crypto/x509"
	"net"
	"testing"
	"time"
)

// A listener that runs for years asks Renewing for its certificate; it must always get a valid one.
func TestRenewingServesAValidCertificateAndKeepsIt(t *testing.T) {
	ips := []net.IP{net.ParseIP("192.168.1.10")}
	get := Renewing(t.TempDir(), ips)
	c1, err := get()
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(c1.Certificate[0])
	if err != nil || time.Until(leaf.NotAfter) < renewBefore || leaf.VerifyHostname("192.168.1.10") != nil {
		t.Fatalf("not a usable certificate: %v %v", leaf, err)
	}
	c2, _ := get()
	if string(c2.Certificate[0]) != string(c1.Certificate[0]) {
		t.Fatal("the certificate must stay the same between re-checks, or the browser's choice to proceed is lost")
	}
}

func TestRenewingReportsAnErrorOnlyWhenThereIsNoCertificateAtAll(t *testing.T) {
	get := Renewing(t.TempDir(), nil) // no address: a certificate is still made (for localhost)
	if _, err := get(); err != nil {
		t.Fatal(err)
	}
}
