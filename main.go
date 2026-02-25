package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	sdk "github.com/getcreddy/creddy-plugin-sdk"
)

func main() {
	// Handle CLI commands
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "info":
			fmt.Printf("Name:              %s\n", PluginName)
			fmt.Printf("Version:           %s\n", PluginVersion)
			fmt.Printf("Description:       Anthropic API access via plugin proxy\n")
			fmt.Printf("Min Creddy Version: 0.4.0\n")
			return

		case "scopes":
			fmt.Println("Pattern: anthropic")
			fmt.Println("  Description: Full access to the Anthropic API")
			fmt.Println("  Examples:")
			fmt.Println("    - anthropic")
			fmt.Println()
			fmt.Println("Pattern: anthropic:claude")
			fmt.Println("  Description: Access to Claude models")
			fmt.Println("  Examples:")
			fmt.Println("    - anthropic:claude")
			return

		case "proxy":
			// Run standalone proxy mode (for testing or standalone deployment)
			runProxyMode()
			return

		case "help", "-h", "--help":
			printHelp()
			return
		}
	}

	// Default: run as Creddy plugin
	sdk.Serve(NewPlugin())
}

func runProxyMode() {
	// Get config from environment
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		log.Fatal("ANTHROPIC_API_KEY environment variable required")
	}

	port := 8401
	if p := os.Getenv("PROXY_PORT"); p != "" {
		fmt.Sscanf(p, "%d", &port)
	}

	// Create and configure plugin
	plugin := NewPlugin()
	configJSON := fmt.Sprintf(`{"api_key": "%s", "proxy_port": %d}`, apiKey, port)
	if err := plugin.Configure(context.Background(), configJSON); err != nil {
		log.Fatalf("Failed to configure: %v", err)
	}

	// Start proxy
	proxy := NewProxyServer(plugin)

	// Handle shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("Shutting down...")
		proxy.Stop(context.Background())
	}()

	if err := proxy.Start(port); err != nil {
		log.Fatalf("Proxy server error: %v", err)
	}
}

func printHelp() {
	fmt.Println("creddy-anthropic - Anthropic plugin for Creddy")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  info     Show plugin information")
	fmt.Println("  scopes   List supported scopes")
	fmt.Println("  proxy    Run standalone proxy server (for testing)")
	fmt.Println("  help     Show this help")
	fmt.Println()
	fmt.Println("This plugin runs as a Creddy plugin process and provides its own proxy.")
	fmt.Println()
	fmt.Println("Setup:")
	fmt.Println("  1. Add backend to Creddy:")
	fmt.Println("     creddy backend add anthropic --config '{")
	fmt.Println("       \"api_key\": \"sk-ant-...\",")
	fmt.Println("       \"proxy_port\": 8401")
	fmt.Println("     }'")
	fmt.Println()
	fmt.Println("  2. Agent gets a token:")
	fmt.Println("     creddy get anthropic")
	fmt.Println()
	fmt.Println("  3. Agent configures SDK:")
	fmt.Println("     ANTHROPIC_BASE_URL=http://localhost:8401")
	fmt.Println("     ANTHROPIC_API_KEY=crd_xxx  # token from step 2")
}
