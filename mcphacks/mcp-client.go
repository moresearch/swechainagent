package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func main() {

	// Create a new MCP client with the server data connection
	//mcpClient, err := client.NewStdioMCPClient(
	//	"docker",
	//	[]string{}, // environment variables
	//	"run",
	//	"--rm",
	//	"-i",
	//	"mcp-curl",
	//)

	mcpClient, err := client.NewStdioMCPClient(
		"./mcp-swechain",
		[]string{}, // environment variables
	)

	if err != nil {
		log.Fatalf("😡 Failed to create client: %v", err)
	}
	defer mcpClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Define and Initialize the MCP request
	fmt.Println("🚀 Initializing mcp client...")
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "mcp-swechain client 🌍",
		Version: "1.0.0",
	}

	// Initialize the MCP client and connect to the server
	initResult, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		log.Fatalf("Failed to initialize: %v", err)
	}
	fmt.Printf(
		"🎉 Initialized with server: %s %s\n\n",
		initResult.ServerInfo.Name,
		initResult.ServerInfo.Version,
	)

	// Get the list of tools from the server
	fmt.Println("🛠️ Available tools...")
	toolsRequest := mcp.ListToolsRequest{}
	tools, err := mcpClient.ListTools(ctx, toolsRequest)
	if err != nil {
		log.Fatalf("😡 Failed to list tools: %v", err)
	}
	// Display the list of tools
	for _, tool := range tools.Tools {
		fmt.Printf("- %s: %s\n", tool.Name, tool.Description)
		fmt.Println("Arguments:", tool.InputSchema.Properties)
	}
	fmt.Println()

	// Prepare the call of the tool "use_curl"
	// to fetch the content of a web page
	fmt.Println("📣 will be calling use_curl")
	fetchRequest := mcp.CallToolRequest{
		Request: mcp.Request{
			Method: "tools/call",
		},
	}
	fetchRequest.Params.Name = "balance"
	fetchRequest.Params.Arguments = map[string]interface{}{
		//"url": "https://raw.githubusercontent.com/docker-sa/01-build-image/refs/heads/main/main.go",
		"account": "alice",
	}

	// Call the tool
	result, err := mcpClient.CallTool(ctx, fetchRequest)
	if err != nil {
		log.Fatalf("😡 Failed to call the tool: %v", err)
	}
	// display the text content of result
	fmt.Println("🌍 content of the page:")
	fmt.Println(result.Content[0].(map[string]interface{})["text"])
}
