package globals

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
	"time"
)

func certificate(t *testing.T, name string, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}

	signer, signerKey := template, key
	if parent != nil {
		signer, signerKey = parent, parentKey
	}

	der, err := x509.CreateCertificate(rand.Reader, template, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	return parsed, key, der
}

func TestParseRootCAGivesTheSameCertificateFromEitherForm(t *testing.T) {
	_, _, der := certificate(t, "Test Root", nil, nil)
	asPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	for name, input := range map[string][]byte{"DER": der, "PEM": asPEM} {
		got, err := ParseRootCA(input)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if !bytes.Equal(got, der) {
			t.Errorf("%s: did not get the certificate back in DER form", name)
		}
	}
}

func TestParseRootCARefusesWhatAGameCouldNotUse(t *testing.T) {
	root, rootKey, _ := certificate(t, "Test Root", nil, nil)
	_, _, signedByAnother := certificate(t, "Test Intermediate", root, rootKey)

	for name, input := range map[string][]byte{
		"a certificate signed by another": signedByAnother,
		"a private key":                   pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("key")}),
		"something else":                  []byte("not a certificate"),
		"nothing":                         nil,
	} {
		if got, err := ParseRootCA(input); err == nil {
			t.Errorf("%s: accepted, giving %d bytes", name, len(got))
		}
	}
}

// * The root the AWS deployment hands to games must be one a game can use
func TestTheDeploymentsRootCAIsUsable(t *testing.T) {
	data, err := os.ReadFile("../deploy/tofu/certificates/lets-encrypt-root-yr.pem")
	if err != nil {
		t.Fatal(err)
	}

	der, err := ParseRootCA(data)
	if err != nil {
		t.Fatal(err)
	}

	root, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	// * A game is given a certificate with an RSA key, so the root has
	// * to be the one for those
	if root.PublicKeyAlgorithm != x509.RSA || root.Subject.CommonName != "Root YR" {
		t.Errorf("the root is %q with a %v key, want Root YR with an RSA key", root.Subject.CommonName, root.PublicKeyAlgorithm)
	}
}
