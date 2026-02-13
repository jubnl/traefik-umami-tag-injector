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

func Test_Passthrough_WhenNoWebsiteID_ConfigOrHeader(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte("<html><head></head><body>Hello</body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = ""
	cfg.WebsiteIDHeader = "X-Analytics-Website-Id"

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if strings.Contains(rr.Body.String(), cfg.ScriptSrc) {
		t.Fatalf("expected no injection when no website id is provided")
	}
}

func Test_Passthrough_WhenWebsiteIDHeaderIsWhitespace(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte("<html><head></head><body>Hello</body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = ""
	cfg.WebsiteIDHeader = "X-Analytics-Website-Id"

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.Header.Set(cfg.WebsiteIDHeader, "   \t\n")
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if strings.Contains(rr.Body.String(), cfg.ScriptSrc) {
		t.Fatalf("expected no injection when header is whitespace")
	}
}

func Test_Passthrough_WhenNotGET(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte("<html><head></head><body>Hello</body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid-config"

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodPost, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if strings.Contains(rr.Body.String(), cfg.ScriptSrc) {
		t.Fatalf("expected no injection for non-GET")
	}
}

func Test_ConfigWebsiteID_TakesPrecedence_OverHeader(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte("<html><head></head><body>Hello</body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid-from-config"
	cfg.WebsiteIDHeader = "X-Analytics-Website-Id"
	cfg.MaxLookaheadBytes = 32 * 1024

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.Header.Set(cfg.WebsiteIDHeader, "uuid-from-header")
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, `data-website-id="uuid-from-config"`) {
		t.Fatalf("expected config websiteId to be used, got body=%q", body)
	}
	if strings.Contains(body, `data-website-id="uuid-from-header"`) {
		t.Fatalf("did not expect header websiteId to be used when config is set, got body=%q", body)
	}
}

func Test_HeaderWebsiteID_IsUsed_WhenConfigWebsiteIDEmpty(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte("<html><head></head><body>Hello</body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = ""
	cfg.WebsiteIDHeader = "X-Analytics-Website-Id"
	cfg.MaxLookaheadBytes = 32 * 1024

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.Header.Set(cfg.WebsiteIDHeader, "uuid-from-header")
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, `data-website-id="uuid-from-header"`) {
		t.Fatalf("expected header websiteId to be used, got body=%q", body)
	}
}

func Test_Inserts_OnLargeBody_WhenHeadCloseEarly(t *testing.T) {
	const websiteID = "014f4608-ab91-44f8-a046-749b8593ada9"

	head := "<html><head><title>x</title></head><body>"
	tail := "</body></html>"
	huge := bytes.Repeat([]byte("A"), 2*1024*1024)

	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte(head))
		_, _ = rw.Write(huge)
		_, _ = rw.Write([]byte(tail))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = websiteID
	cfg.MaxLookaheadBytes = 32 * 1024

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	body := rr.Body.String()
	want := `<script defer src="` + cfg.ScriptSrc + `" data-website-id="` + websiteID + `"></script>`

	if !strings.Contains(body, want) {
		t.Fatalf("expected injection")
	}
	if !strings.Contains(body, tail) {
		t.Fatalf("expected full passthrough tail")
	}

	headCloseIdx := strings.Index(strings.ToLower(body), "</head>")
	snippetIdx := strings.Index(body, want)
	if snippetIdx < 0 || headCloseIdx < 0 || snippetIdx > headCloseIdx {
		t.Fatalf("expected snippet before </head>")
	}
}

func Test_Passthrough_WhenHeadNotInLookahead(t *testing.T) {
	const websiteID = "uuid"

	prefix := bytes.Repeat([]byte("X"), 64*1024)
	html := append([]byte("<html><head>"), prefix...)
	html = append(html, []byte("</head><body>OK</body></html>")...)

	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write(html)
	})

	cfg := CreateConfig()
	cfg.WebsiteID = websiteID
	cfg.MaxLookaheadBytes = 8 * 1024 // too small to include </head>

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if strings.Contains(rr.Body.String(), cfg.ScriptSrc) {
		t.Fatalf("expected no injection when </head> not found within lookahead")
	}
	if rr.Body.Len() != len(html) {
		t.Fatalf("expected passthrough (no truncation), got len=%d want=%d", rr.Body.Len(), len(html))
	}
}

