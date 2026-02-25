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
type AnthropicPlugin struct {
	config *AnthropicConfig
	store  *TokenStore
}

// AnthropicConfig contains the plugin configuration
type AnthropicConfig struct {
	APIKey       string `json:"api_key"`        // Real Anthropic API key
	CreddyServer string `json:"creddy_server"`  // Creddy server URL (for proxy validation)
}

// TokenStore manages the mapping between Creddy tokens and the real API key
type TokenStore struct {
	mu     sync.RWMutex
	tokens map[string]*TokenInfo
}

// TokenInfo holds token metadata
type TokenInfo struct {
	AgentName string
	Scope     string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// NewTokenStore creates a new token store
func NewTokenStore() *TokenStore {
	return &TokenStore{
		tokens: make(map[string]*TokenInfo),
	}
}

// Add stores a new token
func (s *TokenStore) Add(token string, info *TokenInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = info
}

// Get retrieves token info if valid
func (s *TokenStore) Get(token string) (*TokenInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info, ok := s.tokens[token]
	if !ok {
		return nil, false
	}
	if time.Now().After(info.ExpiresAt) {
		return nil, false
	}
	return info, true
}

// Remove deletes a token
func (s *TokenStore) Remove(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, token)
}

// Cleanup removes expired tokens
func (s *TokenStore) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for token, info := range s.tokens {
		if now.After(info.ExpiresAt) {
			delete(s.tokens, token)
		}
	}
}

// generateToken creates a secure random token
func generateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "crd_" + hex.EncodeToString(bytes), nil
}

func (p *AnthropicPlugin) Info(ctx context.Context) (*sdk.PluginInfo, error) {
	return &sdk.PluginInfo{
		Name:             PluginName,
		Version:          PluginVersion,
		Description:      "Anthropic API access via proxy (no Admin API required)",
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
			Description: "Anthropic API key (regular key, not admin)",
			Required:    true,
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

	p.config = &config
	p.store = NewTokenStore()

	// Start background cleanup
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		for range ticker.C {
			p.store.Cleanup()
		}
	}()

	return nil
}

func (p *AnthropicPlugin) Validate(ctx context.Context) error {
	if p.config == nil {
		return fmt.Errorf("plugin not configured")
	}

	// Validate the API key by making a simple request
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.anthropic.com/v1/models", nil)
	if err != nil {
		return err
	}

	req.Header.Set("x-api-key", p.config.APIKey)
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
	if p.config == nil {
		return nil, fmt.Errorf("plugin not configured")
	}

	// Generate a Creddy token (NOT the real Anthropic key)
	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	expiresAt := time.Now().Add(req.TTL)

	// Store the token mapping
	p.store.Add(token, &TokenInfo{
		AgentName: req.Agent.Name,
		Scope:     req.Scope,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	})

	return &sdk.Credential{
		Value:      token,
		ExternalID: token, // Use token as external ID for revocation
		ExpiresAt:  expiresAt,
		Metadata: map[string]string{
			"type":       "proxy_token",
			"agent_name": req.Agent.Name,
			"scope":      req.Scope,
			"note":       "Use with ANTHROPIC_BASE_URL pointing to the Creddy Anthropic proxy",
		},
	}, nil
}

func (p *AnthropicPlugin) RevokeCredential(ctx context.Context, externalID string) error {
	if p.config == nil {
		return fmt.Errorf("plugin not configured")
	}

	p.store.Remove(externalID)
	return nil
}

func (p *AnthropicPlugin) MatchScope(ctx context.Context, scope string) (bool, error) {
	return scope == "anthropic" || strings.HasPrefix(scope, "anthropic:"), nil
}

// GetAPIKey returns the real API key (for proxy use)
func (p *AnthropicPlugin) GetAPIKey() string {
	if p.config == nil {
		return ""
	}
	return p.config.APIKey
}

// ValidateToken checks if a token is valid and returns its info
func (p *AnthropicPlugin) ValidateToken(token string) (*TokenInfo, bool) {
	return p.store.Get(token)
}
