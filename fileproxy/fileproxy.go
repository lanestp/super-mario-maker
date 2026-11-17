// Package fileproxy lets games upload and download course files through
// the server, so that the storage behind it is never reached directly.
//
// A server normally hands a game a link to the storage itself. With this
// package the link points back at the server. That keeps the storage
// private, puts every transfer behind the server's own certificate, and
// gives the server a place to check what is uploaded.
//
// A link is only good for one file and for a short time. It carries a
// signature made with a secret only the server knows, so a game can not
// read or write any file it was not handed a link for
package fileproxy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Path is where the proxy is served
const Path = "/v1/files"

// DefaultMaxUploadBytes is the largest upload taken unless set otherwise.
// A course file is at most a few hundred kilobytes
const DefaultMaxUploadBytes = 4 << 20

// * The most a form may hold besides the file itself
const maxFormFieldBytes = 4 << 10

// ErrNotFound is returned by a Storage that does not have a file
var ErrNotFound = errors.New("file not found")

// Storage is where files are kept
type Storage interface {
	Put(ctx context.Context, key string, data []byte) error
	// Get returns a file and its size. It returns ErrNotFound when there is no such file
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
}

// Proxy makes links to files and serves them
type Proxy struct {
	// BaseURL is how games reach this server, such as https://smm.example.com
	BaseURL *url.URL
	// Secret signs links. Links made with another secret are refused
	Secret  []byte
	Storage Storage

	// MaxUploadBytes is the largest upload taken. DefaultMaxUploadBytes when 0
	MaxUploadBytes int64
	// Now is the clock. time.Now when nil
	Now func() time.Time
	// LogError is told about storage errors. They are never sent to a game
	LogError func(error)
}

func (p *Proxy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}

	return time.Now()
}

func (p *Proxy) maxUploadBytes() int64 {
	if p.MaxUploadBytes > 0 {
		return p.MaxUploadBytes
	}

	return DefaultMaxUploadBytes
}

func (p *Proxy) logError(err error) {
	if p.LogError != nil {
		p.LogError(err)
	}
}

// * A signature covers what the link may be used for, which file, and
// * until when, so that none of the three can be changed
func (p *Proxy) sign(method, key string, expires int64) string {
	mac := hmac.New(sha256.New, p.Secret)
	fmt.Fprintf(mac, "%s\n%s\n%d", method, key, expires)

	return hex.EncodeToString(mac.Sum(nil))
}

func (p *Proxy) signatureIsGood(method, key, expires, signature string) bool {
	until, err := strconv.ParseInt(expires, 10, 64)
	if err != nil || key == "" {
		return false
	}

	// * Compared in constant time, and before the time is looked at, so
	// * that nothing is learned about a signature from how long this takes
	good := subtle.ConstantTimeCompare([]byte(p.sign(method, key, until)), []byte(signature)) == 1

	return good && p.now().Unix() <= until
}

// GetObject returns a link a game can download the file from. The bucket
// is not used, as the storage already knows where it keeps files
func (p *Proxy) GetObject(_, key string, lifetime time.Duration) (*url.URL, error) {
	expires := p.now().Add(lifetime).Unix()

	link := p.BaseURL.JoinPath(Path, key)
	link.RawQuery = url.Values{
		"expires":   {strconv.FormatInt(expires, 10)},
		"signature": {p.sign(http.MethodGet, key, expires)},
	}.Encode()

	return link, nil
}

// PostObject returns a link a game can upload the file to, and the form
// fields it has to send along with the file
func (p *Proxy) PostObject(_, key string, lifetime time.Duration) (*url.URL, map[string]string, error) {
	expires := p.now().Add(lifetime).Unix()

	return p.BaseURL.JoinPath(Path), map[string]string{
		"key":       key,
		"expires":   strconv.FormatInt(expires, 10),
		"signature": p.sign(http.MethodPost, key, expires),
	}, nil
}

