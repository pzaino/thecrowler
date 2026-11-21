// Command fixture-server provides deterministic HTTP fixtures for the isolated
// InformationSeed compose acceptance harness. It never contacts external hosts.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type requestTrace struct {
	Path             string    `json:"path"`
	Method           string    `json:"method"`
	Query            string    `json:"query"`
	RemoteAddr       string    `json:"remote_addr"`
	StartedAt        time.Time `json:"started_at"`
	DurationMS       int64     `json:"duration_ms"`
	Status           int       `json:"status"`
	Canceled         bool      `json:"canceled"`
	HasAuthorization bool      `json:"has_authorization"`
}

var (
	traceMu sync.Mutex
	traces  []requestTrace
	counts  = map[string]int{}
)

func main() {
	port := strings.TrimSpace(os.Getenv("FIXTURE_PORT"))
	if port == "" {
		port = "8080"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/requests", func(w http.ResponseWriter, r *http.Request) {
		traceMu.Lock()
		defer traceMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"counts": counts, "traces": traces})
	})
	mux.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		traceMu.Lock()
		traces = nil
		counts = map[string]int{}
		traceMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/rss/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rssFeed("Fixture RSS", "https://fixture.test/rss", 5, "rss")))
	})
	mux.HandleFunc("/rss/rate", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rssFeed("Fixture Rate RSS", "https://fixture.test/rate", 3, "rate")))
	})
	mux.HandleFunc("/rss/rate60", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rssFeed("Fixture Rate60 RSS", "https://fixture.test/rate60", 3, "rate60")))
	})
	mux.HandleFunc("/rss/cancel", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rssFeed("Fixture Cancel RSS", "https://fixture.test/cancel", 3, "cancel")))
	})
	mux.HandleFunc("/rss/long", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rssFeedLong("Fixture Long RSS", "https://fixture.test/long")))
	})
	mux.HandleFunc("/atom/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = w.Write([]byte(atomFeed("Fixture Atom", "https://fixture.test/atom", 5)))
	})
	mux.HandleFunc("/rss/empty", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>empty</title></channel></rss>`))
	})
	mux.HandleFunc("/rss/invalid", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<rss><channel><item>`))
	})
	mux.HandleFunc("/search/json", func(w http.ResponseWriter, r *http.Request) {
		writeJSONResults(w, r, []map[string]any{
			result("https://fixture.test/new/one", "New one", "First snippet", 0.91, 1),
			result("https://fixture.test/new/two", "New two", "Second snippet", 0.82, 2),
		})
	})
	mux.HandleFunc("/search/link", func(w http.ResponseWriter, r *http.Request) {
		writeJSONResults(w, r, []map[string]any{
			result("https://fixture.test/existing", "Existing source", "Existing snippet", 0.95, 1),
			result("https://fixture.test/new/link-only", "New link-only", "Link-only snippet", 0.80, 2),
		})
	})
	mux.HandleFunc("/search/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSONResults(w, r, []map[string]any{
			result("https://fixture.test/config-existing", "Config existing", "Config snippet", 0.90, 1),
		})
	})
	mux.HandleFunc("/search/noconfig", func(w http.ResponseWriter, r *http.Request) {
		writeJSONResults(w, r, []map[string]any{
			result("https://fixture.test/noconfig/one", "No config one", "No config snippet one", 0.90, 1),
			result("https://fixture.test/noconfig/two", "No config two", "No config snippet two", 0.80, 2),
		})
	})
	mux.HandleFunc("/search/many", func(w http.ResponseWriter, r *http.Request) {
		items := make([]map[string]any, 0, 120)
		for i := 1; i <= 120; i++ {
			items = append(items, result(fmt.Sprintf("https://fixture.test/many/%03d", i), fmt.Sprintf("Many %03d", i), fmt.Sprintf("Snippet %03d", i), float64(1000-i)/1000, i))
		}
		// Deterministic jitter based on a request counter, bounded to 40ms.
		traceMu.Lock()
		counts["/search/many"]++
		iteration := counts["/search/many"]
		traceMu.Unlock()
		time.Sleep(time.Duration((iteration*7)%5) * 10 * time.Millisecond)
		writeJSONResults(w, r, items)
	})
	mux.HandleFunc("/gdelt/doc", func(w http.ResponseWriter, r *http.Request) {
		writeGDELTArticles(w, gdeltArticles(3))
	})
	mux.HandleFunc("/gdelt/paged", func(w http.ResponseWriter, r *http.Request) {
		start := 1
		if raw := strings.TrimSpace(r.URL.Query().Get("startrecord")); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
				start = parsed
			}
		}
		all := gdeltArticles(4)
		begin := start - 1
		if begin < 0 {
			begin = 0
		}
		if begin > len(all) {
			begin = len(all)
		}
		end := begin + 2
		if end > len(all) {
			end = len(all)
		}
		writeGDELTArticles(w, all[begin:end])
	})
	mux.HandleFunc("/gdelt/empty", func(w http.ResponseWriter, r *http.Request) {
		writeGDELTArticles(w, gdeltArticles(0))
	})
	mux.HandleFunc("/gdelt/missing", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/gdelt/malformed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"articles":[`))
	})
	for _, status := range []int{403, 404, 429, 500, 504} {
		status := status
		mux.HandleFunc(fmt.Sprintf("/status/%d", status), func(w http.ResponseWriter, r *http.Request) {
			if status == 429 {
				w.Header().Set("Retry-After", "1")
			}
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"error":"fixture status %d"}`, status)
		})
	}
	mux.HandleFunc("/slow/", func(w http.ResponseWriter, r *http.Request) {
		seconds, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/slow/"))
		if err != nil || seconds < 1 {
			seconds = 5
		}
		writeJSONResults(w, r, []map[string]any{result(fmt.Sprintf("https://fixture.test/slow/%d", seconds), fmt.Sprintf("Slow %d", seconds), "Slow snippet", 0.5, 1)})
	})
	handler := withTrace(mux)
	srv := &http.Server{Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second}
	log.Printf("fixture server listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func withTrace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/requests" || r.URL.Path == "/reset" {
			next.ServeHTTP(w, r)
			return
		}
		traceMu.Lock()
		counts[r.URL.Path]++
		traceMu.Unlock()
		started := time.Now().UTC()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		canceled := false
		if strings.HasPrefix(r.URL.Path, "/slow/") {
			seconds, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/slow/"))
			if seconds < 1 {
				seconds = 5
			}
			select {
			case <-time.After(time.Duration(seconds) * time.Second):
			case <-r.Context().Done():
				canceled = true
			}
			if canceled {
				traceMu.Lock()
				traces = append(traces, requestTrace{
					Path: r.URL.Path, Method: r.Method, Query: r.URL.RawQuery, RemoteAddr: r.RemoteAddr,
					StartedAt: started, DurationMS: time.Since(started).Milliseconds(), Status: 499,
					Canceled: true, HasAuthorization: r.Header.Get("Authorization") != "",
				})
				traceMu.Unlock()
				return
			}
		}
		next.ServeHTTP(recorder, r)
		traceMu.Lock()
		traces = append(traces, requestTrace{
			Path: r.URL.Path, Method: r.Method, Query: r.URL.RawQuery, RemoteAddr: r.RemoteAddr,
			StartedAt: started, DurationMS: time.Since(started).Milliseconds(), Status: recorder.status,
			Canceled: canceled, HasAuthorization: r.Header.Get("Authorization") != "",
		})
		traceMu.Unlock()
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func writeJSONResults(w http.ResponseWriter, r *http.Request, results []map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results, "query": r.URL.Query().Get("q")})
}

