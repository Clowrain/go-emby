package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestXiaoyaResolver(t *testing.T) {
	t.Setenv("XIAOYA_REDIRECT_ORIGINS", "")
	for _, tc := range []struct {
		name, body, location string
		status               int
		ok                   bool
	}{
		{"redirect", "", "https://cdn.example/video?sig=abc", 302, true},
		{"text", "https://cdn.example/video", "", 200, true},
		{"json", `{"url":"https://cdn.example/video"}`, "", 200, true},
		{"loopback", "", "http://127.0.0.1:8097/stream", 302, false},
		{"private", "", "http://172.18.0.1/stream", 302, false},
		{"invalid-scheme", "", "ftp://cdn.example/stream", 302, false},
		{"error", `{"code":500,"message":"error"}`, "", 200, false},
		{"html", "<html>failure</html>", "", 200, false},
		{"oversize", strings.Repeat("a", 65537), "", 200, false},
		{"upstream", "", "", 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/d/中文 a.mp4" || r.URL.Query().Get("sign") != "" || r.URL.Query().Get("other") != "kept" {
					t.Errorf("unexpected resolver request: %s %s", r.Method, r.URL)
				}
				if r.Header.Get("X-Emby-Token") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Alist-OriUA") != "test-player" {
					t.Error("incorrect headers")
				}
				if tc.location != "" {
					w.Header().Set("Location", tc.location)
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			r := httptest.NewRequest("HEAD", "http://emby/Videos/abcdef/stream", nil)
			r.Header.Set("User-Agent", "test-player")
			r.Header.Set("X-Emby-Token", "secret")
			_, err := xiaoyaLink(r, "http://xiaoya.host:5678/d/中文%20a.mp4?sign=SIGN_STR&other=kept", srv.URL)
			if (err == nil) != tc.ok {
				t.Fatalf("ok=%v error=%v", tc.ok, err)
			}
		})
	}
}

// Explicitly opt in on a machine that can reach the unmodified Xiaoya chain.
// Never log signed CDN URLs or request the media body.
func TestXiaoyaLiveResolver(t *testing.T) {
	source, endpoint := os.Getenv("XIAOYA_TEST_SOURCE"), os.Getenv("XIAOYA_URL")
	if source == "" || endpoint == "" {
		t.Skip("set XIAOYA_TEST_SOURCE and XIAOYA_URL to test a real resolver")
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("User-Agent", "VidHub/2.3.4")
	link, err := xiaoyaLink(r, source, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil || !resolverHTTPURL(u) {
		t.Fatal("resolver did not return an HTTP CDN URL")
	}
	t.Logf("resolved CDN origin: %s://%s", u.Scheme, u.Host)
}

func TestXiaoyaTrustedRedirectChain(t *testing.T) {
	var firstCalls, secondCalls atomic.Int32
	checkHeaders := func(r *http.Request) {
		t.Helper()
		if r.Method != "GET" || r.UserAgent() != "VidHub/2.3.4" || r.Header.Get("X-Alist-OriUA") != r.UserAgent() {
			t.Errorf("incorrect resolver request: %s %v", r.Method, r.Header)
		}
		for _, header := range []string{"Authorization", "X-Emby-Token", "Cookie", "Referer"} {
			if r.Header.Get(header) != "" {
				t.Errorf("viewer header forwarded: %s", header)
			}
		}
	}
	cdn := "https://cdn.example/movie.mp4?sig=a%2Bb&expires=123"
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCalls.Add(1)
		checkHeaders(r)
		if r.URL.Path != "/d/中文 a.mp4" || r.URL.RawQuery != "sig=keep%2Bme" {
			t.Errorf("redirect path/query changed: %s", r.URL)
		}
		w.Header().Set("Location", cdn)
		w.WriteHeader(http.StatusFound)
	}))
	defer direct.Close()
	entry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalls.Add(1)
		checkHeaders(r)
		if r.URL.Query().Get("sign") != "" || r.URL.Query().Get("other") != "kept" {
			t.Errorf("initial STRM query changed: %s", r.URL)
		}
		w.Header().Set("Location", direct.URL+"/d/%E4%B8%AD%E6%96%87%20a.mp4?sig=keep%2Bme")
		w.WriteHeader(http.StatusFound)
	}))
	defer entry.Close()
	t.Setenv("XIAOYA_REDIRECT_ORIGINS", "  "+direct.URL+"/ , ")
	r := httptest.NewRequest("HEAD", "/Videos/movie/stream?api_key=viewer-secret", nil)
	r.Header.Set("User-Agent", "VidHub/2.3.4")
	r.Header.Set("Authorization", "Bearer viewer-secret")
	r.Header.Set("X-Emby-Token", "viewer-secret")
	r.Header.Set("Cookie", "session=viewer-secret")
	r.Header.Set("Referer", "http://emby/?api_key=viewer-secret")
	w := httptest.NewRecorder()
	(&App{}).resolveSTRM(w, r, Item{URL: "http://xiaoya.host:5678/d/中文%20a.mp4?sign=SIGN_STR&other=kept"}, entry.URL)
	if w.Code != 302 || w.Header().Get("Location") != cdn || w.Header().Get("Cache-Control") != "no-store" || w.Body.Len() != 0 {
		t.Fatalf("incorrect final response: %d %v %s", w.Code, w.Header(), w.Body)
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatalf("requests: entry=%d direct=%d", firstCalls.Load(), secondCalls.Load())
	}
}

func TestXiaoyaRelativeRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/d/movie" {
					w.Header().Set("Location", "../resolved?sig=a%2Bb")
					w.WriteHeader(status)
					return
				}
				if r.URL.Path != "/resolved" || r.URL.RawQuery != "sig=a%2Bb" {
					t.Errorf("unexpected relative target: %s", r.URL)
				}
				w.Header().Set("Location", "https://cdn.example/movie")
				w.WriteHeader(302)
			}))
			defer srv.Close()
			link, err := xiaoyaLink(httptest.NewRequest("GET", "/", nil), "http://xiaoya.host:5678/d/movie", srv.URL)
			if err != nil || link != "https://cdn.example/movie" {
				t.Fatalf("link=%q err=%v", link, err)
			}
		})
	}
}

func TestXiaoyaRejectsUntrustedHopBeforeRequest(t *testing.T) {
	var calls atomic.Int32
	untrusted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	defer untrusted.Close()
	trusted := httptest.NewServer(http.NotFoundHandler())
	defer trusted.Close()
	t.Setenv("XIAOYA_REDIRECT_ORIGINS", trusted.URL)
	entry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", untrusted.URL+"/admin?secret=must-not-leak")
		w.WriteHeader(302)
	}))
	defer entry.Close()
	w := httptest.NewRecorder()
	(&App{}).resolveSTRM(w, httptest.NewRequest("GET", "/", nil), Item{URL: "http://xiaoya.host:5678/d/movie"}, entry.URL)
	if w.Code != 502 || w.Header().Get("Location") != "" || calls.Load() != 0 || strings.Contains(w.Body.String(), "must-not-leak") {
		t.Fatalf("unsafe fallback: %d %s, requests=%d", w.Code, w.Body, calls.Load())
	}
}

func TestXiaoyaRejectsInvalidOriginsWithoutRequest(t *testing.T) {
	for _, origin := range []string{"http://", "ftp://resolver", "http://user@resolver", "http://resolver/path", "http://resolver?token=secret", "http://resolver#fragment", "http://resolver:invalid"} {
		t.Run(origin, func(t *testing.T) {
			t.Setenv("XIAOYA_REDIRECT_ORIGINS", origin)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("invalid configuration made an upstream request")
			}))
			defer srv.Close()
			_, err := xiaoyaLink(httptest.NewRequest("GET", "/", nil), "http://xiaoya.host:5678/d/movie", srv.URL)
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid origin error: %v", err)
			}
		})
	}
}

func TestXiaoyaRedirectLoopAndLimit(t *testing.T) {
	for _, loop := range []bool{true, false} {
		t.Run(fmt.Sprint(loop), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				location := "/d/movie#ignored"
				if !loop {
					location = fmt.Sprintf("/hop/%d", n)
				}
				w.Header().Set("Location", location)
				w.WriteHeader(302)
			}))
			defer srv.Close()
			_, err := xiaoyaLink(httptest.NewRequest("GET", "/", nil), "http://xiaoya.host:5678/d/movie", srv.URL)
			want := int32(xiaoyaMaxRequests)
			if loop {
				want = 1
			}
			if err == nil || calls.Load() != want {
				t.Fatalf("err=%v requests=%d want=%d", err, calls.Load(), want)
			}
		})
	}
}

func TestXiaoyaCancellationAcrossHops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/d/movie" {
			w.Header().Set("Location", "/wait")
			w.WriteHeader(302)
			return
		}
		cancel()
		<-r.Context().Done()
	}))
	defer srv.Close()
	start := time.Now()
	_, err := xiaoyaLink(httptest.NewRequest("GET", "/", nil).WithContext(ctx), "http://xiaoya.host:5678/d/movie", srv.URL)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("cancellation not respected: %v", err)
	}
}

func TestXiaoyaOriginsDoNotAuthorizeNanShare(t *testing.T) {
	var calls atomic.Int32
	next := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer next.Close()
	t.Setenv("XIAOYA_REDIRECT_ORIGINS", next.URL)
	entry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", next.URL+"/internal")
		w.WriteHeader(302)
	}))
	defer entry.Close()
	_, err := xiaoyaLink(httptest.NewRequest("GET", "/", nil), "http://172.17.0.1:8115/api/?pickcode=test", entry.URL)
	if err == nil || calls.Load() != 0 {
		t.Fatalf("Xiaoya allowlist affected NanShare: err=%v calls=%d", err, calls.Load())
	}
}