func Test_Passthrough_WhenNotHTML(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"ok":true}`))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid"

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if strings.Contains(rr.Body.String(), cfg.ScriptSrc) {
		t.Fatalf("expected no injection for json")
	}
}

func Test_Passthrough_WhenCompressed(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Encoding", "gzip")
		rw.Header().Set("Content-Type", "text/html")
		_, _ = rw.Write([]byte("<html><head></head><body>fake gzip</body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid"

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if strings.Contains(rr.Body.String(), cfg.ScriptSrc) {
		t.Fatalf("expected no injection when Content-Encoding is set")
	}
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

func Test_CaseInsensitiveHeadClose(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html")
		_, _ = rw.Write([]byte("<html><HEAD></HEAD><body>OK</body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid"
	cfg.InjectBefore = "</head>"

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if !strings.Contains(rr.Body.String(), cfg.ScriptSrc) {
		t.Fatalf("expected injection with case-insensitive </head>")
	}
}

func Test_FallbackToBodyClose(t *testing.T) {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/html")
		_, _ = rw.Write([]byte("<html><body></body></html>"))
	})

	cfg := CreateConfig()
	cfg.WebsiteID = "uuid"
	cfg.InjectBefore = "</head>"
	cfg.AlsoMatchBodyClose = true

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if !strings.Contains(rr.Body.String(), cfg.ScriptSrc) {
		t.Fatalf("expected injection at </body> fallback")
	}

	body := rr.Body.String()
	bodyCloseIdx := strings.Index(strings.ToLower(body), "</body>")
	snippetIdx := strings.Index(body, cfg.ScriptSrc)
	if snippetIdx < 0 || bodyCloseIdx < 0 || snippetIdx > bodyCloseIdx {
		t.Fatalf("expected snippet before </body>")
	}
}

func Test_StripAcceptEncoding_DefaultTrue_StripsHeaderBeforeUpstream(t *testing.T) {
	const websiteID = "014f4608-ab91-44f8-a046-749b8593ada9"

	var sawAcceptEncoding string
	next := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		sawAcceptEncoding = r.Header.Get("Accept-Encoding")

		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte("<html><head></head><body>OK</body></html>"))
	})

	cfg := CreateConfig()
	// default is true, but keep explicit to avoid future regressions
	cfg.StripAcceptEncoding = true
	cfg.WebsiteID = websiteID
	cfg.MaxLookaheadBytes = 32 * 1024

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if sawAcceptEncoding != "" {
		t.Fatalf("expected Accept-Encoding to be stripped before upstream, got %q", sawAcceptEncoding)
	}

	body := rr.Body.String()
	if !strings.Contains(body, cfg.ScriptSrc) {
		t.Fatalf("expected injection to occur, got body=%q", body)
	}
}

func Test_StripAcceptEncoding_False_DoesNotStripHeaderBeforeUpstream(t *testing.T) {
	const websiteID = "014f4608-ab91-44f8-a046-749b8593ada9"

	var sawAcceptEncoding string
	next := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		sawAcceptEncoding = r.Header.Get("Accept-Encoding")

		// Return uncompressed anyway; we only validate header forwarding here.
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte("<html><head></head><body>OK</body></html>"))
	})

	cfg := CreateConfig()
	cfg.StripAcceptEncoding = false
	cfg.WebsiteID = websiteID
	cfg.MaxLookaheadBytes = 32 * 1024

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if sawAcceptEncoding != "gzip, br" {
		t.Fatalf("expected Accept-Encoding to be preserved before upstream, got %q", sawAcceptEncoding)
	}

	// Injection should still occur because upstream returned uncompressed HTML.
	body := rr.Body.String()
	if !strings.Contains(body, cfg.ScriptSrc) {
		t.Fatalf("expected injection to occur, got body=%q", body)
	}
}

func Test_StripAcceptEncoding_True_AllowsInjection_WhenUpstreamCompressesConditionally(t *testing.T) {
	const websiteID = "014f4608-ab91-44f8-a046-749b8593ada9"

	// This upstream simulates typical behavior: compress only when Accept-Encoding includes gzip.
	next := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		ae := r.Header.Get("Accept-Encoding")
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")

		if strings.Contains(ae, "gzip") {
			// We are not actually gzipping here; we just set the header to trigger plugin passthrough path.
			rw.Header().Set("Content-Encoding", "gzip")
		}

		_, _ = rw.Write([]byte("<html><head></head><body>OK</body></html>"))
	})

	cfg := CreateConfig()
	cfg.StripAcceptEncoding = true
	cfg.WebsiteID = websiteID
	cfg.MaxLookaheadBytes = 32 * 1024

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	// Because we stripped Accept-Encoding, upstream should NOT set Content-Encoding.
	if rr.Header().Get("Content-Encoding") != "" {
		t.Fatalf("expected upstream to not send Content-Encoding when Accept-Encoding stripped, got %q", rr.Header().Get("Content-Encoding"))
	}

	body := rr.Body.String()
	if !strings.Contains(body, cfg.ScriptSrc) {
		t.Fatalf("expected injection to occur, got body=%q", body)
	}
}

func Test_StripAcceptEncoding_False_SkipsInjection_WhenUpstreamRespondsCompressed(t *testing.T) {
	const websiteID = "014f4608-ab91-44f8-a046-749b8593ada9"

	// Simulate upstream that sets Content-Encoding when client advertises gzip.
	next := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			rw.Header().Set("Content-Encoding", "gzip")
		}

		// Body content doesn't matter; plugin should passthrough because Content-Encoding is set.
		_, _ = rw.Write([]byte("<html><head></head><body>OK</body></html>"))
	})

	cfg := CreateConfig()
	cfg.StripAcceptEncoding = false
	cfg.WebsiteID = websiteID
	cfg.MaxLookaheadBytes = 32 * 1024

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if rr.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected Content-Encoding=gzip passthrough, got %q", rr.Header().Get("Content-Encoding"))
	}

	body := rr.Body.String()
	if strings.Contains(body, cfg.ScriptSrc) {
		t.Fatalf("expected no injection when upstream response is compressed, got body=%q", body)
	}
}

func Test_StripAcceptEncoding_DoesNotMutateOriginalRequestObject(t *testing.T) {
	const websiteID = "014f4608-ab91-44f8-a046-749b8593ada9"

	next := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte("<html><head></head><body>OK</body></html>"))
	})

	cfg := CreateConfig()
	cfg.StripAcceptEncoding = true
	cfg.WebsiteID = websiteID

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	// Ensure we did not mutate the original request's headers.
	if got := req.Header.Get("Accept-Encoding"); got != "gzip, br" {
		t.Fatalf("expected original request Accept-Encoding unchanged, got %q", got)
	}
}

func Test_StripAcceptEncoding_True_WorksOnLargeResponse(t *testing.T) {
	const websiteID = "014f4608-ab91-44f8-a046-749b8593ada9"

	var sawAcceptEncoding string
	next := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		sawAcceptEncoding = r.Header.Get("Accept-Encoding")

		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte("<html><head></head><body>"))
		_, _ = rw.Write(bytes.Repeat([]byte("A"), 2*1024*1024))
		_, _ = rw.Write([]byte("</body></html>"))
	})

	cfg := CreateConfig()
	cfg.StripAcceptEncoding = true
	cfg.WebsiteID = websiteID
	cfg.MaxLookaheadBytes = 32 * 1024

	mw := newTestMiddleware(t, next, cfg)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if sawAcceptEncoding != "" {
		t.Fatalf("expected Accept-Encoding stripped before upstream, got %q", sawAcceptEncoding)
	}

	body := rr.Body.String()
	if !strings.Contains(body, cfg.ScriptSrc) {
		t.Fatalf("expected injection to occur")
	}
	if !strings.Contains(body, "</body></html>") {
		t.Fatalf("expected response not truncated")
	}
}
