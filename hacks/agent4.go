package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/moresearch/swechainagent/tools"
	"github.com/ollama/ollama/api"
)

func main() {
	ctx := context.Background()

	ollamaRawUrl := "http://127.0.0.1:11434"

	var chatLLM string
	if chatLLM = os.Getenv("CHAT_LLM"); chatLLM == "" {
		chatLLM = "llama3.2:3b"
	}

	var toolsLLM string
	if toolsLLM = os.Getenv("TOOLS_LLM"); toolsLLM == "" {
		toolsLLM = "llama3.2:3b-instruct-fp16"
	}

	url, _ := url.Parse(ollamaRawUrl)
	fmt.Println("Parsed URL:", url)

	ollamaClient := api.NewClient(url, http.DefaultClient)

	mcpClient, err := client.NewStdioMCPClient(
		"./swechain-mcp-server",
		[]string{}, // Empty ENV
	)
	if err != nil {
		log.Fatalf("😡 Failed to create client: %v", err)
	}
	defer mcpClient.Close()

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Initialize the client
	fmt.Println("🚀 Initializing mcp client...")
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "swechainagent",
		Version: "1.0.0",
	}

	initResult, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		log.Fatalf("Failed to initialize: %v", err)
	}
	fmt.Printf(
		"🎉 Initialized with server: %s %s\n\n",
		initResult.ServerInfo.Name,
		initResult.ServerInfo.Version,
	)

	// List Tools
	fmt.Println("🛠️ Available tools...")
	toolsRequest := mcp.ListToolsRequest{}
	mcpTools, err := mcpClient.ListTools(ctx, toolsRequest)
	if err != nil {
		log.Fatalf("😡 Failed to list tools: %v", err)
	}

	for _, tool := range mcpTools.Tools {
		fmt.Printf("- %s: %s\n", tool.Name, tool.Description)
		fmt.Println("Arguments:", tool.InputSchema.Properties)
	}
	fmt.Println()

	// Convert tools to Ollama format using our tools package
	ollamaTools := tools.ConvertToOllamaTools(mcpTools.Tools)

	// Display the Ollama format
	fmt.Println("🦙 Ollama tools:")
	fmt.Println(ollamaTools)

	// Have a "tool chat" with Ollama 🦙
	// Prompt construction
	systemMCPInstructions := `You are a useful AI agent. 
	Your job is to understand the user prompt ans decide if you need to use a tool to run external commands.
	Ignore all things not related to the usage of a tool.
	`

	// Get command line arguments
	args := os.Args[1:]
	userInstructions := "1. check the balance of both alice and bob. 2. send 100 tokens from alice to bob's address: cosmos1fgs3u5hvkrh50y7nphrqyjur27jaahh4h3c86w , 3. check the balances again"

	// If command line arguments are provided, use them
	if len(args) >= 2 {
		userInstructions = fmt.Sprintf("1. check the balance of both alice and bob. 2. send 100 tokens from alice to %s's address: %s , 3. check the balances again", args[0], args[1])
	}

	messages := []api.Message{
		{Role: "system", Content: systemMCPInstructions},
		{Role: "user", Content: userInstructions},
	}

	var FALSE = false
	req := &api.ChatRequest{
		Model:    toolsLLM,
		Messages: messages,
		Options: map[string]interface{}{
			"temperature":   0.0,
			"repeat_last_n": 2,
		},
		Tools:  ollamaTools,
		Stream: &FALSE,
	}

	contentForThePrompt := ""

	err = ollamaClient.Chat(ctx, req, func(resp api.ChatResponse) error {
		// Ollma found tool(s) to call
		for _, toolCall := range resp.Message.ToolCalls {
			fmt.Println("🦙🛠️", toolCall.Function.Name, toolCall.Function.Arguments)
			// Call the mcp server
			fmt.Println("📣 calling", toolCall.Function.Name)
			fetchRequest := mcp.CallToolRequest{
				Request: mcp.Request{
					Method: "tools/call",
				},
			}
			fetchRequest.Params.Name = toolCall.Function.Name
			fetchRequest.Params.Arguments = toolCall.Function.Arguments

			result, err := mcpClient.CallTool(ctx, fetchRequest)
			if err != nil {
				log.Fatalf("😡 Failed to call the tool: %v", err)
			}
			// display the text content of result
			fmt.Println("🌍 content of the result:")
			contentForThePrompt += result.Content[0].(map[string]interface{})["text"].(string)
			fmt.Println(contentForThePrompt)
		}

		return nil
	})

	fmt.Println("⏳ Generating the completion...")

	// Have a "chat" with Ollama 🦙
	// Prompt construction
	systemChatInstructions := `You are a useful AI agent. your job is to answer the user prompt.
	If you detect that the user prompt is related to a tool, ignore this part and focus on the other parts.
	`

	messages = []api.Message{
		{Role: "system", Content: systemChatInstructions},
		{Role: "user", Content: userInstructions},
		{Role: "user", Content: contentForThePrompt},
	}

	var TRUE = true
	reqChat := &api.ChatRequest{
		Model:    chatLLM,
		Messages: messages,
		Options: map[string]interface{}{
			"temperature":   0.0,
			"repeat_last_n": -1,
		},
		Stream: &TRUE,
	}

	answer := ""
	errChat := ollamaClient.Chat(ctx, reqChat, func(resp api.ChatResponse) error {
		answer += resp.Message.Content
		fmt.Print(resp.Message.Content)
		return nil
	})

	if errChat != nil {
		log.Fatalln("😡", errChat)
	}
}
