package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	sdk "github.com/getcreddy/creddy-plugin-sdk"
)

var plugin *AnthropicPlugin

func main() {
	// CLI flags for standalone/testing
	standaloneProxy := flag.Bool("proxy", false, "Run proxy in standalone mode (for testing)")
	proxyAddr := flag.String("addr", ":8401", "Proxy listen address")
	apiKey := flag.String("api-key", "", "Anthropic API key (or ANTHROPIC_API_KEY env)")
	flag.Parse()

	// Create the shared plugin instance
	plugin = NewAnthropicPlugin()

	if *standaloneProxy {
		runStandaloneProxy(*proxyAddr, *apiKey)
		return
	}

	// Check if running as Creddy plugin (gRPC mode)
	if os.Getenv("CREDDY_PLUGIN") == "creddy" {
		runAsCreddyPlugin()
		return
	}

	// Default: standalone CLI for testing
	sdk.ServeWithStandalone(plugin, nil)
}

// runAsCreddyPlugin runs the plugin with integrated proxy
func runAsCreddyPlugin() {
	// Get proxy port from env (Creddy can set this)
	proxyPort := os.Getenv("CREDDY_ANTHROPIC_PROXY_PORT")
	if proxyPort == "" {
		proxyPort = "8401"
	}

	// Start proxy server in background
	// It will wait for Configure() to be called before it can validate tokens
	go func() {
		proxy := NewProxy(plugin, ":"+proxyPort)
		log.Printf("[anthropic] Starting proxy on :%s", proxyPort)
		if err := proxy.Start(); err != nil {
			log.Printf("[anthropic] Proxy error: %v", err)
		}
	}()

	// Give proxy a moment to start
	time.Sleep(100 * time.Millisecond)

	// Run the SDK server (handles gRPC calls from Creddy)
	sdk.Serve(plugin)
}

// runStandaloneProxy runs just the proxy for testing
func runStandaloneProxy(addr string, apiKey string) {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		log.Fatal("API key required: --api-key or ANTHROPIC_API_KEY env")
	}

	configJSON, _ := json.Marshal(map[string]interface{}{
		"api_key": apiKey,
	})

	if err := plugin.Configure(nil, string(configJSON)); err != nil {
		log.Fatalf("Configure failed: %v", err)
	}

	// Handle shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutting down...")
		os.Exit(0)
	}()

	log.Printf("[anthropic] Standalone proxy on %s", addr)
	log.Printf("[anthropic] WARNING: Standalone mode - tokens won't persist across restarts")

	proxy := NewProxy(plugin, addr)
	if err := proxy.Start(); err != nil {
		log.Fatalf("Proxy failed: %v", err)
	}
}
