// common/fetch.go
package common

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ---- Public API

// FetchOpts holds knobs for robust fetching.
type FetchOpts struct {
	Timeout            time.Duration     // total request timeout (incl. redirects)
	ConnectTimeout     time.Duration     // TCP connect timeout
	SSLMode            string            // your SafeTransport knob
	MaxSize            int64             // hard cap for body (e.g., 8<<20)
	AllowedMIMEs       []string          // allowlist of MIME types (prefix match OK, e.g. "text/", "application/json")
	Headers            map[string]string // extra headers
	SSRFGuard          string            // "", "on", or "strict"
	UserAgent          string            // default if empty: "theCROWler/1.0"
	Retries            int               // retry count for transient network/5xx/429
	RetryBaseDelay     time.Duration     // base backoff, e.g. 200ms
	FollowRedirects    bool              // default true
	MaxRedirects       int               // default 5
	DropAuthOnRedirect bool              // default true
}

// FetchRemoteBytes fetches raw bytes from HTTP(S) and optionally s3:// (when built with aws_s3 tag).
func FetchRemoteBytes(ctx context.Context, rawURL string, opts FetchOpts) ([]byte, string, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = 10 * time.Second
	}
	if opts.MaxSize <= 0 {
		opts.MaxSize = 16 << 20 // 16 MiB sane default
	}
	if opts.RetryBaseDelay <= 0 {
		opts.RetryBaseDelay = 200 * time.Millisecond
	}
	if opts.MaxRedirects <= 0 {
		opts.MaxRedirects = 5
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "theCROWler/1.0"
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "s3://") {
		return nil, "", fmt.Errorf("unsupported scheme in URL: %s", rawURL)
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("invalid URL: %w", err)
	}

	switch u.Scheme {
	case "http", "https":
		return fetchHTTP(ctx, rawURL, opts)
	case "s3":
		// Optional: only available if built with -tags aws_s3
		return fetchS3(ctx, u, opts)
	default:
		return nil, "", fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
}

// FetchRemoteText is a convenience wrapper that enforces text-like MIME and returns string.
func FetchRemoteText(ctx context.Context, rawURL string, opts FetchOpts) (string, error) {
	// sensible default allowlist for “text” fetches
	if len(opts.AllowedMIMEs) == 0 {
		opts.AllowedMIMEs = []string{"text/", "application/json", "application/javascript", "application/octet-stream"}
	}
	b, ctype, err := FetchRemoteBytes(ctx, rawURL, opts)
	if err != nil {
		return "", err
	}
	// Quick charset sniff (very light): honor Content-Type charset if present; otherwise assume UTF-8.
	cs := "utf-8"
	if ctype != "" {
		_, params, _ := mime.ParseMediaType(ctype)
		if v := strings.TrimSpace(strings.ToLower(params["charset"])); v != "" {
			cs = v
		}
	}
	// We only support UTF-8 transparently here. If you need ISO-8859-1/Shift-JIS, plug an iconv step.
	if cs != "utf-8" && cs != "utf8" {
		// return raw bytes note if not UTF-8 (avoid mojibake)
		return string(b), nil
	}
	return string(b), nil
}

// ---- HTTP(S) implementation

