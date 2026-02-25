package main

import (
	"fmt"
	"os"

	sdk "github.com/getcreddy/creddy-plugin-sdk"
)

func main() {
	// Handle CLI commands for plugin info
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "info":
			fmt.Printf("Name:              %s\n", PluginName)
			fmt.Printf("Version:           %s\n", PluginVersion)
			fmt.Printf("Description:       Anthropic API access via Creddy proxy\n")
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

		case "help", "-h", "--help":
			fmt.Println("creddy-anthropic - Anthropic plugin for Creddy")
			fmt.Println()
			fmt.Println("Commands:")
			fmt.Println("  info     Show plugin information")
			fmt.Println("  scopes   List supported scopes")
			fmt.Println("  help     Show this help")
			fmt.Println()
			fmt.Println("This plugin runs as a Creddy plugin process. Configure in Creddy with:")
			fmt.Println()
			fmt.Println("  creddy backend add anthropic --config '{")
			fmt.Println("    \"api_key\": \"sk-ant-...\",")
			fmt.Println("    \"proxy\": {")
			fmt.Println("      \"upstream_url\": \"https://api.anthropic.com\",")
			fmt.Println("      \"header_name\": \"x-api-key\"")
			fmt.Println("    }")
			fmt.Println("  }'")
			fmt.Println()
			fmt.Println("Agents configure:")
			fmt.Println("  ANTHROPIC_BASE_URL=http://creddy:8400/v1/proxy/anthropic")
			return
		}
	}

	// Run as plugin
	sdk.Serve(NewPlugin())
}
