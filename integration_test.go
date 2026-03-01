//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/getcreddy/creddy-plugin-sdk"
)

func getAPIKey(t *testing.T) string {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping integration tests")
	}
	if !strings.HasPrefix(key, "sk-ant-") {
		t.Fatalf("ANTHROPIC_API_KEY must start with sk-ant-, got: %s...", key[:10])
	}
	return key
}

func getProxyPort() int {
	// Use a different port for tests to avoid conflicts
	return 18401
}

func TestIntegration_PluginInfo(t *testing.T) {
	plugin := NewPlugin()
	info, err := plugin.Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error: %v", err)
	}

	if info.Name != "anthropic" {
		t.Errorf("expected name 'anthropic', got %q", info.Name)
	}
	if info.Version == "" {
		t.Error("expected non-empty version")
	}
	t.Logf("Plugin: %s v%s", info.Name, info.Version)
}

func TestIntegration_Configure(t *testing.T) {
	apiKey := getAPIKey(t)

	tests := []struct {
		name    string
		config  string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid api key",
			config:  fmt.Sprintf(`{"api_key": "%s", "proxy_port": %d}`, apiKey, getProxyPort()),
			wantErr: false,
		},
		{
			name:    "missing api key",
			config:  `{}`,
			wantErr: true,
			errMsg:  "api_key is required",
		},
		{
			name:    "empty api key",
			config:  `{"api_key": ""}`,
			wantErr: true,
			errMsg:  "api_key is required",
		},
		{
			name:    "invalid JSON",
			config:  `{invalid}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := NewPlugin()
			err := plugin.Configure(context.Background(), tt.config)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestIntegration_MatchScope(t *testing.T) {
	plugin := NewPlugin()

	tests := []struct {
		scope string
		want  bool
	}{
		{"anthropic", true},
		{"anthropic:claude", true},
		{"anthropic:messages", true},
		{"github", false},
		{"openai", false},
		{"aws", false},
	}

	for _, tt := range tests {
		t.Run(tt.scope, func(t *testing.T) {
			got, err := plugin.MatchScope(context.Background(), tt.scope)
			if err != nil {
				t.Fatalf("MatchScope() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("MatchScope(%q) = %v, want %v", tt.scope, got, tt.want)
			}
		})
	}
}

func TestIntegration_GetCredential(t *testing.T) {
	apiKey := getAPIKey(t)
	port := getProxyPort()

	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), fmt.Sprintf(`{"api_key": "%s", "proxy_port": %d}`, apiKey, port))
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	// Wait for proxy to start
	time.Sleep(100 * time.Millisecond)

	req := &sdk.CredentialRequest{
		Scope: "anthropic",
		TTL:   10 * time.Minute,
		Agent: sdk.Agent{
			ID:   "test-agent-1",
			Name: "test-agent",
		},
	}

	cred, err := plugin.GetCredential(context.Background(), req)
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}

	// Validate credential fields
	if cred.Value == "" {
		t.Fatal("expected credential value")
	}
	if !strings.HasPrefix(cred.Value, "crd_") {
		t.Errorf("expected token to start with crd_, got: %s", cred.Value[:10])
	}
	if cred.ExternalID == "" {
		t.Fatal("expected external ID for revocation")
	}
	if cred.ExpiresAt.IsZero() {
		t.Fatal("expected expiration time")
	}

	t.Logf("Created token: %s..., expires: %v", cred.Value[:15], cred.ExpiresAt)

	// Clean up
	err = plugin.RevokeCredential(context.Background(), cred.ExternalID)
	if err != nil {
		t.Fatalf("RevokeCredential() error: %v", err)
	}
}

func TestIntegration_FullProxyLifecycle(t *testing.T) {
	apiKey := getAPIKey(t)
	port := getProxyPort() + 1 // Use different port to avoid conflicts

	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), fmt.Sprintf(`{"api_key": "%s", "proxy_port": %d}`, apiKey, port))
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	// Wait for proxy to start
	time.Sleep(200 * time.Millisecond)

	// Create credential
	req := &sdk.CredentialRequest{
		Scope: "anthropic",
		TTL:   5 * time.Minute,
		Agent: sdk.Agent{
			ID:   "lifecycle-test",
			Name: "lifecycle-test",
		},
	}

	cred, err := plugin.GetCredential(context.Background(), req)
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	t.Logf("Got token: %s...", cred.Value[:15])

	// Test proxy with valid token
	t.Run("valid token works", func(t *testing.T) {
		proxyURL := fmt.Sprintf("http://localhost:%d/v1/messages", port)
		body := `{
			"model": "claude-3-haiku-20240307",
			"max_tokens": 10,
			"messages": [{"role": "user", "content": "Say 'test'"}]
		}`

		httpReq, _ := http.NewRequest("POST", proxyURL, strings.NewReader(body))
		httpReq.Header.Set("x-api-key", cred.Value)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("anthropic-version", "2023-06-01")

		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			t.Fatalf("proxy request failed: %v", err)
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("proxy request failed: status=%d body=%s", resp.StatusCode, string(respBody))
		}

		var result map[string]interface{}
		if err := json.Unmarshal(respBody, &result); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if _, ok := result["content"]; !ok {
			t.Fatalf("expected 'content' in response, got: %s", string(respBody))
		}
		t.Log("Proxy request successful ✓")
	})

	// Revoke the token
	err = plugin.RevokeCredential(context.Background(), cred.ExternalID)
	if err != nil {
		t.Fatalf("RevokeCredential() error: %v", err)
	}
	t.Log("Revoked token ✓")

	// Test proxy rejects revoked token
	t.Run("revoked token rejected", func(t *testing.T) {
		proxyURL := fmt.Sprintf("http://localhost:%d/v1/messages", port)
		body := `{
			"model": "claude-3-haiku-20240307",
			"max_tokens": 10,
			"messages": [{"role": "user", "content": "Say 'test'"}]
		}`

		httpReq, _ := http.NewRequest("POST", proxyURL, strings.NewReader(body))
		httpReq.Header.Set("x-api-key", cred.Value)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("anthropic-version", "2023-06-01")

		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			t.Fatalf("proxy request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401 for revoked token, got %d", resp.StatusCode)
		}
		t.Log("Revoked token correctly rejected ✓")
	})
}

func TestIntegration_InvalidTokenRejected(t *testing.T) {
	apiKey := getAPIKey(t)
	port := getProxyPort() + 2

	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), fmt.Sprintf(`{"api_key": "%s", "proxy_port": %d}`, apiKey, port))
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	// Wait for proxy to start
	time.Sleep(200 * time.Millisecond)

	tests := []struct {
		name  string
		token string
	}{
		{"empty token", ""},
		{"invalid format", "not-a-valid-token"},
		{"wrong prefix", "sk-ant-fake-token"},
		{"fake crd token", "crd_0000000000000000000000000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxyURL := fmt.Sprintf("http://localhost:%d/v1/messages", port)
			body := `{"model": "claude-3-haiku-20240307", "max_tokens": 10, "messages": [{"role": "user", "content": "test"}]}`

			httpReq, _ := http.NewRequest("POST", proxyURL, strings.NewReader(body))
			httpReq.Header.Set("x-api-key", tt.token)
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("anthropic-version", "2023-06-01")

			resp, err := http.DefaultClient.Do(httpReq)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			resp.Body.Close()

			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("expected 401 for %s, got %d", tt.name, resp.StatusCode)
			}
		})
	}
}

func TestIntegration_ConcurrentTokens(t *testing.T) {
	apiKey := getAPIKey(t)
	port := getProxyPort() + 3

	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), fmt.Sprintf(`{"api_key": "%s", "proxy_port": %d}`, apiKey, port))
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	// Wait for proxy to start
	time.Sleep(200 * time.Millisecond)

	const numTokens = 10
	var wg sync.WaitGroup
	tokens := make([]*sdk.Credential, numTokens)
	errors := make([]error, numTokens)

	// Generate tokens concurrently
	for i := 0; i < numTokens; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cred, err := plugin.GetCredential(context.Background(), &sdk.CredentialRequest{
				Scope: "anthropic",
				TTL:   5 * time.Minute,
				Agent: sdk.Agent{
					ID:   fmt.Sprintf("concurrent-test-%d", idx),
					Name: fmt.Sprintf("concurrent-test-%d", idx),
				},
			})
			tokens[idx] = cred
			errors[idx] = err
		}(i)
	}

	wg.Wait()

	// Check for errors
	for i, err := range errors {
		if err != nil {
			t.Fatalf("Token %d failed: %v", i, err)
		}
	}

	// Verify all tokens are unique
	seen := make(map[string]bool)
	for i, cred := range tokens {
		if seen[cred.Value] {
			t.Fatalf("Token %d is a duplicate", i)
		}
		seen[cred.Value] = true
	}
	t.Logf("Generated %d unique tokens concurrently ✓", numTokens)

	// Test one token works through proxy (don't test all to save API calls)
	proxyURL := fmt.Sprintf("http://localhost:%d/v1/messages", port)
	body := `{
		"model": "claude-3-haiku-20240307",
		"max_tokens": 5,
		"messages": [{"role": "user", "content": "Hi"}]
	}`

	httpReq, _ := http.NewRequest("POST", proxyURL, strings.NewReader(body))
	httpReq.Header.Set("x-api-key", tokens[0].Value)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	t.Log("Token works through proxy ✓")

	// Clean up all tokens
	for _, cred := range tokens {
		plugin.RevokeCredential(context.Background(), cred.ExternalID)
	}
}

func TestIntegration_TokenExpiry(t *testing.T) {
	apiKey := getAPIKey(t)
	port := getProxyPort() + 4

	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), fmt.Sprintf(`{"api_key": "%s", "proxy_port": %d}`, apiKey, port))
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	// Wait for proxy to start
	time.Sleep(200 * time.Millisecond)

	// Create a very short-lived token
	shortTTL := 2 * time.Second
	cred, err := plugin.GetCredential(context.Background(), &sdk.CredentialRequest{
		Scope: "anthropic",
		TTL:   shortTTL,
		Agent: sdk.Agent{
			ID:   "expiry-test",
			Name: "expiry-test",
		},
	})
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}

	// Verify expiry is set correctly
	expectedExpiry := time.Now().Add(shortTTL)
	if cred.ExpiresAt.Before(time.Now()) {
		t.Fatal("Token already expired at creation")
	}
	if cred.ExpiresAt.After(expectedExpiry.Add(1 * time.Second)) {
		t.Errorf("Token expiry too far in future: %v (expected around %v)", cred.ExpiresAt, expectedExpiry)
	}

	t.Logf("Token expires at: %v (in %v)", cred.ExpiresAt, time.Until(cred.ExpiresAt))

	// Wait for token to expire
	time.Sleep(shortTTL + 500*time.Millisecond)

	// Verify proxy rejects expired token
	proxyURL := fmt.Sprintf("http://localhost:%d/v1/messages", port)
	body := `{"model": "claude-3-haiku-20240307", "max_tokens": 5, "messages": [{"role": "user", "content": "test"}]}`

	httpReq, _ := http.NewRequest("POST", proxyURL, strings.NewReader(body))
	httpReq.Header.Set("x-api-key", cred.Value)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired token, got %d", resp.StatusCode)
	}
	t.Log("Expired token correctly rejected ✓")
}

func TestIntegration_SSEStreaming(t *testing.T) {
	apiKey := getAPIKey(t)
	port := getProxyPort() + 5

	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), fmt.Sprintf(`{"api_key": "%s", "proxy_port": %d}`, apiKey, port))
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	// Wait for proxy to start
	time.Sleep(200 * time.Millisecond)

	cred, err := plugin.GetCredential(context.Background(), &sdk.CredentialRequest{
		Scope: "anthropic",
		TTL:   5 * time.Minute,
		Agent: sdk.Agent{
			ID:   "sse-test",
			Name: "sse-test",
		},
	})
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	defer plugin.RevokeCredential(context.Background(), cred.ExternalID)

	// Test streaming request
	proxyURL := fmt.Sprintf("http://localhost:%d/v1/messages", port)
	body := `{
		"model": "claude-3-haiku-20240307",
		"max_tokens": 20,
		"stream": true,
		"messages": [{"role": "user", "content": "Count to 3"}]
	}`

	httpReq, _ := http.NewRequest("POST", proxyURL, strings.NewReader(body))
	httpReq.Header.Set("x-api-key", cred.Value)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("streaming request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("streaming request failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	// Verify we get SSE content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/event-stream") {
		t.Errorf("expected text/event-stream content type, got: %s", contentType)
	}

	// Read some events
	buf := make([]byte, 4096)
	n, err := resp.Body.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("failed to read stream: %v", err)
	}

	events := string(buf[:n])
	if !strings.Contains(events, "event:") && !strings.Contains(events, "data:") {
		t.Errorf("expected SSE events, got: %s", events)
	}

	t.Log("SSE streaming works ✓")
}

func TestIntegration_RevokeIdempotent(t *testing.T) {
	plugin := NewPlugin()
	apiKey := getAPIKey(t)

	err := plugin.Configure(context.Background(), fmt.Sprintf(`{"api_key": "%s", "proxy_port": %d}`, apiKey, getProxyPort()+6))
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	// Revoking a nonexistent token should not error
	err = plugin.RevokeCredential(context.Background(), "crd_nonexistent_token_12345")
	if err != nil {
		t.Fatalf("RevokeCredential() should be idempotent, got error: %v", err)
	}

	// Revoking same token twice should not error
	cred, err := plugin.GetCredential(context.Background(), &sdk.CredentialRequest{
		Scope: "anthropic",
		TTL:   5 * time.Minute,
		Agent: sdk.Agent{ID: "idempotent-test", Name: "idempotent-test"},
	})
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}

	err = plugin.RevokeCredential(context.Background(), cred.ExternalID)
	if err != nil {
		t.Fatalf("First revoke failed: %v", err)
	}

	err = plugin.RevokeCredential(context.Background(), cred.ExternalID)
	if err != nil {
		t.Fatalf("Second revoke should be idempotent, got: %v", err)
	}

	t.Log("Revocation is idempotent ✓")
}

func TestIntegration_ProxyForwardsHeaders(t *testing.T) {
	apiKey := getAPIKey(t)
	port := getProxyPort() + 7

	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), fmt.Sprintf(`{"api_key": "%s", "proxy_port": %d}`, apiKey, port))
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	cred, err := plugin.GetCredential(context.Background(), &sdk.CredentialRequest{
		Scope: "anthropic",
		TTL:   5 * time.Minute,
		Agent: sdk.Agent{ID: "headers-test", Name: "headers-test"},
	})
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	defer plugin.RevokeCredential(context.Background(), cred.ExternalID)

	// Test that custom headers are forwarded
	proxyURL := fmt.Sprintf("http://localhost:%d/v1/messages", port)
	body := `{
		"model": "claude-3-haiku-20240307",
		"max_tokens": 5,
		"messages": [{"role": "user", "content": "Hi"}]
	}`

	httpReq, _ := http.NewRequest("POST", proxyURL, bytes.NewBufferString(body))
	httpReq.Header.Set("x-api-key", cred.Value)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("anthropic-beta", "messages-2023-12-15") // Custom beta header

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Should succeed (headers forwarded correctly)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("request failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	t.Log("Headers forwarded correctly ✓")
}
