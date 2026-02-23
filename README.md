# creddy-anthropic

Anthropic plugin for [Creddy](https://github.com/getcreddy/creddy) — ephemeral API key management for AI agents.

## Overview

This plugin creates ephemeral Anthropic API keys via the [Admin API](https://docs.anthropic.com/en/api/admin-api), allowing AI agents to access Claude models with short-lived, traceable credentials.

**Requirements:** Anthropic Scale or Enterprise plan (Admin API access required)

## Installation

```bash
creddy plugin install anthropic
```

Or build from source:

```bash
make install
```

## Configuration

Add to your Creddy server config:

```yaml
integrations:
  anthropic:
    plugin: creddy-anthropic
    config:
      admin_key: "sk-admin-..."  # Your Anthropic Admin API key
```

### Getting an Admin Key

1. Log in to the [Anthropic Console](https://console.anthropic.com/)
2. Navigate to **Settings** → **Admin API**
3. Create a new admin key with "Manage API Keys" permission
4. Copy the key (it won't be shown again)

## Scopes

| Scope | Description |
|-------|-------------|
| `anthropic` | Full access to the Anthropic API |
| `anthropic:claude` | Access to Claude models |

## Usage

### Agent Enrollment

```bash
# Request access to Anthropic
creddy enroll --server https://creddy.example.com --name my-agent --can anthropic
```

### Getting Credentials

```bash
# Get an ephemeral API key
creddy get anthropic

# With specific TTL
creddy get anthropic --ttl 30m
```

### Using the Credential

```bash
export ANTHROPIC_API_KEY=$(creddy get anthropic)

# Use with Claude CLI, SDKs, etc.
```

## How It Works

1. Agent requests an Anthropic credential via `creddy get anthropic`
2. Creddy calls the Anthropic Admin API to create a new API key
3. The key is returned to the agent with a Creddy-managed TTL
4. When the TTL expires (or agent is unenrolled), Creddy deletes the key via Admin API

## Standalone Testing

Test the plugin without a Creddy server:

```bash
# Create config file
echo '{"admin_key": "sk-admin-..."}' > config.json

# Show plugin info
make info

# Validate configuration
make validate CONFIG=config.json

# Get a credential
make get CONFIG=config.json SCOPE="anthropic"
```

## Development

```bash
# Build
make build

# Build for all platforms
make build-all

# Install locally
make install

# Watch for changes and rebuild
make dev
```

## License

Apache 2.0
