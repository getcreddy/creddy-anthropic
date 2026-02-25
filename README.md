# creddy-anthropic

Anthropic plugin for [Creddy](https://github.com/getcreddy/creddy) - provides secure Anthropic API access via Creddy's proxy.

## How it Works

Unlike OpenAI which has an Admin API for creating ephemeral keys, Anthropic doesn't offer programmatic key management. This plugin uses Creddy's proxy mode:

1. You configure the plugin with your Anthropic API key
2. Agents authenticate to Creddy with their agent token
3. Creddy validates the agent and their scopes
4. Creddy proxies requests to Anthropic, injecting the real API key
5. Agents never see the actual API key

```
Agent → Creddy Proxy (/v1/proxy/anthropic/v1/messages) → Anthropic API
              │
              └─ Agent authenticated via Creddy token
                 Real API key injected by proxy
```

## Installation

```bash
# Download the plugin
creddy plugin install anthropic

# Or manually:
curl -L https://plugins.creddy.dev/anthropic/latest/creddy-anthropic-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m) \
  -o ~/.creddy/plugins/creddy-anthropic
chmod +x ~/.creddy/plugins/creddy-anthropic
```

## Configuration

Add the Anthropic backend to Creddy:

```bash
creddy backend add anthropic --config '{
  "api_key": "sk-ant-api03-...",
  "proxy": {
    "upstream_url": "https://api.anthropic.com",
    "header_name": "x-api-key"
  }
}'
```

## Agent Setup

Agents need the `anthropic` scope:

```bash
# Create or update an agent with anthropic scope
creddy agent add myagent --scopes "anthropic"
```

Agents configure their environment to use Creddy's proxy:

```bash
export ANTHROPIC_BASE_URL=http://creddy:8400/v1/proxy/anthropic
export ANTHROPIC_API_KEY=$CREDDY_TOKEN  # Their Creddy agent token
```

The Anthropic SDK will then route all requests through Creddy.

## Supported Scopes

| Scope | Description |
|-------|-------------|
| `anthropic` | Full Anthropic API access |
| `anthropic:claude` | Access to Claude models |

## Development

```bash
# Build
go build -o creddy-anthropic .

# Test info command
./creddy-anthropic info

# Test scopes command
./creddy-anthropic scopes
```

## Security Notes

- The real Anthropic API key is stored in Creddy's backend config
- Agents authenticate with their Creddy token, not the Anthropic key
- All requests are proxied through Creddy with full audit logging
- Token TTL controls how long credentials are cached (default 10 min)

## Requirements

- Creddy v0.4.0 or later (with proxy support)
- Anthropic API key from [console.anthropic.com](https://console.anthropic.com/)
