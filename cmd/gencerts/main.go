// gencerts створює набір сертифікатів для демонстрації mTLS без openssl
// (працює однаково на Windows / macOS / Linux):
//
//	ca.pem, ca-key.pem                    — наш внутрішній CA
//	server.pem, server-key.pem            — сертифікат сервера (localhost, 127.0.0.1, ::1)
//	client.pem, client-key.pem            — довірений клієнт (підписаний нашим CA)
//	rogue-ca.pem                          — сторонній CA (для негативного тесту)
//	rogue-client.pem, rogue-client-key.pem — клієнт від стороннього CA (сервер ПОВИНЕН відхилити)
//
// Лише для локальної розробки. У продакшені сертифікати видає внутрішній CA
// з автоматичною ротацією (напр. cert-manager у Kubernetes).
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

type pair struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func main() {
	dir := flag.String("dir", "certs", "каталог для сертифікатів")
	force := flag.Bool("force", false, "перезаписати наявні файли")
	flag.Parse()

	if _, err := os.Stat(filepath.Join(*dir, "ca.pem")); err == nil && !*force {
		fmt.Printf("Сертифікати вже є в %s/ (використайте -force, щоб перестворити)\n", *dir)
		return
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		log.Fatal(err)
	}

	ca := newCA("LibraTrack Demo CA")
	write(*dir, "ca", ca)

	server := newLeaf(ca, "inventory-server", x509.ExtKeyUsageServerAuth,
		[]string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")})
	write(*dir, "server", server)

	client := newLeaf(ca, "inventory-client", x509.ExtKeyUsageClientAuth, nil, nil)
	write(*dir, "client", client)

	rogueCA := newCA("Rogue CA (untrusted)")
	writeCertOnly(*dir, "rogue-ca", rogueCA)
	rogueClient := newLeaf(rogueCA, "rogue-client", x509.ExtKeyUsageClientAuth, nil, nil)
	write(*dir, "rogue-client", rogueClient)

	fmt.Printf("Готово. Файли створено в %s/:\n", *dir)
	entries, _ := os.ReadDir(*dir)
	for _, e := range entries {
		fmt.Println("  ", e.Name())
	}
}

func newCA(cn string) pair {
	key := mustKey()
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"LibraTrack Demo"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	check(err)
	cert, err := x509.ParseCertificate(der)
	check(err)
	return pair{cert, key}
}

func newLeaf(ca pair, cn string, usage x509.ExtKeyUsage, dns []string, ips []net.IP) pair {
	key := mustKey()
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: cn, Organization: []string{"LibraTrack Demo"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0), // у проді — короткоживучі сертифікати
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	check(err)
	cert, err := x509.ParseCertificate(der)
	check(err)
	return pair{cert, key}
}

func write(dir, name string, p pair) {
	writeCertOnly(dir, name, p)
	keyDER, err := x509.MarshalECPrivateKey(p.key)
	check(err)
	writePEM(filepath.Join(dir, name+"-key.pem"), "EC PRIVATE KEY", keyDER, 0o600)
}

func writeCertOnly(dir, name string, p pair) {
	writePEM(filepath.Join(dir, name+".pem"), "CERTIFICATE", p.cert.Raw, 0o644)
}

func writePEM(path, typ string, der []byte, mode os.FileMode) {
	check(os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), mode))
}

func mustKey() *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	check(err)
	return k
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	check(err)
	return n
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
