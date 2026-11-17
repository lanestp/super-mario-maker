package globals

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
)

// ParseRootCA reads a certificate in PEM or DER form, and returns it in
// DER form, which is what a game is sent. Only the first certificate of
// a PEM file is used, as a game is handed a single certificate
func ParseRootCA(data []byte) ([]byte, error) {
	der := data

	if block, _ := pem.Decode(data); block != nil {
		if block.Type != "CERTIFICATE" {
			return nil, errors.New("the file holds a " + block.Type + ", not a certificate")
		}

		der = block.Bytes
	}

	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}

	// * A game stops at a certificate that signed itself. One that was
	// * signed by another is of no use to it without that other one
	if err := certificate.CheckSignatureFrom(certificate); err != nil {
		return nil, errors.New("the certificate is not a root, as it was not signed by itself")
	}

	return der, nil
}
