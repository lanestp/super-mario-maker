package accountapi

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// * Writes a certificate with an RSA key for 127.0.0.1, signed by itself
func writeCertificate(t *testing.T) (string, string, *x509.CertPool) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	return writeCertificateFor(t, key, &key.PublicKey, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

// * The same with an elliptic curve key, the other kind Let's Encrypt
// * hands out
func writeEllipticCertificate(t *testing.T) (string, string, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	return writeCertificateFor(t, key, &key.PublicKey, &pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

func writeCertificateFor(t *testing.T, key crypto.Signer, public any, keyBlock *pem.Block) (string, string, *x509.CertPool) {
	t.Helper()

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, public, key)
	if err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	certificatePath := filepath.Join(directory, "certificate.pem")
	keyPath := filepath.Join(directory, "key.pem")

	if err := os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(keyPath, pem.EncodeToMemory(keyBlock), 0o600); err != nil {
		t.Fatal(err)
	}

	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	trusted := x509.NewCertPool()
	trusted.AddCert(certificate)

	return certificatePath, keyPath, trusted
}

// * Serves TLS the way the server would, and returns its address
func serveTLS(t *testing.T, server Server) string {
	t.Helper()

	config, err := server.TLSConfig()
	if err != nil || config == nil {
		t.Fatalf("no TLS configuration: %v", err)
	}

	listener, err := tls.Listen("tcp", "127.0.0.1:0", config)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { listener.Close() })

	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}

			go func() {
				defer connection.Close()
				// * Reading makes the handshake happen
				connection.Read(make([]byte, 1))
			}()
		}
	}()

	return listener.Addr().String()
}

// * A client like an old console: one version of TLS and one cipher
func handshake(address string, trusted *x509.CertPool, version uint16, cipherSuites []uint16) (uint16, error) {
	connection, err := tls.Dial("tcp", address, &tls.Config{
		RootCAs:      trusted,
		MinVersion:   version,
		MaxVersion:   version,
		CipherSuites: cipherSuites,
	})
	if err != nil {
		return 0, err
	}

	defer connection.Close()

	return connection.ConnectionState().CipherSuite, nil
}

var consoleCipher = []uint16{tls.TLS_RSA_WITH_AES_128_CBC_SHA}

func TestLegacyTLSLetsAnOldConsoleConnect(t *testing.T) {
	certificatePath, keyPath, trusted := writeCertificate(t)

	address := serveTLS(t, Server{TLSCertificatePath: certificatePath, TLSKeyPath: keyPath, LegacyTLS: true})

	for name, version := range map[string]uint16{"TLS 1.0": tls.VersionTLS10, "TLS 1.1": tls.VersionTLS11} {
		cipher, err := handshake(address, trusted, version, consoleCipher)
		if err != nil {
			t.Errorf("%s with a console's cipher was refused: %v", name, err)
		} else if cipher != tls.TLS_RSA_WITH_AES_128_CBC_SHA {
			t.Errorf("%s: got cipher %#x", name, cipher)
		}
	}

	// * A current client is still given something current
	cipher, err := handshake(address, trusted, tls.VersionTLS12, nil)
	if err != nil {
		t.Fatalf("TLS 1.2 was refused: %v", err)
	}

	if cipher != tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256 && cipher != tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384 && cipher != tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256 {
		t.Errorf("a current client was given cipher %#x, want a current one", cipher)
	}

	if _, err := handshake(address, trusted, tls.VersionTLS13, nil); err != nil {
		t.Errorf("TLS 1.3 was refused: %v", err)
	}
}

func TestOldTLSIsRefusedUnlessAskedFor(t *testing.T) {
	certificatePath, keyPath, trusted := writeCertificate(t)

	address := serveTLS(t, Server{TLSCertificatePath: certificatePath, TLSKeyPath: keyPath})

	for name, version := range map[string]uint16{"TLS 1.0": tls.VersionTLS10, "TLS 1.1": tls.VersionTLS11} {
		if _, err := handshake(address, trusted, version, consoleCipher); err == nil {
			t.Errorf("%s was accepted by a server that was not asked to take it", name)
		}
	}

	// * A console's cipher is refused as well, even over a current version
	if _, err := handshake(address, trusted, tls.VersionTLS12, consoleCipher); err == nil {
		t.Error("a console's cipher was accepted by a server that was not asked to take it")
	}

	if _, err := handshake(address, trusted, tls.VersionTLS12, nil); err != nil {
		t.Errorf("TLS 1.2 was refused: %v", err)
	}
}

