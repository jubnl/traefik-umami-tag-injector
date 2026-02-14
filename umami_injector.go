// Package traefikumamitaginjector provides a Traefik middleware that injects an Umami tracking script
// into eligible HTML responses.
package traefikumamitaginjector

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/http"
	"strings"
)

type htmlCandidate int

const (
	candidateNo htmlCandidate = iota
	candidateMaybe
	candidateYes
)

// Config holds the plugin configuration as provided by Traefik dynamic configuration.
type Config struct {
	ScriptSrc           string `json:"scriptSrc,omitempty"`
	WebsiteID           string `json:"websiteId,omitempty"` // per-router override via labels
	DefaultWebsiteID    string `json:"defaultWebsiteId,omitempty"`
	WebsiteIDHeader     string `json:"websiteIdHeader,omitempty"`   // fallback, e.g. X-Analytics-Website-Id
	MaxLookaheadBytes   int    `json:"maxLookaheadBytes,omitempty"` // e.g. 131072 (128 KiB)
	InjectBefore        string `json:"injectBefore,omitempty"`      // default </head>
	AlsoMatchBodyClose  bool   `json:"alsoMatchBodyClose,omitempty"`
	StripAcceptEncoding bool   `json:"stripAcceptEncoding,omitempty"`
	InjectOnNon2xx      bool   `json:"injectOnNon2xx,omitempty"`
}

// CreateConfig creates the default plugin configuration.
func CreateConfig() *Config {
	return &Config{
		ScriptSrc:           "https://analytics.jubnl.ch/script.js",
		WebsiteID:           "",
		WebsiteIDHeader:     "X-Analytics-Website-Id",
		DefaultWebsiteID:    "c1df940e-066c-40df-a48a-fb0c92eac0a3",
		MaxLookaheadBytes:   32 * 1024,
		InjectBefore:        "</head>",
		AlsoMatchBodyClose:  true,
		StripAcceptEncoding: true,
		InjectOnNon2xx:      false,
	}
}

// Middleware is a Traefik HTTP middleware that injects an Umami tracking script into HTML responses.
type Middleware struct {
	next http.Handler

	scriptSrc           string
	websiteID           string
	defaultWebsiteID    string
	websiteIDHeader     string
	maxLookaheadBytes   int
	injectBefore        string
	alsoMatchBodyClose  bool
	stripAcceptEncoding bool
	injectOnNon2xx      bool
}

// New constructs a new Middleware instance.
func New(_ context.Context, next http.Handler, cfg *Config, _ string) (http.Handler, error) {
	return &Middleware{
		next: next,

		scriptSrc:           cfg.ScriptSrc,
		websiteID:           strings.TrimSpace(cfg.WebsiteID),
		defaultWebsiteID:    strings.TrimSpace(cfg.DefaultWebsiteID),
		websiteIDHeader:     cfg.WebsiteIDHeader,
		maxLookaheadBytes:   cfg.MaxLookaheadBytes,
		injectBefore:        cfg.InjectBefore,
		alsoMatchBodyClose:  cfg.AlsoMatchBodyClose,
		stripAcceptEncoding: cfg.StripAcceptEncoding,
		injectOnNon2xx:      cfg.InjectOnNon2xx,
	}, nil
}

func (m *Middleware) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		m.next.ServeHTTP(rw, req)
		return
	}

	if isUpgradeRequest(req) {
		m.next.ServeHTTP(rw, req)
		return
	}

	websiteID := strings.TrimSpace(m.websiteID)
	if websiteID == "" {
		websiteID = strings.TrimSpace(req.Header.Get(m.websiteIDHeader))
	}
	if websiteID == "" {
		websiteID = strings.TrimSpace(m.defaultWebsiteID)
	}
	if websiteID == "" {
		m.next.ServeHTTP(rw, req)
		return
	}

	reqToForward := req
	if m.stripAcceptEncoding {
		cloned := req.Clone(req.Context())
		cloned.Header = req.Header.Clone()
		cloned.Header.Del("Accept-Encoding")
		reqToForward = cloned
	}

	sw := newStreamWriter(
		rw,
		m.maxLookaheadBytes,
		m.scriptSrc,
		websiteID,
		m.injectBefore,
		m.alsoMatchBodyClose,
		m.injectOnNon2xx,
	)
	m.next.ServeHTTP(sw, reqToForward)

	sw.finish()
}

func isUpgradeRequest(r *http.Request) bool {
	conn := r.Header.Get("Connection")
	upg := r.Header.Get("Upgrade")
	if upg == "" {
		return false
	}
	return strings.Contains(strings.ToLower(conn), "upgrade")
}

