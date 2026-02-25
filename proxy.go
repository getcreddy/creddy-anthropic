package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	AnthropicBaseURL = "https://api.anthropic.com"
)

// ProxyServer handles proxying requests to Anthropic
type ProxyServer struct {
	plugin *AnthropicPlugin
	server *http.Server
}

// NewProxyServer creates a new proxy server
func NewProxyServer(plugin *AnthropicPlugin) *ProxyServer {
	return &ProxyServer{
		plugin: plugin,
	}
}

// Start starts the proxy server
func (ps *ProxyServer) Start(port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", ps.handleProxy)

	ps.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute,
	}

	log.Printf("Anthropic proxy listening on :%d", port)
	return ps.server.ListenAndServe()
}

// Stop gracefully stops the proxy server
func (ps *ProxyServer) Stop(ctx context.Context) error {
	if ps.server != nil {
		return ps.server.Shutdown(ctx)
	}
	return nil
}

// handleProxy handles all proxy requests
func (ps *ProxyServer) handleProxy(w http.ResponseWriter, r *http.Request) {
	// Extract token from x-api-key header (standard for Anthropic SDK)
	token := r.Header.Get("x-api-key")
	if token == "" {
		// Also check Authorization header
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
	}

	if token == "" {
		http.Error(w, `{"error": {"type": "authentication_error", "message": "missing api key"}}`, http.StatusUnauthorized)
		return
	}

	// Validate the crd_xxx token
	if !strings.HasPrefix(token, "crd_") {
		http.Error(w, `{"error": {"type": "authentication_error", "message": "invalid token format"}}`, http.StatusUnauthorized)
		return
	}

	tokenInfo, valid := ps.plugin.ValidateToken(token)
	if !valid {
		http.Error(w, `{"error": {"type": "authentication_error", "message": "invalid or expired token"}}`, http.StatusUnauthorized)
		return
	}

	// Get the real API key
	apiKey := ps.plugin.GetAPIKey()
	if apiKey == "" {
		http.Error(w, `{"error": {"type": "api_error", "message": "plugin not configured"}}`, http.StatusInternalServerError)
		return
	}

	// Build upstream request
	upstreamURL := AnthropicBaseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	upstreamReq, err := http.NewRequestWithContext(ctx, r.Method, upstreamURL, r.Body)
	if err != nil {
		log.Printf("Failed to create upstream request: %v", err)
		http.Error(w, `{"error": {"type": "api_error", "message": "internal error"}}`, http.StatusInternalServerError)
		return
	}

	// Copy headers (except auth headers)
	for k, vv := range r.Header {
		k = http.CanonicalHeaderKey(k)
		if k == "X-Api-Key" || k == "Authorization" || k == "Host" {
			continue
		}
		for _, v := range vv {
			upstreamReq.Header.Add(k, v)
		}
	}

	// Set the real API key
	upstreamReq.Header.Set("x-api-key", apiKey)

	// Ensure anthropic-version is set
	if upstreamReq.Header.Get("anthropic-version") == "" {
		upstreamReq.Header.Set("anthropic-version", "2023-06-01")
	}

	// Make the request
	client := &http.Client{
		Timeout: 5 * time.Minute,
	}

	resp, err := client.Do(upstreamReq)
	if err != nil {
		log.Printf("Upstream request failed: %v", err)
		http.Error(w, `{"error": {"type": "api_error", "message": "upstream request failed"}}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Log the request (minimal)
	log.Printf("[%s] %s %s → %d", tokenInfo.AgentName, r.Method, r.URL.Path, resp.StatusCode)

	// Copy response headers
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(resp.StatusCode)

	// Check if streaming (SSE)
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		// Stream with flushing
		flusher, ok := w.(http.Flusher)
		if !ok {
			io.Copy(w, resp.Body)
			return
		}

		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
				flusher.Flush()
			}
			if err != nil {
				break
			}
		}
	} else {
		io.Copy(w, resp.Body)
	}
}
