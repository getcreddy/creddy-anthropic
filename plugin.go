package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	sdk "github.com/getcreddy/creddy-plugin-sdk"
)

const (
	PluginName    = "anthropic"
	PluginVersion = "0.0.1"
)

// AnthropicPlugin implements the Creddy Plugin interface for Anthropic
// It also runs an HTTP proxy for agents to use
type AnthropicPlugin struct {
	mu        sync.RWMutex
	config    *AnthropicConfig
	store     *TokenStore
	proxyPort int
}

// AnthropicConfig contains the plugin configuration
type AnthropicConfig struct {
	APIKey    string `json:"api_key"`    // Real Anthropic API key
	ProxyPort int    `json:"proxy_port"` // Port for the proxy server (default 8401)
}

// TokenStore manages valid tokens
type TokenStore struct {
	mu     sync.RWMutex
	tokens map[string]*TokenInfo
}

// TokenInfo holds token metadata
type TokenInfo struct {
	AgentName string
	Scope     string
	CreatedAt time.Time
}

func NewTokenStore() *TokenStore {
	return &TokenStore{
		tokens: make(map[string]*TokenInfo),
	}
}

func (s *TokenStore) Add(token string, info *TokenInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = info
}

func (s *TokenStore) Exists(token string) (*TokenInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info, ok := s.tokens[token]
	return info, ok
}

func (s *TokenStore) Remove(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, token)
}

func (s *TokenStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tokens)
}

func generateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "crd_ant_" + hex.EncodeToString(bytes), nil
}

// NewAnthropicPlugin creates a plugin with initialized store
func NewAnthropicPlugin() *AnthropicPlugin {
	return &AnthropicPlugin{
		store:     NewTokenStore(),
		proxyPort: 8401, // Default, can be overridden by config
	}
}

func (p *AnthropicPlugin) Info(ctx context.Context) (*sdk.PluginInfo, error) {
	return &sdk.PluginInfo{
		Name:             PluginName,
		Version:          PluginVersion,
		Description:      "Anthropic API access via proxy",
		MinCreddyVersion: "0.4.0",
	}, nil
}

func (p *AnthropicPlugin) Scopes(ctx context.Context) ([]sdk.ScopeSpec, error) {
	return []sdk.ScopeSpec{
		{
			Pattern:     "anthropic",
			Description: "Full access to the Anthropic API",
			Examples:    []string{"anthropic"},
		},
		{
			Pattern:     "anthropic:claude",
			Description: "Access to Claude models",
			Examples:    []string{"anthropic:claude"},
		},
	}, nil
}

func (p *AnthropicPlugin) ConfigSchema(ctx context.Context) ([]sdk.ConfigField, error) {
	return []sdk.ConfigField{
		{
			Name:        "api_key",
			Type:        "secret",
			Description: "Anthropic API key",
			Required:    true,
		},
		{
			Name:        "proxy_port",
			Type:        "number",
			Description: "Port for the proxy server (default 8401)",
			Required:    false,
		},
	}, nil
}

func (p *AnthropicPlugin) Constraints(ctx context.Context) (*sdk.Constraints, error) {
	return nil, nil
}

func (p *AnthropicPlugin) Configure(ctx context.Context, configJSON string) error {
	var config AnthropicConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}

	if config.APIKey == "" {
		return fmt.Errorf("api_key is required")
	}

	if config.ProxyPort == 0 {
		config.ProxyPort = 8401
	}

	p.mu.Lock()
	p.config = &config
	p.proxyPort = config.ProxyPort
	p.mu.Unlock()

	return nil
}

func (p *AnthropicPlugin) Validate(ctx context.Context) error {
	p.mu.RLock()
	config := p.config
	p.mu.RUnlock()

	if config == nil {
		return fmt.Errorf("plugin not configured")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.anthropic.com/v1/models", nil)
	if err != nil {
		return err
	}

	req.Header.Set("x-api-key", config.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to validate API key: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("invalid API key")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("anthropic API error (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func (p *AnthropicPlugin) GetCredential(ctx context.Context, req *sdk.CredentialRequest) (*sdk.Credential, error) {
	p.mu.RLock()
	config := p.config
	proxyPort := p.proxyPort
	p.mu.RUnlock()

	if config == nil {
		return nil, fmt.Errorf("plugin not configured")
	}

	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	// Store token for proxy validation
	p.store.Add(token, &TokenInfo{
		AgentName: req.Agent.Name,
		Scope:     req.Scope,
		CreatedAt: time.Now(),
	})

	proxyURL := fmt.Sprintf("http://localhost:%d", proxyPort)

	return &sdk.Credential{
		Value:      token,
		ExternalID: token,
		ExpiresAt:  time.Now().Add(req.TTL),
		Metadata: map[string]string{
			"proxy_url":  proxyURL,
			"agent_name": req.Agent.Name,
			"scope":      req.Scope,
		},
	}, nil
}

func (p *AnthropicPlugin) RevokeCredential(ctx context.Context, externalID string) error {
	p.store.Remove(externalID)
	return nil
}

func (p *AnthropicPlugin) MatchScope(ctx context.Context, scope string) (bool, error) {
	return scope == "anthropic" || strings.HasPrefix(scope, "anthropic:"), nil
}

// --- Proxy support ---

func (p *AnthropicPlugin) GetAPIKey() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.config == nil {
		return ""
	}
	return p.config.APIKey
}

func (p *AnthropicPlugin) ValidateToken(token string) (*TokenInfo, bool) {
	return p.store.Exists(token)
}

func (p *AnthropicPlugin) GetProxyPort() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.proxyPort
}

func (p *AnthropicPlugin) GetTokenCount() int {
	return p.store.Count()
}

func (p *AnthropicPlugin) IsConfigured() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config != nil
}