type decision int

const (
	undecided decision = iota
	passthrough
	injecting
)

type streamWriter struct {
	orig http.ResponseWriter

	// captured
	header http.Header
	status int

	// decision state
	state          decision
	wroteHeader    bool
	headersFlushed bool
	lookaheadLimit int
	buf            bytes.Buffer

	// injection params
	scriptSrc          string
	websiteID          string
	injectBefore       string
	alsoMatchBodyClose bool
	injectOnNon2xx     bool
}

func newStreamWriter(orig http.ResponseWriter, lookaheadLimit int, scriptSrc, websiteID, injectBefore string, alsoMatchBodyClose bool, injectOnNon2xx bool) *streamWriter {
	if lookaheadLimit <= 0 {
		lookaheadLimit = 64 * 1024
	}

	return &streamWriter{
		orig: orig,

		header: make(http.Header),
		status: http.StatusOK,

		state:          undecided,
		lookaheadLimit: lookaheadLimit,

		scriptSrc:          scriptSrc,
		websiteID:          websiteID,
		injectBefore:       injectBefore,
		alsoMatchBodyClose: alsoMatchBodyClose,
		injectOnNon2xx:     injectOnNon2xx,
	}
}

func (w *streamWriter) Header() http.Header {
	return w.header
}

func (w *streamWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}

	w.wroteHeader = true
	w.status = statusCode
}

// Decide based on status + headers + (optional) sniffing.
func (w *streamWriter) htmlCandidateFromHeadersAndSniff(sample []byte) htmlCandidate {
	if !w.isStatusEligible() {
		return candidateNo
	}

	ct := strings.ToLower(w.header.Get("Content-Type"))

	// Explicit HTML => yes.
	if strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml+xml") {
		return candidateYes
	}

	// Explicit non-empty and NOT html => no.
	if strings.TrimSpace(ct) != "" {
		return candidateNo
	}

	// CT is empty => sniff the prefix.
	return sniffHTML(sample)
}

func (w *streamWriter) isStatusEligible() bool {
	if w.injectOnNon2xx {
		return w.status >= 200 && w.status < 600
	}
	return w.status >= 200 && w.status < 300
}

func sniffHTML(sample []byte) htmlCandidate {
	if len(sample) == 0 {
		return candidateMaybe
	}

	// Only need a small prefix.
	if len(sample) > 2048 {
		sample = sample[:2048]
	}

	lower := bytes.ToLower(sample)

	// Trim leading whitespace (best-effort).
	lower = bytes.TrimLeft(lower, " \t\r\n")

	// Strong HTML indicators.
	if bytes.HasPrefix(lower, []byte("<!doctype html")) {
		return candidateYes
	}
	if bytes.HasPrefix(lower, []byte("<html")) {
		return candidateYes
	}

	// Common early tags for HTML documents.
	if bytes.Contains(lower, []byte("<head")) || bytes.Contains(lower, []byte("<body")) {
		return candidateYes
	}

	// If it starts like XML, likely not HTML (unless xhtml, but that usually has CT set).
	if bytes.HasPrefix(lower, []byte("<?xml")) {
		return candidateNo
	}

	// Not enough evidence yet.
	return candidateMaybe
}