func TestTLSConfigWithoutACertificate(t *testing.T) {
	config, err := Server{}.TLSConfig()
	if config != nil || err != nil {
		t.Errorf("a server with no certificate got %v and %v, want no TLS and no error", config, err)
	}

	// * Asking for legacy TLS must never end up as no TLS at all
	if config, err := (Server{LegacyTLS: true}).TLSConfig(); err == nil {
		t.Errorf("legacy TLS with no certificate gave %v, want an error", config)
	}

	if _, err := (Server{TLSCertificatePath: "missing.pem", TLSKeyPath: "missing.pem"}).TLSConfig(); err == nil {
		t.Error("a certificate that is not there was accepted")
	}
}

// * Giving only half of what TLS needs is a mistake, and must not quietly
// * end up serving without TLS
func TestACertificateWithoutAKeyOrAKeyWithoutACertificateIsRefused(t *testing.T) {
	certificatePath, keyPath, _ := writeCertificate(t)

	for what, server := range map[string]Server{
		"a certificate without a key": {TLSCertificatePath: certificatePath},
		"a key without a certificate": {TLSKeyPath: keyPath},
	} {
		if config, err := server.TLSConfig(); err == nil {
			t.Errorf("%s gave %v and no error", what, config)
		}
	}
}

// * Writes a certificate for the domain the way the certificate manager
// * keeps one it fetched: the key, then the certificate
func writeCachedCertificate(t *testing.T, directory, domain string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{domain},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	contents := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	contents = append(contents, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)

	// * Kept under the domain followed by +rsa, as it has an RSA key
	if err := os.WriteFile(filepath.Join(directory, domain+"+rsa"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestACertificateKeptInTheCacheDirectoryIsUsedAfterARestart(t *testing.T) {
	// * Should the cache not be read, the certificate would be fetched
	// * again. This makes that fail at once instead of reaching Let's Encrypt
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")

	directory := t.TempDir()
	writeCachedCertificate(t, directory, "smm.example.com")

	config, err := Server{TLSDomain: "smm.example.com", TLSCacheDirectory: directory, LegacyTLS: true}.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}

	certificate, err := config.GetCertificate(&tls.ClientHelloInfo{ServerName: "smm.example.com", CipherSuites: consoleCipher})
	if err != nil {
		t.Fatalf("the kept certificate was not used: %v", err)
	}

	if certificate.Leaf == nil || certificate.Leaf.Subject.CommonName != "smm.example.com" {
		t.Errorf("got a certificate for %v, want the kept one", certificate.Leaf)
	}
}

func TestTLSConfigForADomainFetchesItsOwnCertificate(t *testing.T) {
	config, err := Server{TLSDomain: "smm.example.com", TLSCacheDirectory: t.TempDir(), LegacyTLS: true}.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}

	if config.GetCertificate == nil || len(config.Certificates) != 0 {
		t.Error("a server with a domain does not fetch its own certificate")
	}

	// * Without this, Let's Encrypt can not check the name on the same port
	answersTheCheck := false
	for _, protocol := range config.NextProtos {
		if protocol == "acme-tls/1" {
			answersTheCheck = true
		}
	}

	if !answersTheCheck || config.MinVersion != tls.VersionTLS10 {
		t.Errorf("protocols %v, minimum version %#x", config.NextProtos, config.MinVersion)
	}

	// * A name that is not this server's must not get a certificate
	// * fetched for it, as anyone can send any name
	if _, err := config.GetCertificate(&tls.ClientHelloInfo{ServerName: "someone-else.example.com"}); err == nil {
		t.Error("a certificate was offered for a name that is not this server's")
	}
}

// * Found on the live server: with legacy TLS on, a client that was given
// * the elliptic curve certificate and spoke TLS 1.2 was refused, as the
// * ciphers listed were all for the other kind of key
func TestLegacyTLSStillServesAClientGivenAnEllipticCurveCertificate(t *testing.T) {
	certificatePath, keyPath, trusted := writeEllipticCertificate(t)

	address := serveTLS(t, Server{TLSCertificatePath: certificatePath, TLSKeyPath: keyPath, LegacyTLS: true})

	for name, version := range map[string]uint16{"TLS 1.2": tls.VersionTLS12, "TLS 1.3": tls.VersionTLS13} {
		if _, err := handshake(address, trusted, version, nil); err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
}
