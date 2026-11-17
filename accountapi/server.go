package accountapi

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// Server serves the account API over HTTP, or over HTTPS
// when given a certificate and key
type Server struct {
	Config Config
	Port   int

	// TLSCertificatePath and TLSKeyPath are the certificate and
	// private key to serve HTTPS with. HTTP is served when both are empty
	TLSCertificatePath string
	TLSKeyPath         string

	// TLSDomain, when set, is the name games reach this server by. A
	// certificate for it is fetched from Let's Encrypt and kept up to
	// date, and TLSCertificatePath and TLSKeyPath are not used. The
	// server must be reachable by that name on port 443, as that is how
	// Let's Encrypt checks the name is this server's
	TLSDomain string
	// TLSCacheDirectory is where fetched certificates are kept, so that
	// a restart does not fetch them again
	TLSCacheDirectory string

	// LegacyTLS also accepts TLS 1.0 and 1.1 and the ciphers of their
	// time, which is all a Wii U or 3DS speaks. They are weaker than what
	// is used today, and are only fit for a server that does nothing but
	// serve games
	LegacyTLS bool

	// Files, when set, is served under FilesPath on the same port, so
	// that games reach course files through this server and with its
	// certificate
	Files     http.Handler
	FilesPath string
}

// * What a current client asks for, followed by what an old console can
// * do. A client that can do better is given better, as the server's
// * order is the one used
var legacyCipherSuites = []uint16{
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
	tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
	tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_RSA_WITH_AES_128_CBC_SHA,
	tls.TLS_RSA_WITH_AES_256_CBC_SHA,
}

// TLSConfig returns how connections are secured, or nil when the server
// is served without TLS
func (s Server) TLSConfig() (*tls.Config, error) {
	usesFiles := s.TLSCertificatePath != "" || s.TLSKeyPath != ""

	if s.TLSDomain == "" && !usesFiles {
		if s.LegacyTLS {
			return nil, errors.New("legacy TLS was asked for, but there is no certificate to serve TLS with")
		}

		return nil, nil
	}

	config := &tls.Config{MinVersion: tls.VersionTLS12}

	if s.LegacyTLS {
		config.MinVersion = tls.VersionTLS10
		config.CipherSuites = legacyCipherSuites
	}

	if s.TLSDomain != "" {
		manager := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(s.TLSDomain),
		}

		if s.TLSCacheDirectory != "" {
			manager.Cache = autocert.DirCache(s.TLSCacheDirectory)
		}

		// * An old console can only check a certificate with an RSA key
		if s.LegacyTLS {
			manager.ForceRSA = true //nolint:staticcheck // the only way to ask for an RSA key
		}

		config.GetCertificate = manager.GetCertificate
		// * Lets Let's Encrypt check the name over the same port
		config.NextProtos = []string{"h2", "http/1.1", acme.ALPNProto}

		return config, nil
	}

	certificate, err := tls.LoadX509KeyPair(s.TLSCertificatePath, s.TLSKeyPath)
	if err != nil {
		return nil, err
	}

	config.Certificates = []tls.Certificate{certificate}

	return config, nil
}

// ListenAndServe serves the account API until it fails
func (s Server) ListenAndServe() error {
	handler, err := Handler(s.Config)
	if err != nil {
		return err
	}

	if s.Files != nil {
		mux := http.NewServeMux()
		mux.Handle(s.FilesPath, s.Files)
		mux.Handle(s.FilesPath+"/", s.Files)
		mux.Handle("/", handler)

		handler = mux
	}

	httpServer := &http.Server{
		Addr:    net.JoinHostPort("", strconv.Itoa(s.Port)),
		Handler: handler,

		// * Stops slow or stalled clients from holding connections open
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 * 1024,
	}

	tlsConfig, err := s.TLSConfig()
	if err != nil {
		return err
	}

	if tlsConfig != nil {
		httpServer.TLSConfig = tlsConfig

		// * The certificate comes from the configuration
		return httpServer.ListenAndServeTLS("", "")
	}

	return httpServer.ListenAndServe()
}
