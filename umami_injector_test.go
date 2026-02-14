package traefikumamitaginjector

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestMiddleware(t *testing.T, next http.Handler, cfg *Config) http.Handler {
	t.Helper()

	h, err := New(context.Background(), next, cfg, "traefikumamitaginjector")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	return h
}

func mustContain(t *testing.T, haystack, needle, msg string) {
	t.Helper()

	if !strings.Contains(haystack, needle) {
		t.Fatalf("%s: expected to contain %q, got body=%q", msg, needle, haystack)
	}
}

func mustNotContain(t *testing.T, haystack, needle, msg string) {
	t.Helper()

	if strings.Contains(haystack, needle) {
		t.Fatalf("%s: expected NOT to contain %q, got body=%q", msg, needle, haystack)
	}
}

func scriptSnippet(src, websiteID string) string {
	return `<script defer src="` + src + `" data-website-id="` + websiteID + `"></script>`
}

func Test_Passthrough_WhenNotGET(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte("<html><head></head><body>Hello</body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid-config"
	cfg.DefaultWebsiteID = "" // keep test explicit

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodPost, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	mustNotContain(t, rr.Body.String(), cfg.ScriptSrc, "non-GET should passthrough")
}

func Test_ConfigWebsiteID_TakesPrecedence_OverHeader_AndDefault(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte("<html><head></head><body>Hello</body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid-from-config"
	cfg.WebsiteIDHeader = "X-Analytics-Website-Id"
	cfg.DefaultWebsiteID = "uuid-default"

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.Header.Set(cfg.WebsiteIDHeader, "uuid-from-header")

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	body := rr.Body.String()
	mustContain(t, body, `data-website-id="uuid-from-config"`, "config websiteId should win")
	mustNotContain(t, body, `data-website-id="uuid-from-header"`, "header should not be used when config set")
	mustNotContain(t, body, `data-website-id="uuid-default"`, "default should not be used when config set")
}

func Test_HeaderWebsiteID_IsUsed_WhenConfigWebsiteIDEmpty(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte("<html><head></head><body>Hello</body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = ""
	cfg.WebsiteIDHeader = "X-Analytics-Website-Id"
	cfg.DefaultWebsiteID = "uuid-default"

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.Header.Set(cfg.WebsiteIDHeader, "uuid-from-header")
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	body := rr.Body.String()
	mustContain(t, body, `data-website-id="uuid-from-header"`, "header websiteId should be used")
	mustNotContain(t, body, `data-website-id="uuid-default"`, "default should not be used when header present")
}

func Test_DefaultWebsiteID_IsUsed_WhenConfigAndHeaderEmpty(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte("<html><head></head><body>Hello</body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = ""
	cfg.WebsiteIDHeader = "X-Analytics-Website-Id"
	cfg.DefaultWebsiteID = "uuid-default"

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	body := rr.Body.String()
	mustContain(t, body, `data-website-id="uuid-default"`, "default websiteId should be used")
}

func Test_Passthrough_WhenNoWebsiteID_ConfigHeaderDefaultAllEmpty(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte("<html><head></head><body>Hello</body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = ""
	cfg.WebsiteIDHeader = "X-Analytics-Website-Id"
	cfg.DefaultWebsiteID = ""

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	mustNotContain(t, rr.Body.String(), cfg.ScriptSrc, "should not inject when no website id anywhere")
}

func Test_Inserts_WhenContentTypeMissing_ButHTMLSniffDetects(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		// Intentionally do NOT set Content-Type.
		_, _ = rw.Write([]byte("<!doctype html><html><head></head><body>Hello</body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid"
	cfg.DefaultWebsiteID = ""

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	body := rr.Body.String()
	mustContain(t, body, scriptSnippet(cfg.ScriptSrc, "uuid"), "should inject with sniffed HTML even when CT missing")
}

func Test_Passthrough_WhenContentTypeMissing_AndSniffNotHTML(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		// No Content-Type and not HTML at the beginning.
		_, _ = rw.Write([]byte("{\"ok\":true}"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid"
	cfg.DefaultWebsiteID = ""

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	mustNotContain(t, rr.Body.String(), cfg.ScriptSrc, "should not inject when sniff doesn't look like HTML")
}

func Test_Passthrough_WhenStatusNon2xx_AndInjectOnNon2xxFalse(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html")
		rw.WriteHeader(http.StatusNotFound)
		_, _ = rw.Write([]byte("<html><head></head><body>404</body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid"
	cfg.DefaultWebsiteID = ""
	cfg.InjectOnNon2xx = false

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/missing", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 passthrough, got %d", rr.Code)
	}
	mustNotContain(t, rr.Body.String(), cfg.ScriptSrc, "should not inject on 404 when InjectOnNon2xx=false")
}

func Test_Injects_WhenStatusNon2xx_AndInjectOnNon2xxTrue(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html")
		rw.WriteHeader(http.StatusNotFound)
		_, _ = rw.Write([]byte("<html><head></head><body>404</body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid"
	cfg.DefaultWebsiteID = ""
	cfg.InjectOnNon2xx = true

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/missing", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 passthrough status preserved, got %d", rr.Code)
	}
	mustContain(t, rr.Body.String(), scriptSnippet(cfg.ScriptSrc, "uuid"), "should inject on 404 when InjectOnNon2xx=true")
}

func Test_Passthrough_WhenNotHTML(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"ok":true}`))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid"
	cfg.DefaultWebsiteID = ""

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	mustNotContain(t, rr.Body.String(), cfg.ScriptSrc, "should not inject into json")
}

func Test_Passthrough_WhenCompressed(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Encoding", "gzip")
		rw.Header().Set("Content-Type", "text/html")
		_, _ = rw.Write([]byte("<html><head></head><body>fake gzip</body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid"
	cfg.DefaultWebsiteID = ""

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	mustNotContain(t, rr.Body.String(), cfg.ScriptSrc, "should not inject when Content-Encoding is set")
	if rr.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected Content-Encoding preserved")
	}
}

func Test_DoesNotInjectTwice_WhenScriptAlreadyPresent(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html")
		_, _ = rw.Write([]byte(`<html><head><script defer src="https://analytics.jubnl.ch/script.js"></script></head></html>`))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid"
	cfg.DefaultWebsiteID = ""
	cfg.ScriptSrc = "https://analytics.jubnl.ch/script.js"

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if strings.Count(rr.Body.String(), cfg.ScriptSrc) != 1 {
		t.Fatalf("expected exactly one script occurrence (no double-injection)")
	}
}

func Test_HeaderCleanup_OnInjection(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("ETag", "abc")
		rw.Header().Set("Content-Length", "999")
		rw.Header().Set("Content-Type", "text/html")
		_, _ = rw.Write([]byte("<html><head></head></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid"
	cfg.DefaultWebsiteID = ""

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if rr.Header().Get("ETag") != "" {
		t.Fatalf("expected ETag removed after injection")
	}
	if rr.Header().Get("Content-Length") != "" {
		t.Fatalf("expected Content-Length removed after injection")
	}
}

func Test_FallbackToBodyClose_WhenNoHeadClose(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html")
		_, _ = rw.Write([]byte("<html><body></body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid"
	cfg.DefaultWebsiteID = ""
	cfg.InjectBefore = "</head>"
	cfg.AlsoMatchBodyClose = true

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	body := rr.Body.String()
	mustContain(t, body, cfg.ScriptSrc, "should inject at </body> fallback")
}

func Test_RemainingGuard_FallsBackToPassthrough_WithoutTruncation(t *testing.T) {
	// Goal: exercise the "remaining <= 0 => passthrough" guard path.
	// We write two chunks:
	//   - first chunk fills lookahead completely with content that keeps candidateMaybe (CT empty, no html markers)
	//   - second chunk should be streamed immediately by the guard, not buffered/stalled/dropped.

	chunk1 := bytes.Repeat([]byte("X"), 1024)
	chunk2 := bytes.Repeat([]byte("Y"), 256)

	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		// No Content-Type set => sniffing used.
		_, _ = rw.Write(chunk1)
		_, _ = rw.Write(chunk2)
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid"
	cfg.DefaultWebsiteID = ""
	cfg.MaxLookaheadBytes = len(chunk1) // fill buffer exactly

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	got := rr.Body.Bytes()
	want := append(append([]byte{}, chunk1...), chunk2...)
	if !bytes.Equal(got, want) {
		t.Fatalf("expected full passthrough without truncation; got len=%d want=%d", len(got), len(want))
	}
	mustNotContain(t, rr.Body.String(), cfg.ScriptSrc, "should not inject for non-html payload")
}

func Test_StripAcceptEncoding_True_StripsHeaderBeforeUpstream(t *testing.T) {
	var sawAcceptEncoding string
	next := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		sawAcceptEncoding = r.Header.Get("Accept-Encoding")

		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte("<html><head></head><body>OK</body></html>"))
	})

	cfg := CreateConfig()
	cfg.StripAcceptEncoding = true
	cfg.WebsiteID = "uuid"
	cfg.DefaultWebsiteID = ""

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if sawAcceptEncoding != "" {
		t.Fatalf("expected Accept-Encoding to be stripped before upstream, got %q", sawAcceptEncoding)
	}
	mustContain(t, rr.Body.String(), cfg.ScriptSrc, "expected injection")
}

func Test_StripAcceptEncoding_False_DoesNotStripHeaderBeforeUpstream(t *testing.T) {
	var sawAcceptEncoding string
	next := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		sawAcceptEncoding = r.Header.Get("Accept-Encoding")

		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte("<html><head></head><body>OK</body></html>"))
	})

	cfg := CreateConfig()
	cfg.StripAcceptEncoding = false
	cfg.WebsiteID = "uuid"
	cfg.DefaultWebsiteID = ""

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	if sawAcceptEncoding != "gzip, br" {
		t.Fatalf("expected Accept-Encoding to be preserved before upstream, got %q", sawAcceptEncoding)
	}
	mustContain(t, rr.Body.String(), cfg.ScriptSrc, "expected injection")
}
