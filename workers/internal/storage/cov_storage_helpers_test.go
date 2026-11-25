package storage

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// covZObject e um objeto guardado no S3 falso.
type covZObject struct {
	body         []byte
	contentType  string
	storageClass string
}

// covZS3 e um servidor S3 minimo (path-style) em memoria: PUT/GET/HEAD/DELETE
// de um unico bucket. Serve para exercitar o Client sem rede externa.
type covZS3 struct {
	srv    *httptest.Server
	bucket string

	mu      sync.Mutex
	objects map[string]covZObject
	// requests registra "METODO /caminho" de cada chamada recebida.
	requests []string
	// deny faz toda chamada responder 403 AccessDenied (erro nao-retentavel).
	deny bool
	// authSeen guarda o header Authorization da ultima chamada.
	authSeen string
}

func covZNewS3(t *testing.T, bucket string) *covZS3 {
	t.Helper()
	f := &covZS3{bucket: bucket, objects: map[string]covZObject{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *covZS3) URL() string { return f.srv.URL }

func (f *covZS3) object(key string) (covZObject, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.objects[key]
	return o, ok
}

func (f *covZS3) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.requests))
	copy(out, f.requests)
	return out
}

func covZXMLError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>` + code + `</Code><Message>` + code + `</Message></Error>`))
}

func (f *covZS3) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	f.authSeen = r.Header.Get("Authorization")
	deny := f.deny
	f.mu.Unlock()

	if deny {
		covZXMLError(w, http.StatusForbidden, "AccessDenied")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	bucket, key, _ := strings.Cut(path, "/")
	if bucket != f.bucket {
		covZXMLError(w, http.StatusNotFound, "NoSuchBucket")
		return
	}

	switch r.Method {
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.objects[key] = covZObject{
			body:         body,
			contentType:  r.Header.Get("Content-Type"),
			storageClass: r.Header.Get("X-Amz-Storage-Class"),
		}
		f.mu.Unlock()
		w.Header().Set("ETag", `"covz-etag"`)
		w.WriteHeader(http.StatusOK)
	case http.MethodGet, http.MethodHead:
		o, ok := f.object(key)
		if !ok {
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			covZXMLError(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		w.Header().Set("Content-Type", o.contentType)
		w.Header().Set("Content-Length", itoa(len(o.body)))
		w.Header().Set("ETag", `"covz-etag"`)
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(o.body)
		}
	case http.MethodDelete:
		f.mu.Lock()
		delete(f.objects, key)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		covZXMLError(w, http.StatusMethodNotAllowed, "MethodNotAllowed")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
