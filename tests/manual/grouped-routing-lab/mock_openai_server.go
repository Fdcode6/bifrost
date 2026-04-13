package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type action struct {
	Type         string `json:"type"`
	Status       int    `json:"status,omitempty"`
	DelayMs      int    `json:"delay_ms,omitempty"`
	Message      string `json:"message,omitempty"`
	ResponseText string `json:"response_text,omitempty"`
}

type profile struct {
	Default action   `json:"default"`
	Series  []action `json:"series,omitempty"`
	Loop    bool     `json:"loop,omitempty"`

	index int
}

type event struct {
	Timestamp   string `json:"timestamp"`
	Path        string `json:"path"`
	Key         string `json:"key"`
	Model       string `json:"model,omitempty"`
	User        string `json:"user,omitempty"`
	Stream      bool   `json:"stream"`
	ActionType  string `json:"action_type"`
	ActionDelay int    `json:"action_delay_ms,omitempty"`
	StatusCode  int    `json:"status_code,omitempty"`
	Message     string `json:"message,omitempty"`
}

type chatRequest struct {
	Model    string            `json:"model"`
	Stream   bool              `json:"stream"`
	User     string            `json:"user"`
	Messages []json.RawMessage `json:"messages"`
}

type adminProfilesRequest struct {
	Profiles map[string]profile `json:"profiles"`
}

type state struct {
	mu       sync.Mutex
	profiles map[string]*profile
	events   []event
	jsonl    *os.File
}

func newState(jsonlPath string) (*state, error) {
	s := &state{
		profiles: make(map[string]*profile),
		events:   make([]event, 0, 128),
	}
	if jsonlPath == "" {
		return s, nil
	}
	if err := os.MkdirAll(filepath.Dir(jsonlPath), 0o755); err != nil {
		return nil, err
	}
	f, err := os.Create(jsonlPath)
	if err != nil {
		return nil, err
	}
	s.jsonl = f
	return s, nil
}

func (s *state) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jsonl != nil {
		return s.jsonl.Close()
	}
	return nil
}

func (s *state) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.profiles {
		p.index = 0
	}
	s.events = s.events[:0]
}

func (s *state) setProfiles(input map[string]profile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles = make(map[string]*profile, len(input))
	for key, p := range input {
		cp := p
		cp.index = 0
		s.profiles[key] = &cp
	}
}

func (s *state) nextAction(key string) action {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.profiles[key]
	if !ok {
		return action{Type: "success", DelayMs: 100, ResponseText: "default success"}
	}
	if len(p.Series) == 0 {
		return p.Default
	}
	if p.index >= len(p.Series) {
		if p.Loop {
			p.index = 0
		} else {
			return p.Series[len(p.Series)-1]
		}
	}
	current := p.Series[p.index]
	p.index++
	return current
}

func (s *state) addEvent(ev event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	if s.jsonl != nil {
		data, _ := json.Marshal(ev)
		_, _ = s.jsonl.Write(append(data, '\n'))
	}
}

func (s *state) snapshot() []event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]event, len(s.events))
	copy(out, s.events)
	return out
}

func bearerKey(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return auth
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload != nil {
		_ = json.NewEncoder(w).Encode(payload)
	}
}

func writeSSESuccess(w http.ResponseWriter, model, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]any{
				"message": "streaming unsupported",
				"type":    "server_error",
			},
		})
		return
	}
	chunks := []map[string]any{
		{
			"id":      "chatcmpl-mock",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]any{
				{
					"index": 0,
					"delta": map[string]any{"role": "assistant"},
				},
			},
		},
		{
			"id":      "chatcmpl-mock",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]any{
				{
					"index": 0,
					"delta": map[string]any{"content": text},
				},
			},
		},
		{
			"id":      "chatcmpl-mock",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]any{
				{
					"index":         0,
					"delta":         map[string]any{},
					"finish_reason": "stop",
				},
			},
		},
	}
	for _, chunk := range chunks {
		data, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func main() {
	port := flag.Int("port", 19101, "server port")
	recordPath := flag.String("record-path", "", "optional jsonl event output")
	flag.Parse()

	store, err := newState(*recordPath)
	if err != nil {
		log.Fatalf("failed to initialize state: %v", err)
	}
	defer func() {
		if err := store.close(); err != nil {
			log.Printf("close record file: %v", err)
		}
	}()

	mux := http.NewServeMux()

	mux.HandleFunc("/__admin/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("/__admin/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		store.reset()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("/__admin/profiles", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"profiles": store.profiles})
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		var req adminProfilesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		store.setProfiles(req.Profiles)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("/__admin/events", func(w http.ResponseWriter, _ *http.Request) {
		events := store.snapshot()
		counts := make(map[string]int)
		for _, ev := range events {
			counts[ev.Key]++
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"events": events,
			"count":  len(events),
			"counts": counts,
		})
	})

	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		key := bearerKey(r)
		store.addEvent(event{
			Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
			Path:       r.URL.Path,
			Key:        key,
			ActionType: "list_models",
			StatusCode: http.StatusOK,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":       "gpt-4.1",
					"object":   "model",
					"created":  time.Now().Unix(),
					"owned_by": "mock-openai",
				},
			},
		})
	})

	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}

		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]any{
					"message": err.Error(),
					"type":    "invalid_request_error",
				},
			})
			return
		}

		key := bearerKey(r)
		act := store.nextAction(key)
		ev := event{
			Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
			Path:        r.URL.Path,
			Key:         key,
			Model:       req.Model,
			User:        req.User,
			Stream:      req.Stream,
			ActionType:  act.Type,
			ActionDelay: act.DelayMs,
			Message:     act.Message,
		}

		if act.DelayMs > 0 {
			select {
			case <-time.After(time.Duration(act.DelayMs) * time.Millisecond):
			case <-r.Context().Done():
				ev.StatusCode = 499
				ev.Message = "client_cancelled"
				store.addEvent(ev)
				return
			}
		}

		switch act.Type {
		case "timeout":
			<-r.Context().Done()
			ev.StatusCode = 499
			ev.Message = "client_timeout"
			store.addEvent(ev)
			return
		case "error":
			status := act.Status
			if status == 0 {
				status = http.StatusBadGateway
			}
			ev.StatusCode = status
			store.addEvent(ev)
			writeJSON(w, status, map[string]any{
				"error": map[string]any{
					"message": firstNonEmpty(act.Message, "mock upstream error"),
					"type":    "mock_upstream_error",
				},
			})
			return
		default:
			text := firstNonEmpty(act.ResponseText, fmt.Sprintf("mock response for %s", key))
			ev.StatusCode = http.StatusOK
			store.addEvent(ev)
			if req.Stream {
				writeSSESuccess(w, req.Model, text)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"id":      "chatcmpl-mock",
				"object":  "chat.completion",
				"created": time.Now().Unix(),
				"model":   req.Model,
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]any{
							"role":    "assistant",
							"content": text,
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]any{
					"prompt_tokens":     8,
					"completion_tokens": 12,
					"total_tokens":      20,
				},
			})
		}
	})

	server := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", *port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("mock_openai_server listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
