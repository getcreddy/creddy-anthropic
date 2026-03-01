package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/getcreddy/creddy-plugin-sdk"
)

func TestPluginInfo(t *testing.T) {
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
	if info.Description == "" {
		t.Error("expected non-empty description")
	}
}

func TestConfigSchema(t *testing.T) {
	plugin := NewPlugin()
	schema, err := plugin.ConfigSchema(context.Background())
	if err != nil {
		t.Fatalf("ConfigSchema() error: %v", err)
	}

	if len(schema) == 0 {
		t.Fatal("expected non-empty schema")
	}

	// Should have api_key field
	hasAPIKey := false
	for _, field := range schema {
		if field.Name == "api_key" {
			hasAPIKey = true
			if !field.Required {
				t.Error("api_key should be required")
			}
			if field.Type != "secret" {
				t.Errorf("api_key should be type 'secret', got %q", field.Type)
			}
		}
	}
	if !hasAPIKey {
		t.Error("expected api_key field in schema")
	}
}

func TestConfigure_MissingAPIKey(t *testing.T) {
	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), `{}`)
	if err == nil {
		t.Fatal("expected error for missing api_key")
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Errorf("expected error about api_key, got: %v", err)
	}
}

func TestConfigure_EmptyAPIKey(t *testing.T) {
	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), `{"api_key": ""}`)
	if err == nil {
		t.Fatal("expected error for empty api_key")
	}
}

func TestConfigure_InvalidJSON(t *testing.T) {
	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), `{invalid}`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestConfigure_DefaultProxyPort(t *testing.T) {
	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), `{"api_key": "sk-ant-test"}`)
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	if plugin.GetProxyPort() != 8401 {
		t.Errorf("expected default proxy port 8401, got %d", plugin.GetProxyPort())
	}
}

func TestConfigure_CustomProxyPort(t *testing.T) {
	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), `{"api_key": "sk-ant-test", "proxy_port": 9999}`)
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	if plugin.GetProxyPort() != 9999 {
		t.Errorf("expected proxy port 9999, got %d", plugin.GetProxyPort())
	}
}

func TestMatchScope(t *testing.T) {
	plugin := NewPlugin()

	tests := []struct {
		scope string
		want  bool
	}{
		{"anthropic", true},
		{"anthropic:claude", true},
		{"anthropic:messages", true},
		{"anthropic:completion", true},
		{"Anthropic", false}, // case sensitive
		{"github", false},
		{"openai", false},
		{"aws", false},
		{"", false},
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

func TestScopes(t *testing.T) {
	plugin := NewPlugin()
	scopes, err := plugin.Scopes(context.Background())
	if err != nil {
		t.Fatalf("Scopes() error: %v", err)
	}

	if len(scopes) == 0 {
		t.Fatal("expected at least one scope")
	}

	// Should have "anthropic" scope
	hasAnthropic := false
	for _, s := range scopes {
		if s.Pattern == "anthropic" {
			hasAnthropic = true
		}
	}
	if !hasAnthropic {
		t.Error("expected 'anthropic' scope pattern")
	}
}

func TestConstraints(t *testing.T) {
	plugin := NewPlugin()
	constraints, err := plugin.Constraints(context.Background())
	if err != nil {
		t.Fatalf("Constraints() error: %v", err)
	}

	if constraints.MinTTL <= 0 {
		t.Error("expected positive MinTTL")
	}
	if constraints.MaxTTL <= constraints.MinTTL {
		t.Error("expected MaxTTL > MinTTL")
	}
}

func TestValidate_NotConfigured(t *testing.T) {
	plugin := NewPlugin()
	err := plugin.Validate(context.Background())
	if err == nil {
		t.Fatal("expected error when not configured")
	}
}

func TestValidate_Configured(t *testing.T) {
	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), `{"api_key": "sk-ant-test"}`)
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	err = plugin.Validate(context.Background())
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
}

