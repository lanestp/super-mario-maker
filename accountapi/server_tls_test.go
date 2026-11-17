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
