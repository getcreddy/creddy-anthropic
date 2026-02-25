# creddy-anthropic

Creddy plugin for Anthropic API access via proxy.

## Why a Proxy?

Anthropic doesn't offer a self-service Admin API for key management. This plugin uses a proxy approach:

1. Creddy issues short-lived tokens via the plugin
2. Plugin stores tokens in memory and runs an HTTP proxy
3. Agents use tokens with the proxy
4. Proxy validates tokens and forwards requests with the real API key
5. Agents never see the real Anthropic key

## Architecture

```
┌─────────────┐                        ┌─────────────────────────────┐
│   Agent     │ ── creddy get ───────▶ │         Creddy              │
│             │ ◀── crd_ant_xxx ────── │                             │
└──────┬──────┘                        │  ┌───────────────────────┐  │
       │                               │  │  anthropic plugin     │  │
       │ ANTHROPIC_BASE_URL            │  │  (gRPC + HTTP proxy)  │  │
       │ = http://localhost:8401       │  │                       │  │
       │                               │  │  - GetCredential()    │  │
       │ ANTHROPIC_API_KEY             │  │  - RevokeCredential() │  │
       │ = crd_ant_xxx                 │  │  - Token store        │  │
       │                               │  │  - HTTP proxy :8401   │  │
       ▼                               │  └───────────────────────┘  │
┌─────────────┐                        └─────────────────────────────┘
│   Plugin    │                                    │
│   Proxy     │ ────── real API key ─────────────▶ │
│  :8401      │                                    │
└─────────────┘                        ┌───────────▼───────────┐
                                       │    Anthropic API      │
                                       └───────────────────────┘
```

## Server Setup

### 1. Install the Plugin

```bash
creddy plugin install anthropic
```

### 2. Configure the Backend

```bash
creddy backend add anthropic \
  --api-key "sk-ant-..."
```

The plugin will start its HTTP proxy on port 8401 (configurable).

## Agent Usage

```bash
# Get a token (includes proxy URL in metadata)
creddy get anthropic

# Configure your application
export ANTHROPIC_API_KEY=crd_ant_xxx...
export ANTHROPIC_BASE_URL=http://localhost:8401

# Use the Anthropic SDK normally
```

### Python Example

```python
from anthropic import Anthropic

# SDK uses ANTHROPIC_API_KEY and ANTHROPIC_BASE_URL from env
client = Anthropic()

response = client.messages.create(
    model="claude-sonnet-4-20250514",
    max_tokens=1024,
    messages=[{"role": "user", "content": "Hello!"}]
)
```

## How Token Lifecycle Works

1. **Agent requests token**: `creddy get anthropic --ttl 1h`
2. **Creddy calls plugin**: Plugin generates `crd_ant_xxx` token, stores it
3. **Creddy tracks TTL**: Stores the token with expiration time
4. **Agent uses token**: Proxy validates token exists in plugin's store
5. **TTL expires**: Creddy calls `RevokeCredential(crd_ant_xxx)`
6. **Plugin removes token**: Token no longer validates, requests fail

**Key point**: The plugin doesn't manage TTL internally. It just tracks which tokens exist. Creddy manages the lifecycle and tells the plugin when to revoke.

## Configuration Options

```bash
creddy backend add anthropic \
  --api-key "sk-ant-..." \
  --proxy-port 8401  # Default: 8401
```

## Proxy Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |
| `/_creddy/status` | GET | Token count and status |
| `/v1/*` | * | Proxied to Anthropic API |

## Security Benefits

- **Real key never exposed to agents** — only the plugin has it
- **Short-lived tokens** — configurable TTL (default 1h, max 24h)
- **Centralized revocation** — Creddy can revoke any token immediately
- **Audit trail** — proxy logs all requests with agent name

## Standalone Testing

For testing without Creddy:

```bash
# Run proxy standalone
./creddy-anthropic --proxy --api-key "sk-ant-..."

# Note: In standalone mode, there's no way to issue tokens!
# This is by design - tokens come from Creddy.
```

## Building

```bash
make build
make test
```

## License

Apache 2.0
