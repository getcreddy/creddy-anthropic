package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	sdk "github.com/getcreddy/creddy-plugin-sdk"
)

const (
	PluginName    = "anthropic"
	PluginVersion = "0.1.0"
)

// AnthropicPlugin implements the Creddy Plugin interface for Anthropic
type AnthropicPlugin struct {
	config *AnthropicConfig
}

// AnthropicConfig contains the plugin configuration
type AnthropicConfig struct {
	AdminKey string `json:"admin_key"` // Admin API key for managing keys
}

// anthropicAPIKey represents an API key returned by the Admin API
type anthropicAPIKey struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Key       string    `json:"key"` // Only returned on creation
	CreatedAt time.Time `json:"created_at"`
}

func (p *AnthropicPlugin) Info(ctx context.Context) (*sdk.PluginInfo, error) {
	return &sdk.PluginInfo{
		Name:             PluginName,
		Version:          PluginVersion,
		Description:      "Ephemeral Anthropic API keys via Admin API (Scale/Enterprise plans)",
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
			Name:        "admin_key",
			Type:        "secret",
			Description: "Anthropic Admin API key (requires Scale/Enterprise plan)",
			Required:    true,
		},
	}, nil
}

func (p *AnthropicPlugin) Constraints(ctx context.Context) (*sdk.Constraints, error) {
	// Anthropic API keys don't have inherent TTL limits - Creddy manages expiration via revocation
	return nil, nil
}

func (p *AnthropicPlugin) Configure(ctx context.Context, configJSON string) error {
	var config AnthropicConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}

	if config.AdminKey == "" {
		return fmt.Errorf("admin_key is required")
	}

	p.config = &config
	return nil
}

func (p *AnthropicPlugin) Validate(ctx context.Context) error {
	if p.config == nil {
		return fmt.Errorf("plugin not configured")
	}

	// Test the admin key by listing existing keys
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.anthropic.com/v1/api_keys", nil)
	if err != nil {
		return err
	}

	req.Header.Set("x-api-key", p.config.AdminKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to validate admin key: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("invalid admin key")
	}
	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("admin key does not have permission to manage API keys (requires Scale/Enterprise plan)")
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

	// Create a new API key with a unique name
	name := fmt.Sprintf("creddy-%s-%d", req.Agent.Name, time.Now().UnixNano())

	apiKey, err := p.createAPIKey(ctx, name)
	if err != nil {
		return nil, err
	}

	// Anthropic keys don't have inherent expiry - Creddy manages TTL
	// The key ID is stored as ExternalID for revocation
	return &sdk.Credential{
		Value:      apiKey.Key,
		ExternalID: apiKey.ID,
		ExpiresAt:  time.Now().Add(req.TTL),
		Metadata: map[string]string{
			"key_id":   apiKey.ID,
			"key_name": name,
		},
	}, nil
}

func (p *AnthropicPlugin) RevokeCredential(ctx context.Context, externalID string) error {
	if p.config == nil {
		return fmt.Errorf("plugin not configured")
	}

	return p.deleteAPIKey(ctx, externalID)
}

func (p *AnthropicPlugin) MatchScope(ctx context.Context, scope string) (bool, error) {
	// Match "anthropic" or "anthropic:*"
	return scope == "anthropic" || len(scope) > 10 && scope[:10] == "anthropic:", nil
}

// --- Anthropic Admin API helpers ---

func (p *AnthropicPlugin) createAPIKey(ctx context.Context, name string) (*anthropicAPIKey, error) {
	reqBody, _ := json.Marshal(map[string]interface{}{
		"name": name,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/api_keys", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("x-api-key", p.config.AdminKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create API key: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("anthropic API error (%d): %s", resp.StatusCode, string(body))
	}

	var key anthropicAPIKey
	if err := json.Unmarshal(body, &key); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &key, nil
}

func (p *AnthropicPlugin) deleteAPIKey(ctx context.Context, keyID string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", "https://api.anthropic.com/v1/api_keys/"+keyID, nil)
	if err != nil {
		return err
	}

	req.Header.Set("x-api-key", p.config.AdminKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete API key: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("anthropic API error (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}
