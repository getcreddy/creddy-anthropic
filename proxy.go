package main

import (
	"encoding/json"
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
	mux.HandleFunc("/v1/tokens", p.handleIssueToken) // Token issuance endpoint

	p.server = &http.Server{
		Addr:         p.listenAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second, // Long timeout for streaming
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("Anthropic proxy starting on %s", p.listenAddr)
	return p.server.ListenAndServe()
}

// Stop gracefully shuts down the proxy
func (p *Proxy) Stop() error {
	if p.server != nil {
		return p.server.Close()
	}
	return nil
}

func (p *Proxy) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// handleIssueToken issues a new proxy token
func (p *Proxy) handleIssueToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Parse request
	var req struct {
		TTL       string `json:"ttl"`
		AgentName string `json:"agent_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Default values if no body
		req.TTL = "1h"
		req.AgentName = "anonymous"
	}

	// Parse TTL
	ttl, err := time.ParseDuration(req.TTL)
	if err != nil {
		ttl = 1 * time.Hour
	}

	// Cap TTL at 24h
	if ttl > 24*time.Hour {
		ttl = 24 * time.Hour
	}

	// Generate token
	token, err := generateToken()
	if err != nil {
		http.Error(w, `{"error": "failed to generate token"}`, http.StatusInternalServerError)
		return
	}

	expiresAt := time.Now().Add(ttl)

	// Store token
	p.plugin.store.Add(token, &TokenInfo{
		AgentName: req.AgentName,
		Scope:     "anthropic",
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	})

	// Return token
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"token":      token,
		"expires_at": expiresAt.Format(time.RFC3339),
		"ttl":        ttl.String(),
	})

	log.Printf("Issued token for agent=%s ttl=%s", req.AgentName, ttl)
}

func (p *Proxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Extract the Creddy token from Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		// Also check x-api-key header (Anthropic's native header)
		authHeader = r.Header.Get("x-api-key")
		if authHeader == "" {
			http.Error(w, `{"error": {"type": "authentication_error", "message": "Missing API key"}}`, http.StatusUnauthorized)
			return
		}
	} else {
		// Strip "Bearer " prefix if present
		authHeader = strings.TrimPrefix(authHeader, "Bearer ")
	}

	token := authHeader

	// Validate the Creddy token
	tokenInfo, valid := p.plugin.ValidateToken(token)
	if !valid {
		http.Error(w, `{"error": {"type": "authentication_error", "message": "Invalid or expired token"}}`, http.StatusUnauthorized)
		return
	}

	// Log the request (without sensitive data)
	log.Printf("Proxying request: %s %s (agent: %s, scope: %s)", r.Method, r.URL.Path, tokenInfo.AgentName, tokenInfo.Scope)

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
		// Skip hop-by-hop headers and auth headers
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
		Timeout: 120 * time.Second, // Long timeout for streaming
	}

	upstreamResp, err := client.Do(upstreamReq)
	if err != nil {
		log.Printf("Upstream request failed: %v", err)
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
		// Handle SSE streaming
		p.handleStreaming(w, upstreamResp)
	} else {
		// Regular response
		w.WriteHeader(upstreamResp.StatusCode)
		io.Copy(w, upstreamResp.Body)
	}
}

func (p *Proxy) handleStreaming(w http.ResponseWriter, upstreamResp *http.Response) {
	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering
	w.WriteHeader(upstreamResp.StatusCode)

	// Get the flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("Warning: ResponseWriter does not support flushing")
		io.Copy(w, upstreamResp.Body)
		return
	}

	// Stream the response
	buf := make([]byte, 4096)
	for {
		n, err := upstreamResp.Body.Read(buf)
		if n > 0 {
			_, writeErr := w.Write(buf[:n])
			if writeErr != nil {
				log.Printf("Error writing response: %v", writeErr)
				return
			}
			flusher.Flush()
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("Error reading upstream: %v", err)
			}
			return
		}
	}
}

// isHopByHop returns true for hop-by-hop headers that shouldn't be proxied
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
