package accountapi

import (
	"net"
	"net/http"
	"strconv"
	"time"
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
}

// ListenAndServe serves the account API until it fails
func (s Server) ListenAndServe() error {
	handler, err := Handler(s.Config)
	if err != nil {
		return err
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

	if s.TLSCertificatePath != "" || s.TLSKeyPath != "" {
		return httpServer.ListenAndServeTLS(s.TLSCertificatePath, s.TLSKeyPath)
	}

	return httpServer.ListenAndServe()
}