func TestGetCredential_NotConfigured(t *testing.T) {
	plugin := NewPlugin()
	_, err := plugin.GetCredential(context.Background(), &sdk.CredentialRequest{
		Scope: "anthropic",
		TTL:   10 * time.Minute,
	})
	if err == nil {
		t.Fatal("expected error when not configured")
	}
}

func TestGetCredential_TokenFormat(t *testing.T) {
	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), `{"api_key": "sk-ant-test", "proxy_port": 19401}`)
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	cred, err := plugin.GetCredential(context.Background(), &sdk.CredentialRequest{
		Scope: "anthropic",
		TTL:   10 * time.Minute,
		Agent: sdk.Agent{ID: "test", Name: "test"},
	})
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}

	// Token should start with crd_
	if !strings.HasPrefix(cred.Value, "crd_") {
		t.Errorf("expected token to start with crd_, got: %s", cred.Value)
	}

	// Token should be long enough (crd_ + 48 hex chars = 52 total)
	if len(cred.Value) < 50 {
		t.Errorf("token too short: %d chars", len(cred.Value))
	}

	// ExternalID should be set (same as Value for this plugin)
	if cred.ExternalID == "" {
		t.Error("expected ExternalID to be set")
	}

	// ExpiresAt should be set
	if cred.ExpiresAt.IsZero() {
		t.Error("expected ExpiresAt to be set")
	}
	if cred.ExpiresAt.Before(time.Now()) {
		t.Error("ExpiresAt should be in the future")
	}
}

func TestGetCredential_TTLRespected(t *testing.T) {
	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), `{"api_key": "sk-ant-test", "proxy_port": 19402}`)
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	ttl := 5 * time.Minute
	before := time.Now()

	cred, err := plugin.GetCredential(context.Background(), &sdk.CredentialRequest{
		Scope: "anthropic",
		TTL:   ttl,
		Agent: sdk.Agent{ID: "test", Name: "test"},
	})
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}

	// ExpiresAt should be approximately now + TTL
	expectedExpiry := before.Add(ttl)
	diff := cred.ExpiresAt.Sub(expectedExpiry)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("ExpiresAt off by too much: expected ~%v, got %v (diff: %v)", expectedExpiry, cred.ExpiresAt, diff)
	}
}

func TestTokenStore_AddAndGet(t *testing.T) {
	store := NewTokenStore()
	token := "crd_test123"
	info := &TokenInfo{
		AgentID:   "agent1",
		AgentName: "Test Agent",
		Scope:     "anthropic",
		ExpiresAt: time.Now().Add(10 * time.Minute),
		CreatedAt: time.Now(),
	}

	store.Add(token, info)

	got, ok := store.Get(token)
	if !ok {
		t.Fatal("expected to find token")
	}
	if got.AgentID != info.AgentID {
		t.Errorf("AgentID mismatch: got %q, want %q", got.AgentID, info.AgentID)
	}
}

func TestTokenStore_GetExpired(t *testing.T) {
	store := NewTokenStore()
	token := "crd_expired"
	info := &TokenInfo{
		AgentID:   "agent1",
		ExpiresAt: time.Now().Add(-1 * time.Minute), // Already expired
		CreatedAt: time.Now().Add(-2 * time.Minute),
	}

	store.Add(token, info)

	_, ok := store.Get(token)
	if ok {
		t.Error("expected expired token to not be found")
	}
}

func TestTokenStore_Remove(t *testing.T) {
	store := NewTokenStore()
	token := "crd_remove"
	info := &TokenInfo{
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}

	store.Add(token, info)
	store.Remove(token)

	_, ok := store.Get(token)
	if ok {
		t.Error("expected removed token to not be found")
	}
}

