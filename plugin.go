package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	sdk "github.com/getcreddy/creddy-plugin-sdk"
)

const (
	PluginName    = "anthropic"
	PluginVersion = "0.0.2"
)

// AnthropicPlugin implements the Creddy Plugin interface for Anthropic
type AnthropicPlugin struct {
	mu     sync.RWMutex
	config *AnthropicConfig
	tokens *TokenStore
	proxy  *ProxyServer
}

// AnthropicConfig contains the plugin configuration
type AnthropicConfig struct {
	APIKey    string `json:"api_key"`    // Real Anthropic API key
	ProxyPort int    `json:"proxy_port"` // Port for plugin proxy (default 8401)
}

// TokenStore manages issued crd_xxx tokens
type TokenStore struct {
	mu     sync.RWMutex
	tokens map[string]*TokenInfo
}

// TokenInfo holds metadata about an issued token
type TokenInfo struct {
	AgentID   string
	AgentName string
	Scope     string
	ExpiresAt time.Time
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

func (s *TokenStore) Get(token string) (*TokenInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info, ok := s.tokens[token]
	if !ok {
		return nil, false
	}
	// Check expiry
	if time.Now().After(info.ExpiresAt) {
		return nil, false
	}
	return info, true
}

func (s *TokenStore) Remove(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, token)
}

// Cleanup removes expired tokens
func (s *TokenStore) Cleanup() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	now := time.Now()
	removed := 0
	for token, info := range s.tokens {
		if now.After(info.ExpiresAt) {
			delete(s.tokens, token)
			removed++
		}
	}
	return removed
}

func NewPlugin() *AnthropicPlugin {
	p := &AnthropicPlugin{
		tokens: NewTokenStore(),
	}
	// Start cleanup goroutine
	go p.cleanupLoop()
	return p
}

func (p *AnthropicPlugin) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	for range ticker.C {
		p.tokens.Cleanup()
	}
}

// Info returns plugin metadata
func (p *AnthropicPlugin) Info(ctx context.Context) (*sdk.PluginInfo, error) {
	return &sdk.PluginInfo{
		Name:             PluginName,
		Version:          PluginVersion,
		Description:      "Anthropic API access via plugin proxy",
		MinCreddyVersion: "0.4.0",
	}, nil
}

// ConfigSchema returns the configuration fields for the CLI
func (p *AnthropicPlugin) ConfigSchema(ctx context.Context) ([]sdk.ConfigField, error) {
	return []sdk.ConfigField{
		{
			Name:        "api_key",
			Type:        "secret",
			Description: "Anthropic API key (sk-ant-...)",
			Required:    true,
		},
		{
			Name:        "proxy_port",
			Type:        "int",
			Description: "Port for plugin proxy server",
			Required:    false,
			Default:     "8401",
		},
	}, nil
}

// Configure sets up the plugin with the provided config
func (p *AnthropicPlugin) Configure(ctx context.Context, configJSON string) error {
	var cfg AnthropicConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return err
	}

	if cfg.APIKey == "" {
		return errors.New("api_key is required")
	}

	if cfg.ProxyPort == 0 {
		cfg.ProxyPort = 8401
	}

	p.mu.Lock()
	p.config = &cfg
	p.mu.Unlock()

	// Start the proxy server in background
	p.proxy = NewProxyServer(p)
	go func() {
		if err := p.proxy.Start(cfg.ProxyPort); err != nil {
			// Log but don't fail - proxy might already be running
			// or port might be in use
		}
	}()

	return nil
}

// Validate tests the configuration (called after Configure)
func (p *AnthropicPlugin) Validate(ctx context.Context) error {
	p.mu.RLock()
	cfg := p.config
	p.mu.RUnlock()

	if cfg == nil {
		return errors.New("plugin not configured")
	}

	// Could validate API key here, but skip for now
	return nil
}

// Scopes returns the scopes this plugin supports
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

// MatchScope checks if this plugin handles the given scope
func (p *AnthropicPlugin) MatchScope(ctx context.Context, scope string) (bool, error) {
	return strings.HasPrefix(scope, "anthropic"), nil
}

// Constraints returns TTL constraints for this plugin
func (p *AnthropicPlugin) Constraints(ctx context.Context) (*sdk.Constraints, error) {
	return &sdk.Constraints{
		MinTTL:      1 * time.Minute,
		MaxTTL:      1 * time.Hour,
		Description: "Plugin-issued tokens for proxy authentication",
	}, nil
}

// GetCredential issues a crd_xxx token for the agent
func (p *AnthropicPlugin) GetCredential(ctx context.Context, req *sdk.CredentialRequest) (*sdk.Credential, error) {
	p.mu.RLock()
	cfg := p.config
	p.mu.RUnlock()

	if cfg == nil {
		return nil, errors.New("plugin not configured")
	}

	// Generate a crd_xxx token
	token := generateToken()
	expiresAt := time.Now().Add(req.TTL)

	// Store the token
	p.tokens.Add(token, &TokenInfo{
		AgentID:   req.Agent.ID,
		AgentName: req.Agent.Name,
		Scope:     req.Scope,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	})

	return &sdk.Credential{
		Value:      token,
		ExpiresAt:  expiresAt,
		ExternalID: token, // For revocation
	}, nil
}

// RevokeCredential revokes a previously issued token
func (p *AnthropicPlugin) RevokeCredential(ctx context.Context, externalID string) error {
	p.tokens.Remove(externalID)
	return nil
}

// generateToken creates a crd_xxx format token
func generateToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return "crd_" + hex.EncodeToString(b)
}

// --- Methods used by the proxy ---

// GetAPIKey returns the real Anthropic API key
func (p *AnthropicPlugin) GetAPIKey() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.config == nil {
		return ""
	}
	return p.config.APIKey
}

// GetProxyPort returns the configured proxy port
func (p *AnthropicPlugin) GetProxyPort() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.config == nil {
		return 8401
	}
	return p.config.ProxyPort
}

// ValidateToken checks if a crd_xxx token is valid
func (p *AnthropicPlugin) ValidateToken(token string) (*TokenInfo, bool) {
	return p.tokens.Get(token)
}
