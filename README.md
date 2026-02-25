# creddy-anthropic

Anthropic plugin for [Creddy](https://github.com/getcreddy/creddy) - provides secure Anthropic API access via plugin-managed proxy.

## Architecture

```
Agent → Creddy (/v1/proxy/anthropic/*) → Plugin Proxy (:8401) → api.anthropic.com
              │                               │
              └─ validates agent             └─ validates crd_xxx token
                 routes to plugin               swaps for real API key
```

**Why this design:**
- Plugin owns the upstream relationship with Anthropic
- When Anthropic changes their API, update the plugin (not Creddy)
- Creddy handles auth and routing, plugin handles the actual API

## How it Works

1. Agent calls `creddy get anthropic` → gets a `crd_xxx` token (short-lived)
2. Agent sets `ANTHROPIC_BASE_URL` to point at Creddy's proxy
3. Agent uses `crd_xxx` as their API key
4. Creddy routes to the plugin's proxy
5. Plugin validates `crd_xxx`, swaps for real `sk-ant-xxx`, forwards to Anthropic

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
  "proxy_port": 8401
}'
```

The plugin automatically starts its proxy on the configured port when loaded.

## Agent Setup

1. Create an agent with anthropic scope:
```bash
creddy agent add myagent --scopes "anthropic"
```

2. Agent gets a short-lived token:
```bash
export ANTHROPIC_API_KEY=$(creddy get anthropic)
```

3. Agent configures SDK to use Creddy's proxy:
```bash
export ANTHROPIC_BASE_URL=http://creddy-host:8400/v1/proxy/anthropic
```

## Token Flow

```
┌─────────┐                    ┌─────────┐                    ┌─────────────────┐
│  Agent  │                    │ Creddy  │                    │ creddy-anthropic│
└────┬────┘                    └────┬────┘                    └────────┬────────┘
     │                              │                                  │
     │ creddy get anthropic         │                                  │
     │ (with agent token)           │                                  │
     │─────────────────────────────>│                                  │
     │                              │ GetCredential()                  │
     │                              │─────────────────────────────────>│
     │                              │                                  │
     │                              │ crd_xxx token                    │
     │                              │<─────────────────────────────────│
     │                              │                                  │
     │ crd_xxx                      │                                  │
     │<─────────────────────────────│                                  │
     │                              │                                  │
     │ POST /v1/messages            │                                  │
     │ x-api-key: crd_xxx           │                                  │
     │─────────────────────────────>│                                  │
     │                              │ proxy to :8401                   │
     │                              │─────────────────────────────────>│
     │                              │                                  │ validate crd_xxx
     │                              │                                  │ swap for sk-ant-xxx
     │                              │                                  │ forward to Anthropic
     │                              │                                  │
     │ response                     │                      response    │
     │<─────────────────────────────│<─────────────────────────────────│
```

## Supported Scopes

| Scope | Description |
|-------|-------------|
| `anthropic` | Full Anthropic API access |
| `anthropic:claude` | Access to Claude models |

## Standalone Proxy Mode

For testing or standalone deployment:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
export PROXY_PORT=8401
./creddy-anthropic proxy
```

## Security

- Real API key (`sk-ant-xxx`) never leaves the plugin
- Agents only receive short-lived `crd_xxx` tokens
- Tokens are validated on every request
- Full audit trail in Creddy for credential issuance

## Requirements

- Creddy v0.4.0 or later (with proxy routing support)
- Anthropic API key from [console.anthropic.com](https://console.anthropic.com/)