func TestTokenStore_Cleanup(t *testing.T) {
	store := NewTokenStore()

	// Add some expired tokens
	for i := 0; i < 5; i++ {
		store.Add(fmt.Sprintf("crd_expired_%d", i), &TokenInfo{
			ExpiresAt: time.Now().Add(-1 * time.Minute),
		})
	}

	// Add some valid tokens
	for i := 0; i < 3; i++ {
		store.Add(fmt.Sprintf("crd_valid_%d", i), &TokenInfo{
			ExpiresAt: time.Now().Add(10 * time.Minute),
		})
	}

	removed := store.Cleanup()
	if removed != 5 {
		t.Errorf("expected 5 removed, got %d", removed)
	}

	// Valid tokens should still be there
	for i := 0; i < 3; i++ {
		_, ok := store.Get(fmt.Sprintf("crd_valid_%d", i))
		if !ok {
			t.Errorf("valid token %d should still exist", i)
		}
	}
}

func TestTokenStore_Concurrent(t *testing.T) {
	store := NewTokenStore()
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			store.Add(fmt.Sprintf("crd_%d", id), &TokenInfo{
				AgentID:   fmt.Sprintf("agent_%d", id),
				ExpiresAt: time.Now().Add(10 * time.Minute),
			})
		}(i)
	}

	wg.Wait()

	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, _ = store.Get(fmt.Sprintf("crd_%d", id))
		}(i)
	}

	wg.Wait()

	// Should have all 100 tokens
	count := 0
	for i := 0; i < 100; i++ {
		if _, ok := store.Get(fmt.Sprintf("crd_%d", i)); ok {
			count++
		}
	}
	if count != 100 {
		t.Errorf("expected 100 tokens, got %d", count)
	}
}

func TestRevokeCredential_Idempotent(t *testing.T) {
	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), `{"api_key": "sk-ant-test", "proxy_port": 19403}`)
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	// Revoking nonexistent token should not error
	err = plugin.RevokeCredential(context.Background(), "crd_nonexistent")
	if err != nil {
		t.Fatalf("RevokeCredential() should be idempotent, got: %v", err)
	}

	// Create and revoke twice
	cred, err := plugin.GetCredential(context.Background(), &sdk.CredentialRequest{
		Scope: "anthropic",
		TTL:   10 * time.Minute,
		Agent: sdk.Agent{ID: "test", Name: "test"},
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
}

func TestGetAPIKey(t *testing.T) {
	plugin := NewPlugin()

	// Before configure
	if plugin.GetAPIKey() != "" {
		t.Error("expected empty API key before configure")
	}

	err := plugin.Configure(context.Background(), `{"api_key": "sk-ant-test123"}`)
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	if plugin.GetAPIKey() != "sk-ant-test123" {
		t.Errorf("expected 'sk-ant-test123', got %q", plugin.GetAPIKey())
	}
}

func TestValidateToken(t *testing.T) {
	plugin := NewPlugin()
	err := plugin.Configure(context.Background(), `{"api_key": "sk-ant-test", "proxy_port": 19404}`)
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	// Create a token
	cred, err := plugin.GetCredential(context.Background(), &sdk.CredentialRequest{
		Scope: "anthropic",
		TTL:   10 * time.Minute,
		Agent: sdk.Agent{ID: "test", Name: "Test Agent"},
	})
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}

	// Validate it
	info, ok := plugin.ValidateToken(cred.Value)
	if !ok {
		t.Fatal("expected token to be valid")
	}
	if info.AgentName != "Test Agent" {
		t.Errorf("expected AgentName 'Test Agent', got %q", info.AgentName)
	}

	// Invalid token
	_, ok = plugin.ValidateToken("crd_invalid")
	if ok {
		t.Error("expected invalid token to fail validation")
	}
}

func TestConfig_JSON(t *testing.T) {
	cfg := &AnthropicConfig{
		APIKey:    "sk-ant-secret",
		ProxyPort: 8401,
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded AnthropicConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if decoded.APIKey != cfg.APIKey {
		t.Errorf("APIKey mismatch")
	}
	if decoded.ProxyPort != cfg.ProxyPort {
		t.Errorf("ProxyPort mismatch")
	}
}
