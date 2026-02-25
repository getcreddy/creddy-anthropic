package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
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
}

// AnthropicConfig contains the plugin configuration
type AnthropicConfig struct {
	APIKey string `json:"api_key"` // Real Anthropic API key
}

func NewPlugin() *AnthropicPlugin {
	return &AnthropicPlugin{}
}

// Info returns plugin metadata
func (p *AnthropicPlugin) Info(ctx context.Context) (*sdk.PluginInfo, error) {
	return &sdk.PluginInfo{
		Name:             PluginName,
		Version:          PluginVersion,
		Description:      "Anthropic API access via Creddy proxy",
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

	p.config = &cfg
	return nil
}

// Validate tests the configuration by making a test API call
func (p *AnthropicPlugin) Validate(ctx context.Context) error {
	if p.config == nil {
		return errors.New("plugin not configured")
	}

	// Make a simple API call to validate the key
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.anthropic.com/v1/models", nil)
	if err != nil {
		return err
	}

	req.Header.Set("x-api-key", p.config.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return errors.New("invalid API key")
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return errors.New("API error: " + string(body))
	}

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
	// Anthropic doesn't have programmatic key management,
	// so we rely on Creddy's credential caching. Longer TTLs
	// are fine since the same key is reused.
	return &sdk.Constraints{
		MinTTL:      1 * time.Minute,
		MaxTTL:      24 * time.Hour,
		Description: "Anthropic uses a shared API key via Creddy proxy",
	}, nil
}

// GetCredential returns the API key for Anthropic
// This is called by Creddy when proxying requests
func (p *AnthropicPlugin) GetCredential(ctx context.Context, req *sdk.CredentialRequest) (*sdk.Credential, error) {
	if p.config == nil {
		return nil, errors.New("plugin not configured")
	}

	// For Anthropic, we just return the API key
	// Creddy handles proxying and credential injection
	return &sdk.Credential{
		Value:      p.config.APIKey,
		ExpiresAt:  time.Now().Add(req.TTL),
		ExternalID: "", // Not revocable
	}, nil
}

// RevokeCredential is a no-op for Anthropic (no key management API)
func (p *AnthropicPlugin) RevokeCredential(ctx context.Context, credential string) error {
	// Anthropic doesn't support key revocation via API
	// The shared key continues to be valid
	return nil
}