//nolint:funlen
func (w *streamWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	if w.state == passthrough {
		w.flushHeaders()
		return w.orig.Write(p)
	}

	if w.state == injecting {
		w.flushHeaders()
		return w.orig.Write(p)
	}

	if len(p) == 0 {
		return 0, nil
	}

	// Avoid corrupting compressed responses (unless you implement decompress/recompress).
	if w.header.Get("Content-Encoding") != "" {
		w.state = passthrough
		w.flushHeaders()
		w.flushBuffer()
		return w.orig.Write(p)
	}

	// Buffer up to lookaheadLimit.
	remaining := w.lookaheadLimit - w.buf.Len()

	if remaining <= 0 && w.state == undecided {
		// We can’t buffer more; fall back to passthrough.
		w.state = passthrough
		w.flushHeaders()
		w.flushBuffer()
		return w.orig.Write(p)
	}

	consumed := 0
	if remaining > 0 {
		if len(p) <= remaining {
			_, _ = w.buf.Write(p)
			consumed = len(p)
		} else {
			_, _ = w.buf.Write(p[:remaining])
			consumed = remaining
		}
	}

	bufBytes := w.buf.Bytes()

	// Decide if this is HTML (status + header or sniff).
	cand := w.htmlCandidateFromHeadersAndSniff(bufBytes)
	if cand == candidateNo {
		w.state = passthrough
		w.flushHeaders()
		w.flushBuffer()

		if consumed < len(p) {
			return w.orig.Write(p[consumed:])
		}
		return len(p), nil
	}

	// If already contains the script in buffered bytes, don’t inject.
	if bytes.Contains(bufBytes, []byte(w.scriptSrc)) {
		w.state = passthrough
		w.flushHeaders()
		w.flushBuffer()

		if consumed < len(p) {
			return w.orig.Write(p[consumed:])
		}
		return len(p), nil
	}

	// If maybe, keep buffering until we can decide or hit lookahead limit.
	if cand == candidateMaybe {
		if w.buf.Len() >= w.lookaheadLimit {
			w.state = passthrough
			w.flushHeaders()
			w.flushBuffer()

			if consumed < len(p) {
				return w.orig.Write(p[consumed:])
			}
			return len(p), nil
		}

		// Keep buffering; don't forward yet.
		return len(p), nil
	}

	// cand == candidateYes => try injection with current buffer.
	updated, ok := tryInject(bufBytes, w.scriptSrc, w.websiteID, w.injectBefore, w.alsoMatchBodyClose)
	if ok {
		w.state = injecting
		w.prepareHeadersForInjection()
		w.flushHeaders()

		_, err := w.orig.Write(updated)
		if err != nil {
			return len(p), err
		}

		if consumed < len(p) {
			_, err2 := w.orig.Write(p[consumed:])
			if err2 != nil {
				return len(p), err2
			}
		}

		w.buf.Reset()
		return len(p), nil
	}

	// Still HTML but couldn't inject yet; if we hit lookahead limit, give up.
	if w.buf.Len() >= w.lookaheadLimit {
		w.state = passthrough
		w.flushHeaders()
		w.flushBuffer()

		if consumed < len(p) {
			return w.orig.Write(p[consumed:])
		}
		return len(p), nil
	}

	return len(p), nil
}

func (w *streamWriter) prepareHeadersForInjection() {
	// Body changed -> strip potentially wrong validators/length.
	w.header.Del("Content-Length")
	w.header.Del("ETag")
}

func (w *streamWriter) flushHeaders() {
	if w.headersFlushed {
		return
	}

	dst := w.orig.Header()
	for k := range dst {
		dst.Del(k)
	}
	for k, vv := range w.header {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}

	w.orig.WriteHeader(w.status)
	w.headersFlushed = true
}

func (w *streamWriter) flushBuffer() {
	if w.buf.Len() == 0 {
		return
	}

	_, _ = w.orig.Write(w.buf.Bytes())
	w.buf.Reset()
}

func (w *streamWriter) finish() {
	if w.state == undecided {
		w.state = passthrough
		w.flushHeaders()
		w.flushBuffer()
	}
}

// tryInject attempts injection into the provided bytes (assumed to be the beginning of HTML).
// Returns (updated, true) if injected.
func tryInject(prefix []byte, scriptSrc, websiteID, injectBefore string, alsoMatchBodyClose bool) ([]byte, bool) {
	if len(prefix) == 0 {
		return nil, false
	}

	// Don’t inject twice (best-effort: check in lookahead).
	if bytes.Contains(prefix, []byte(scriptSrc)) {
		return nil, false
	}

	snippet := []byte(`<script defer src="` + scriptSrc + `" data-website-id="` + websiteID + `"></script>`)

	lower := bytes.ToLower(prefix)
	target := []byte(strings.ToLower(injectBefore))

	if idx := bytes.Index(lower, target); idx >= 0 {
		out := make([]byte, 0, len(prefix)+len(snippet))
		out = append(out, prefix[:idx]...)
		out = append(out, snippet...)
		out = append(out, prefix[idx:]...)
		return out, true
	}

	if alsoMatchBodyClose {
		if idx := bytes.Index(lower, []byte("</body>")); idx >= 0 {
			out := make([]byte, 0, len(prefix)+len(snippet))
			out = append(out, prefix[:idx]...)
			out = append(out, snippet...)
			out = append(out, prefix[idx:]...)
			return out, true
		}
	}

	return nil, false
}

// Flush implements http.Flusher. If we haven't decided yet whether to inject,
// we fall back to passthrough before flushing to avoid partial/invalid rewrites.
func (w *streamWriter) Flush() {
	if w.state == undecided {
		w.state = passthrough
		w.flushHeaders()
		w.flushBuffer()
	}

	if f, ok := w.orig.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *streamWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.orig.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}

	// If hijacking occurs, we must flush what we have and stop rewriting.
	if w.state == undecided {
		w.state = passthrough
		w.flushHeaders()
		w.flushBuffer()
	}

	return h.Hijack()
}