func result(url, title, snippet string, score float64, rank int) map[string]any {
	return map[string]any{"url": url, "title": title, "snippet": snippet, "score": score, "rank": rank, "metadata": map[string]any{"fixture": true}}
}

func gdeltArticles(count int) []map[string]any {
	articles := make([]map[string]any, 0, count)
	for i := 1; i <= count; i++ {
		articles = append(articles, map[string]any{
			"url": fmt.Sprintf("https://fixture.test/gdelt/%d", i), "title": fmt.Sprintf("GDELT article %d", i),
			"seendate": fmt.Sprintf("20261112T12000%dZ", i), "domain": "fixture.test", "language": "English", "sourcecountry": "Testland",
		})
	}
	return articles
}

func writeGDELTArticles(w http.ResponseWriter, articles []map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"articles": articles})
}

func rssFeed(title, base string, count int, prefix string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>` + title + `</title><link>` + base + `</link><description>fixture</description>`)
	for i := 1; i <= count; i++ {
		b.WriteString(fmt.Sprintf(`<item><title>%s item %d</title><link>https://fixture.test/%s/%d</link><guid>%s-guid-%d</guid><description>%s snippet %d with Unicode café</description><pubDate>Wed, 12 Nov 2026 12:00:0%d GMT</pubDate></item>`, title, i, prefix, i, prefix, i, title, i, i))
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

func rssFeedLong(title, base string) string {
	long := strings.Repeat("x", 3000)
	return `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>` + title + `</title><link>` + base + `</link><description>fixture</description>` +
		`<item><title>Fixture long item</title><link>https://fixture.test/long/1</link><guid>long-guid-1</guid><description>` + long + `</description><pubDate>Wed, 12 Nov 2026 12:00:01 GMT</pubDate></item>` +
		`</channel></rss>`
}

func atomFeed(title, base string, count int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><feed xmlns="http://www.w3.org/2005/Atom"><title>` + title + `</title><id>` + base + `</id><updated>2026-11-12T12:00:00Z</updated>`)
	for i := 1; i <= count; i++ {
		b.WriteString(fmt.Sprintf(`<entry><title>%s entry %d</title><id>atom-guid-%d</id><link rel="alternate" href="https://fixture.test/atom/%d"/><summary>%s summary %d Unicode café</summary><updated>2026-11-12T12:00:0%dZ</updated></entry>`, title, i, i, i, title, i, i))
	}
	b.WriteString(`</feed>`)
	return b.String()
}
