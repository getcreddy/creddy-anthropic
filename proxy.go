package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	AnthropicAPIURL = "https://api.anthropic.com"
)

// Proxy handles HTTP proxying to Anthropic API
type Proxy struct {
	plugin     *AnthropicPlugin
	listenAddr string
	server     *http.Server
}

// NewProxy creates a new proxy instance
func NewProxy(plugin *AnthropicPlugin, listenAddr string) *Proxy {
	return &Proxy{
		plugin:     plugin,
		listenAddr: listenAddr,
	}
}

// Start begins listening for requests
func (p *Proxy) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", p.handleRequest)
	mux.HandleFunc("/health", p.handleHealth)
	mux.HandleFunc("/_creddy/status", p.handleStatus)

	p.server = &http.Server{
		Addr:         p.listenAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second, // Long timeout for streaming
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("[anthropic-proxy] Starting on %s", p.listenAddr)
	return p.server.ListenAndServe()
}

// Stop gracefully shuts down the proxy
func (p *Proxy) Stop() error {
	if p.server != nil {
		log.Printf("[anthropic-proxy] Shutting down")
		return p.server.Close()
	}
	return nil
}

func (p *Proxy) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (p *Proxy) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"active_tokens": %d}`, p.plugin.GetTokenCount())
}

func (p *Proxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Don't proxy health/status endpoints
	if r.URL.Path == "/health" || r.URL.Path == "/_creddy/status" {
		return
	}

	// Extract the token from Authorization header or x-api-key
	token := ""
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		token = strings.TrimPrefix(authHeader, "Bearer ")
	}
	if token == "" {
		token = r.Header.Get("x-api-key")
	}

	if token == "" {
		http.Error(w, `{"error": {"type": "authentication_error", "message": "Missing API key"}}`, http.StatusUnauthorized)
		return
	}

	// Validate the token exists in our store
	// Note: Expiry is managed by Creddy - if token is in store, it's valid
	tokenInfo, valid := p.plugin.ValidateToken(token)
	if !valid {
		http.Error(w, `{"error": {"type": "authentication_error", "message": "Invalid or revoked token"}}`, http.StatusUnauthorized)
		return
	}

	// Log the request (without sensitive data)
	log.Printf("[anthropic-proxy] %s %s (agent: %s)", r.Method, r.URL.Path, tokenInfo.AgentName)

	// Create the upstream request
	upstreamURL := AnthropicAPIURL + r.URL.Path
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, r.Body)
	if err != nil {
		http.Error(w, `{"error": {"type": "internal_error", "message": "Failed to create upstream request"}}`, http.StatusInternalServerError)
		return
	}

	// Copy headers, but replace auth with real API key
	for key, values := range r.Header {
		if isHopByHop(key) || key == "Authorization" || key == "X-Api-Key" {
			continue
		}
		for _, value := range values {
			upstreamReq.Header.Add(key, value)
		}
	}

	// Set the real Anthropic API key
	upstreamReq.Header.Set("x-api-key", p.plugin.GetAPIKey())

	// Ensure required Anthropic headers
	if upstreamReq.Header.Get("anthropic-version") == "" {
		upstreamReq.Header.Set("anthropic-version", "2023-06-01")
	}

	// Make the upstream request
	client := &http.Client{
		Timeout: 120 * time.Second,
	}

	upstreamResp, err := client.Do(upstreamReq)
	if err != nil {
		log.Printf("[anthropic-proxy] Upstream error: %v", err)
		http.Error(w, fmt.Sprintf(`{"error": {"type": "upstream_error", "message": "Failed to reach Anthropic API: %s"}}`, err.Error()), http.StatusBadGateway)
		return
	}
	defer upstreamResp.Body.Close()

	// Copy response headers
	for key, values := range upstreamResp.Header {
		if isHopByHop(key) {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Check if this is a streaming response
	contentType := upstreamResp.Header.Get("Content-Type")
	isStreaming := strings.Contains(contentType, "text/event-stream")

	if isStreaming {
		p.handleStreaming(w, upstreamResp)
	} else {
		w.WriteHeader(upstreamResp.StatusCode)
		io.Copy(w, upstreamResp.Body)
	}
}

func (p *Proxy) handleStreaming(w http.ResponseWriter, upstreamResp *http.Response) {
	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(upstreamResp.StatusCode)

	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("[anthropic-proxy] Warning: ResponseWriter does not support flushing")
		io.Copy(w, upstreamResp.Body)
		return
	}

	buf := make([]byte, 4096)
	for {
		n, err := upstreamResp.Body.Read(buf)
		if n > 0 {
			_, writeErr := w.Write(buf[:n])
			if writeErr != nil {
				log.Printf("[anthropic-proxy] Write error: %v", writeErr)
				return
			}
			flusher.Flush()
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("[anthropic-proxy] Read error: %v", err)
			}
			return
		}
	}
}

func isHopByHop(header string) bool {
	hopByHop := map[string]bool{
		"Connection":          true,
		"Keep-Alive":          true,
		"Proxy-Authenticate":  true,
		"Proxy-Authorization": true,
		"Te":                  true,
		"Trailers":            true,
		"Transfer-Encoding":   true,
		"Upgrade":             true,
	}
	return hopByHop[http.CanonicalHeaderKey(header)]
}
