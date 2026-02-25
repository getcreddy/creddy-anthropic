# creddy-anthropic

Anthropic API proxy with ephemeral token support.

## Why a Proxy?

Unlike OpenAI, Anthropic doesn't offer a self-service Admin API for key management. This plugin uses a **proxy approach**:

1. Proxy issues short-lived tokens (not real Anthropic keys)
2. Agents use tokens with the proxy endpoint
3. Proxy validates tokens and forwards requests with the real key
4. Agents never see the real Anthropic API key

## Quick Start

### 1. Run the Proxy

```bash
# With flag
./creddy-anthropic --proxy --api-key "sk-ant-..."

# Or with environment variable
export ANTHROPIC_API_KEY=sk-ant-...
./creddy-anthropic --proxy
```

### 2. Get a Token

```bash
curl -X POST http://localhost:8080/v1/tokens \
  -d '{"ttl":"1h","agent_name":"my-agent"}'

# Response:
# {"token":"crd_xxx...","expires_at":"2026-02-25T13:00:00Z","ttl":"1h0m0s"}
```

### 3. Use the Token

```bash
export ANTHROPIC_API_KEY=crd_xxx...
export ANTHROPIC_BASE_URL=http://localhost:8080
```

Now use the Anthropic SDK or API as normal - requests are proxied transparently.

## How It Works

```
┌─────────────┐                        ┌─────────────┐
│   Agent     │ ── POST /v1/tokens ──▶ │   Proxy     │
│             │ ◀── crd_xxx token ──── │             │
└──────┬──────┘                        └──────┬──────┘
       │                                      │
       │ API request with crd_xxx             │
       ▼                                      ▼
┌─────────────┐                        ┌─────────────┐
│   Proxy     │ ── validates token     │   Anthropic │
│             │ ── swaps for real ───▶ │     API     │
│             │    API key             │             │
└─────────────┘                        └─────────────┘
```

## Python Example

```python
import os
import requests
from anthropic import Anthropic

# Get a token from the proxy
resp = requests.post("http://proxy:8080/v1/tokens", 
    json={"ttl": "1h", "agent_name": "my-script"})
token = resp.json()["token"]

# Configure the SDK
os.environ["ANTHROPIC_API_KEY"] = token
os.environ["ANTHROPIC_BASE_URL"] = "http://proxy:8080"

# Use normally
client = Anthropic()
response = client.messages.create(
    model="claude-sonnet-4-20250514",
    max_tokens=1024,
    messages=[{"role": "user", "content": "Hello!"}]
)
```

## Proxy Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/tokens` | POST | Issue a new token |
| `/health` | GET | Health check |
| `/v1/*` | * | Proxied to Anthropic API |

### Token Request

```json
{
  "ttl": "1h",           // Token lifetime (max 24h)
  "agent_name": "my-app" // Optional identifier for logging
}
```

### Token Response

```json
{
  "token": "crd_xxx...",
  "expires_at": "2026-02-25T13:00:00Z",
  "ttl": "1h0m0s"
}
```

## Security Benefits

- **Real key never exposed** — only the proxy has it
- **Short-lived tokens** — limit blast radius if leaked  
- **Immediate revocation** — restart proxy to invalidate all tokens
- **Audit trail** — proxy logs all requests with agent name

## CLI Options

```
./creddy-anthropic --proxy [options]

Options:
  --addr string     Listen address (default ":8080")
  --api-key string  Anthropic API key (or ANTHROPIC_API_KEY env)
```

## Production Deployment

### Docker

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o creddy-anthropic .

FROM alpine:latest
COPY --from=builder /app/creddy-anthropic /usr/local/bin/
ENTRYPOINT ["creddy-anthropic", "--proxy"]
```

```bash
docker run -d \
  -e ANTHROPIC_API_KEY=sk-ant-... \
  -p 8080:8080 \
  creddy-anthropic
```

### systemd

```ini
[Unit]
Description=Creddy Anthropic Proxy
After=network.target

[Service]
Type=simple
Environment=ANTHROPIC_API_KEY=sk-ant-...
ExecStart=/usr/local/bin/creddy-anthropic --proxy --addr :8080
Restart=always

[Install]
WantedBy=multi-user.target
```

## Building

```bash
# Build
make build

# Run tests  
make test

# Build for all platforms
make build-all
```

## Roadmap

- [ ] Creddy server integration (validate tokens via Creddy API)
- [ ] Redis-backed token store (for multi-instance deployments)
- [ ] Per-token rate limiting
- [ ] Usage tracking per agent

## License

Apache 2.0