func fetchHTTP(ctx context.Context, rawURL string, opts FetchOpts) ([]byte, string, error) {
	// Transport reuses your SafeTransport while enforcing connect+TLS timeouts.
	tr := SafeTransport(int(opts.ConnectTimeout.Seconds()), opts.SSLMode)

	// Ensure dialer timeouts honored even if SafeTransport replaces them.
	// Only set if not already set by SafeTransport
	if tr.DialContext == nil {
		tr.DialContext = (&net.Dialer{Timeout: opts.ConnectTimeout}).DialContext
	}
	if tr.TLSHandshakeTimeout == 0 {
		tr.TLSHandshakeTimeout = 10 * time.Second
	}
	// Keep compression on (default). It saves bandwidth; Go auto-decompresses gzip.
	tr.DisableCompression = false
	// Harden TLS if you want (example; depends on your SSL mode policy):
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	// Redirect policy
	follow := opts.FollowRedirects
	if !follow {
		// if not following redirects, any 3xx is an error
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   opts.Timeout,
	}
	if follow || opts.DropAuthOnRedirect {
		client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
			if !follow {
				return http.ErrUseLastResponse
			}
			if len(via) >= opts.MaxRedirects {
				return errors.New("stopped after too many redirects")
			}
			if opts.DropAuthOnRedirect && len(via) > 0 {
				orig := via[0].URL
				if !strings.EqualFold(r.URL.Hostname(), orig.Hostname()) {
					// drop sensitive headers on cross-host
					r.Header.Del("Authorization")
					r.Header.Del("Cookie")
				}
			}
			return nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	// Headers
	req.Header.Set("User-Agent", opts.UserAgent)
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	// Optional SSRF guard (like your GenericAPIRequest)
	if g := strings.ToLower(strings.TrimSpace(opts.SSRFGuard)); g == "on" || g == "strict" {
		u, _ := url.Parse(rawURL)
		host := u.Hostname()
		ips, lookupErr := net.LookupIP(host)
		if lookupErr != nil || len(ips) == 0 {
			return nil, "", fmt.Errorf("DNS lookup failed for %s: %v", host, lookupErr)
		}
		for _, ip := range ips {
			if isPrivateOrMeta(ip, g == "strict") {
				return nil, "", fmt.Errorf("destination IP blocked by ssrf_guard: %s (%s)", ip, host)
			}
		}
	}

	var lastErr error
	retries := opts.Retries
	if retries < 0 {
		retries = 0
	}
	delay := opts.RetryBaseDelay

	for attempt := 0; attempt <= retries; attempt++ {
		resp, err := client.Do(req)
		if err != nil {
			// Retry on transient net errors
			if attempt < retries && isTransientNetErr(err) {
				time.Sleep(jitter(delay))
				delay = backoff(delay)
				lastErr = err
				continue
			}
			return nil, "", fmt.Errorf("request failed: %w", err)
		}

		ctype := strings.TrimSpace(resp.Header.Get("Content-Type"))

		// Non-200s: optionally retry on 429/5xx
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			_ = resp.Body.Close()
			if attempt < retries && shouldRetryStatus(resp.StatusCode) {
				time.Sleep(jitter(delay))
				delay = backoff(delay)
				lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
				continue
			}
			return nil, ctype, fmt.Errorf("non-2xx status: %d", resp.StatusCode)
		}

		// Enforce content length if provided
		if resp.ContentLength > 0 && resp.ContentLength > opts.MaxSize {
			_ = resp.Body.Close()
			return nil, ctype, fmt.Errorf("response too large: %d > %d", resp.ContentLength, opts.MaxSize)
		}

		// MIME allowlist
		if len(opts.AllowedMIMEs) > 0 && ctype != "" {
			mt, _, _ := mime.ParseMediaType(ctype)
			if !mimeAllowed(mt, opts.AllowedMIMEs) {
				_ = resp.Body.Close()
				return nil, ctype, fmt.Errorf("content-type %q not allowed", mt)
			}
		}

		// Stream with limit
		limited := io.LimitReader(resp.Body, opts.MaxSize+1)
		buf := bufio.NewReader(limited)
		data, readErr := io.ReadAll(buf)
		_ = resp.Body.Close()
		if readErr != nil {
			// Retry reads that error mid-stream if allowed
			if attempt < retries && isTransientNetErr(readErr) {
				time.Sleep(jitter(delay))
				delay = backoff(delay)
				lastErr = readErr
				continue
			}
			return nil, ctype, fmt.Errorf("read body: %w", readErr)
		}
		if int64(len(data)) > opts.MaxSize {
			return nil, ctype, fmt.Errorf("response exceeded limit (%d bytes)", opts.MaxSize)
		}
		return data, ctype, nil
	}

	return nil, "", fmt.Errorf("request failed after retries: %v", lastErr)
}

// ---- Helpers

func mimeAllowed(mt string, allow []string) bool {
	mt = strings.ToLower(strings.TrimSpace(mt))
	for _, a := range allow {
		a = strings.ToLower(strings.TrimSpace(a))
		if strings.HasSuffix(a, "/") {
			if strings.HasPrefix(mt, a) {
				return true
			}
		} else {
			if mt == a {
				return true
			}
		}
	}
	return false
}

func shouldRetryStatus(code int) bool {
	if code == 429 {
		return true
	}
	return code >= 500 && code <= 599
}

func isTransientNetErr(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	// ECONNRESET, EOF etc.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "tls handshake timeout") ||
		strings.Contains(msg, "eof")
}

func backoff(d time.Duration) time.Duration {
	nd := d * 2
	if nd > 4*time.Second {
		nd = 4 * time.Second
	}
	return nd
}

func jitter(d time.Duration) time.Duration {
	// +/- 20%
	n := int64(d)
	return time.Duration(n + (n/5)*(int64(time.Now().UnixNano()%4000)-2000)/2000)
}

func fetchS3(ctx context.Context, u *url.URL, opts FetchOpts) ([]byte, string, error) {
	bkt := u.Host
	key := strings.TrimPrefix(u.Path, "/")
	if bkt == "" || key == "" {
		return nil, "", fmt.Errorf("invalid s3 URL: %s", u.String())
	}

	cfg, err := awscfg.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("aws config: %w", err)
	}
	client := s3.NewFromConfig(cfg)

	// If you prefer presigning instead of GetObject (e.g., through a proxy):
	// ps := s3.NewPresignClient(client)
	// pre, _ := ps.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &bkt, Key: &key}, s3.WithPresignExpires(15*time.Minute))
	// return fetchHTTP(ctx, pre.URL, opts)

	// Direct GetObject:
	goctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	out, err := client.GetObject(goctx, &s3.GetObjectInput{
		Bucket: &bkt,
		Key:    &key,
	})
	if err != nil {
		return nil, "", fmt.Errorf("s3 get: %w", err)
	}
	defer out.Body.Close()

	// enforce size cap while copying
	lim := io.LimitReader(out.Body, opts.MaxSize+1)
	data, err := io.ReadAll(lim)
	if err != nil {
		return nil, "", fmt.Errorf("s3 read: %w", err)
	}
	if int64(len(data)) > opts.MaxSize {
		return nil, "", fmt.Errorf("s3 object exceeded limit (%d bytes)", opts.MaxSize)
	}

	ctype := ""
	if out.ContentType != nil {
		ctype = *out.ContentType
	}
	// MIME allowlist (same as HTTP path)
	if len(opts.AllowedMIMEs) > 0 && ctype != "" {
		if !mimeAllowed(ctype, opts.AllowedMIMEs) {
			return nil, ctype, fmt.Errorf("content-type %q not allowed", ctype)
		}
	}
	return data, ctype, nil
}
