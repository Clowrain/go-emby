package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var xiaoyaClient = &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

const xiaoyaMaxRequests = 5

// Resolve the original STRM, never the viewer-facing authenticated playback URL.
func xiaoyaSource(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return (h == "xiaoya.host" || h == "172.17.0.1" || h == "127.0.0.1" || h == "localhost") && u.Port() == "5678"
}

func xiaoyaLink(r *http.Request, raw, endpoint string) (string, error) {
	source, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid STRM")
	}
	target, err := url.Parse(endpoint)
	if err != nil || !resolverHTTPURL(target) || target.RawQuery != "" || target.Fragment != "" {
		return "", fmt.Errorf("invalid resolver address")
	}
	// Only administrator-configured origins may be contacted. A redirect must
	// never grant access to another internal service, even on the same host.
	origins := map[string]bool{resolverOrigin(target): true}
	hosts := map[string]bool{strings.ToLower(target.Hostname()): true}
	if xiaoyaSource(raw) {
		for _, value := range strings.Split(os.Getenv("XIAOYA_REDIRECT_ORIGINS"), ",") {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			u, err := url.Parse(value)
			if err != nil || !resolverHTTPURL(u) || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
				return "", fmt.Errorf("invalid XIAOYA_REDIRECT_ORIGINS origin")
			}
			origins[resolverOrigin(u)] = true
			hosts[strings.ToLower(u.Hostname())] = true
		}
	}
	target.Path = strings.TrimRight(target.Path, "/") + source.Path
	target.RawPath = ""
	query := source.Query()
	if query.Get("sign") == "SIGN_STR" {
		query.Del("sign")
	}
	target.RawQuery = query.Encode()
	// Bound the entire chain, rather than granting each hop a fresh timeout.
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	seen := make(map[string]bool)
	for hop := 0; hop < xiaoyaMaxRequests; hop++ {
		if seen[target.String()] {
			return "", fmt.Errorf("resolver redirect loop")
		}
		seen[target.String()] = true
		link, err := requestResolverLink(ctx, r.UserAgent(), target)
		if err != nil {
			return "", err
		}
		if origins[resolverOrigin(link)] {
			target = link
			continue
		}
		h := strings.ToLower(link.Hostname())
		ip := net.ParseIP(h)
		if hosts[h] || h == "localhost" || h == "xiaoya.host" || (ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast())) {
			return "", fmt.Errorf("resolver returned an untrusted internal address")
		}
		// Stop before requesting the CDN: only the player transfers media bytes.
		return link.String(), nil
	}
	return "", fmt.Errorf("resolver redirect limit exceeded")
}

func resolverHTTPURL(u *url.URL) bool {
	return u.Hostname() != "" && u.User == nil && (u.Scheme == "http" || u.Scheme == "https") && !strings.ContainsAny(u.String(), "\r\n")
}

func resolverOrigin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func requestResolverLink(ctx context.Context, userAgent string, target *url.URL) (*url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("invalid resolver request")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Alist-OriUA", userAgent)
	res, err := xiaoyaClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("resolver request failed")
	}
	defer res.Body.Close()
	link := ""
	switch res.StatusCode {
	case 301, 302, 303, 307, 308:
		link = res.Header.Get("Location")
	case 200:
		b, err := io.ReadAll(io.LimitReader(res.Body, 65537))
		if err != nil || len(b) > 65536 {
			return nil, fmt.Errorf("invalid resolver response")
		}
		link = strings.TrimSpace(string(b))
		if strings.HasPrefix(link, "{") {
			var v struct {
				URL string `json:"url"`
			}
			if json.Unmarshal(b, &v) != nil {
				return nil, fmt.Errorf("invalid resolver JSON")
			}
			link = v.URL
		}
	default:
		return nil, fmt.Errorf("resolver HTTP %d", res.StatusCode)
	}
	u, err := url.Parse(link)
	if err != nil || strings.TrimSpace(link) == "" || strings.ContainsAny(link, "\r\n") {
		return nil, fmt.Errorf("resolver did not return an HTTP link")
	}
	if res.StatusCode != http.StatusOK {
		u = target.ResolveReference(u)
	}
	if !resolverHTTPURL(u) {
		return nil, fmt.Errorf("resolver did not return an HTTP link")
	}
	// Fragments are not sent over HTTP and must not evade loop detection.
	u.Fragment = ""
	return u, nil
}

func (a *App) resolveXiaoya(w http.ResponseWriter, r *http.Request, x Item) {
	link, err := xiaoyaLink(r, x.URL, os.Getenv("XIAOYA_URL"))
	if err != nil {
		fail(w, http.StatusBadGateway, "Xiaoya 直链解析失败: "+err.Error())
		return
	}
	w.Header().Set("Location", link)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusFound)
}
