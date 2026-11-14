package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type chatMessage struct {
	Role      string           `json:"role"`
	Content   any              `json:"content,omitempty"`
	ToolCalls []map[string]any `json:"tool_calls,omitempty"`
}

type chatRequest struct {
	Messages []chatMessage `json:"messages"`
}

var (
	requests atomic.Int64
	canceled atomic.Int64
)

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func main() {
	port := strings.TrimSpace(os.Getenv("CROWLER_E2E_MODEL_PORT"))
	if port == "" {
		port = "18095"
	}
	alias := strings.TrimSpace(os.Getenv("CROWLER_E2E_MCP_ALIAS"))
	if alias == "" {
		alias = "mcp_approved-server_mutate_counter"
	}
	sourceID := strings.TrimSpace(os.Getenv("CROWLER_E2E_SOURCE_ID"))
	if sourceID == "" {
		sourceID = "42"
	}
	mode := strings.TrimSpace(os.Getenv("CROWLER_E2E_MODEL_MODE"))
	delay := time.Duration(envInt("CROWLER_E2E_MODEL_DELAY_MS", 0)) * time.Millisecond
	handler := http.NewServeMux()
	handler.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"OK"}`))
	})
	handler.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{
			"requests": requests.Load(),
			"canceled": canceled.Load(),
		})
	})
	handler.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var request chatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		requests.Add(1)
		sawTool := false
		for _, message := range request.Messages {
			if message.Role == "tool" {
				sawTool = true
				break
			}
		}
		// Delay only the first model round. This keeps the cancellation window
		// deterministic while letting an uncancelled run finish its tool result
		// round promptly.
		if delay > 0 && !sawTool {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-r.Context().Done():
				canceled.Add(1)
				return
			}
		}
		message := map[string]any{"role": "assistant", "content": "proposal recorded"}
		finish := "stop"
		if mode != "plain" && !sawTool {
			message = map[string]any{
				"role": "assistant", "content": "",
				"tool_calls": []any{map[string]any{
					"id": "e2e-call-1", "type": "function",
					"function": map[string]any{
						"name": alias, "arguments": fmt.Sprintf(`{"source_id":%s}`, sourceID),
					},
				}},
			}
			finish = "tool_calls"
		}
		response := map[string]any{
			"id": "chatcmpl-e2e", "object": "chat.completion", "model": "scripted",
			"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}},
			"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})
	log.Printf("fake model listening on 0.0.0.0:%s (mode=%q delay=%s)", port, mode, delay)
	if err := http.ListenAndServe("0.0.0.0:"+port, handler); err != nil {
		log.Fatal(err)
	}
}