// Handler serves the links. Mount it at Path and at Path followed by a slash
func (p *Proxy) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// * A file is never to be shown as a page, whatever it holds
		w.Header().Set("X-Content-Type-Options", "nosniff")

		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, Path+"/"):
			p.download(w, r, strings.TrimPrefix(r.URL.Path, Path+"/"))
		case r.Method == http.MethodPost && r.URL.Path == Path:
			p.upload(w, r)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	})
}

func (p *Proxy) download(w http.ResponseWriter, r *http.Request, key string) {
	query := r.URL.Query()

	if !p.signatureIsGood(http.MethodGet, key, query.Get("expires"), query.Get("signature")) {
		http.Error(w, "this link is not valid, or no longer valid", http.StatusForbidden)
		return
	}

	file, size, err := p.Storage.Get(r.Context(), key)
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if err != nil {
		p.logError(fmt.Errorf("downloading %s: %w", key, err))
		http.Error(w, "the file could not be read", http.StatusBadGateway)

		return
	}

	defer file.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))

	if _, err := io.Copy(w, file); err != nil {
		p.logError(fmt.Errorf("downloading %s: %w", key, err))
	}
}

func (p *Proxy) upload(w http.ResponseWriter, r *http.Request) {
	fields, file, status, err := p.readForm(w, r)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}

	key := fields["key"]

	if !p.signatureIsGood(http.MethodPost, key, fields["expires"], fields["signature"]) {
		http.Error(w, "this link is not valid, or no longer valid", http.StatusForbidden)
		return
	}

	if file == nil {
		http.Error(w, "the form has no file", http.StatusBadRequest)
		return
	}

	if err := p.Storage.Put(r.Context(), key, file); err != nil {
		p.logError(fmt.Errorf("uploading %s: %w", key, err))
		http.Error(w, "the file could not be stored", http.StatusBadGateway)

		return
	}

	// * What S3 answers to an upload form, which is what games expect
	w.WriteHeader(http.StatusNoContent)
}

// * Reads an upload form: its fields, and the part named "file". A
// * console sends the file part without a file name, which the standard
// * form reader would take for a field, so the parts are read one by one.
// * A missing file is returned as nil, and an empty one as no bytes
func (p *Proxy) readForm(w http.ResponseWriter, r *http.Request) (map[string]string, []byte, int, error) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return nil, nil, http.StatusBadRequest, errors.New("an upload must be a multipart form")
	}

	limit := p.maxUploadBytes()

	// * Nothing past the largest form there could be is read at all
	body := http.MaxBytesReader(w, r.Body, limit+16*maxFormFieldBytes)
	reader := multipart.NewReader(body, params["boundary"])

	fields := map[string]string{}
	var file []byte

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, nil, http.StatusBadRequest, errors.New("the form could not be read, or is too large")
		}

		name := part.FormName()
		partLimit := int64(maxFormFieldBytes)

		if name == "file" {
			partLimit = limit
		}

		// * One byte more than allowed is read, to tell a part that is
		// * too large from one that is exactly as large as allowed
		data, err := io.ReadAll(io.LimitReader(part, partLimit+1))
		if err != nil {
			return nil, nil, http.StatusBadRequest, errors.New("the form could not be read, or is too large")
		}

		if int64(len(data)) > partLimit {
			return nil, nil, http.StatusRequestEntityTooLarge, fmt.Errorf("the %s part of the form is too large", name)
		}

		if name == "file" {
			if file != nil {
				return nil, nil, http.StatusBadRequest, errors.New("the form has more than one file")
			}

			// * Not nil, so that an empty file is told from a missing one
			file = append([]byte{}, data...)

			continue
		}

		if len(fields) >= 16 {
			return nil, nil, http.StatusBadRequest, errors.New("the form has too many fields")
		}

		fields[name] = string(data)
	}

	return fields, file, http.StatusOK, nil
}
