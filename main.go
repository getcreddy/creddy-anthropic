package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	sdk "github.com/getcreddy/creddy-plugin-sdk"
)

func main() {
	// CLI flags
	proxyMode := flag.Bool("proxy", false, "Run in proxy mode")
	proxyAddr := flag.String("addr", ":8080", "Proxy listen address")
	apiKey := flag.String("api-key", "", "Anthropic API key (can also use ANTHROPIC_API_KEY env var)")
	flag.Parse()

	if *proxyMode {
		runProxy(*proxyAddr, *apiKey)
	} else {
		// Plugin mode - serve via Creddy plugin SDK
		sdk.ServeWithStandalone(&AnthropicPlugin{}, nil)
	}
}

func runProxy(addr string, apiKey string) {
	// Get API key from flag or environment
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		log.Fatal("API key required: use --api-key flag or ANTHROPIC_API_KEY env var")
	}

	// Create and configure the plugin
	plugin := &AnthropicPlugin{}
	
	configJSON, _ := json.Marshal(map[string]string{
		"api_key": apiKey,
	})
	
	if err := plugin.Configure(nil, string(configJSON)); err != nil {
		log.Fatalf("Failed to configure plugin: %v", err)
	}

	// Create the proxy
	proxy := NewProxy(plugin, addr)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutting down proxy...")
		proxy.Stop()
		os.Exit(0)
	}()

	// Start the proxy
	fmt.Printf(`
Anthropic Proxy Server
======================
Listening on: %s
Upstream:     https://api.anthropic.com

Usage:
  1. Get a token:
     curl -X POST http://localhost%s/v1/tokens -d '{"ttl":"1h","agent_name":"my-agent"}'

  2. Configure your agent:
     export ANTHROPIC_BASE_URL=http://localhost%s
     export ANTHROPIC_API_KEY=<token-from-step-1>

  3. Make API calls as normal - they'll be proxied to Anthropic

Endpoints:
  POST /v1/tokens     - Issue a new token
  GET  /health        - Health check
  *    /v1/*          - Proxied to Anthropic API

Press Ctrl+C to stop.
`, addr, addr, addr)

	if err := proxy.Start(); err != nil {
		log.Fatalf("Proxy failed: %v", err)
	}
}
