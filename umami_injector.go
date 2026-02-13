package traefikumamitaginjector

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/http"
	"strings"
)

// Config holds the plugin configuration as provided by Traefik dynamic configuration.
type Config struct {
	ScriptSrc          string `json:"scriptSrc,omitempty"`
	WebsiteID          string `json:"websiteId,omitempty"`         // NEW: allows per-router config via labels
	WebsiteIDHeader    string `json:"websiteIdHeader,omitempty"`   // fallback, e.g. X-Analytics-Website-Id
	MaxLookaheadBytes  int    `json:"maxLookaheadBytes,omitempty"` // e.g. 131072 (128 KiB)
	InjectBefore       string `json:"injectBefore,omitempty"`      // default </head>
	AlsoMatchBodyClose bool   `json:"alsoMatchBodyClose,omitempty"`
}

// CreateConfig creates the default plugin configuration.
func CreateConfig() *Config {
	return &Config{
		ScriptSrc:          "https://analytics.jubnl.ch/script.js",
		WebsiteID:          "", // per-site; empty means "use header fallback"
		WebsiteIDHeader:    "X-Analytics-Website-Id",
		MaxLookaheadBytes:  128 * 1024,
		InjectBefore:       "</head>",
		AlsoMatchBodyClose: true,
	}
}

// Middleware is a Traefik HTTP middleware that injects an Umami tracking script into HTML responses.
type Middleware struct {
	next http.Handler

	scriptSrc          string
	websiteID          string // NEW
	websiteIDHeader    string
	maxLookaheadBytes  int
	injectBefore       string
	alsoMatchBodyClose bool
}

// New constructs a new Middleware instance.
func New(_ context.Context, next http.Handler, cfg *Config, _ string) (http.Handler, error) {
	return &Middleware{
		next: next,

		scriptSrc:          cfg.ScriptSrc,
		websiteID:          strings.TrimSpace(cfg.WebsiteID), // NEW
		websiteIDHeader:    cfg.WebsiteIDHeader,
		maxLookaheadBytes:  cfg.MaxLookaheadBytes,
		injectBefore:       cfg.InjectBefore,
		alsoMatchBodyClose: cfg.AlsoMatchBodyClose,
	}, nil
}

func (m *Middleware) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		m.next.ServeHTTP(rw, req)
		return
	}

	// Skip websocket/upgrade traffic (GET can be websockets).
	if isUpgradeRequest(req) {
		m.next.ServeHTTP(rw, req)
		return
	}

	// NEW: prefer middleware config WebsiteID (labels), fallback to request header if empty.
	websiteID := m.websiteID
	if websiteID == "" {
		websiteID = strings.TrimSpace(req.Header.Get(m.websiteIDHeader))
	}
	if websiteID == "" {
		m.next.ServeHTTP(rw, req)
		return
	}

	sw := newStreamWriter(rw, m.maxLookaheadBytes, m.scriptSrc, websiteID, m.injectBefore, m.alsoMatchBodyClose)
	m.next.ServeHTTP(sw, req)

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
}

func newStreamWriter(orig http.ResponseWriter, lookaheadLimit int, scriptSrc, websiteID, injectBefore string, alsoMatchBodyClose bool) *streamWriter {
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

	// undecided: buffer up to lookaheadLimit, decide ASAP.
	if len(p) == 0 {
		return 0, nil
	}

	// If response obviously not eligible, decide passthrough immediately.
	if !w.isCandidateHTML() {
		w.state = passthrough
		w.flushHeaders()
		w.flushBuffer()
		return w.orig.Write(p)
	}

	// Avoid corrupting compressed responses (unless you implement decompress/recompress).
	if w.header.Get("Content-Encoding") != "" {
		w.state = passthrough
		w.flushHeaders()
		w.flushBuffer()
		return w.orig.Write(p)
	}

	// If already contains the script in the early bytes, don’t inject.
	if w.buf.Len() > 0 && bytes.Contains(w.buf.Bytes(), []byte(w.scriptSrc)) {
		w.state = passthrough
		w.flushHeaders()
		w.flushBuffer()
		return w.orig.Write(p)
	}

	remaining := w.lookaheadLimit - w.buf.Len()
	if remaining > 0 {
		if len(p) <= remaining {
			_, _ = w.buf.Write(p)
		} else {
			_, _ = w.buf.Write(p[:remaining])
			// We’ll passthrough the rest below if we can’t decide.
		}
	}

	updated, ok := tryInject(w.buf.Bytes(), w.scriptSrc, w.websiteID, w.injectBefore, w.alsoMatchBodyClose)
	if ok {
		w.state = injecting
		w.prepareHeadersForInjection()
		w.flushHeaders()

		_, err := w.orig.Write(updated)
		if err != nil {
			return len(p), err
		}

		// If p had extra beyond the lookahead, write it too.
		if remaining < len(p) {
			_, err2 := w.orig.Write(p[remaining:])
			if err2 != nil {
				return len(p), err2
			}
		}

		// Clear buffer (we already wrote it).
		w.buf.Reset()
		return len(p), nil
	}

	// If we reached lookahead limit and still can’t inject, switch to passthrough.
	if w.buf.Len() >= w.lookaheadLimit {
		w.state = passthrough
		w.flushHeaders()
		w.flushBuffer()

		if remaining < len(p) {
			return w.orig.Write(p[remaining:])
		}

		return len(p), nil
	}

	// Still undecided, keep buffering.
	return len(p), nil
}

func (w *streamWriter) isCandidateHTML() bool {
	if w.status < 200 || w.status >= 300 {
		return false
	}

	ct := strings.ToLower(w.header.Get("Content-Type"))
	if strings.Contains(ct, "text/html") {
		return true
	}
	return strings.Contains(ct, "application/xhtml+xml")
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

// Support Flush/Hijack to not break other handlers.
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
