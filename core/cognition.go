package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ollama/ollama/api"
)

// Agent handles the cognition process and client interaction
type Agent struct {
	OllamaClient *api.Client
	MCPClient    client.MCPClient
	Config       *Config
}

// NewAgent creates a new Agent with initialized clients
func NewAgent(config *Config) (*Agent, error) {
	// Parse Ollama URL
	url, err := url.Parse(config.OllamaHost)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Ollama URL: %v", err)
	}
	fmt.Println("Parsed URL:", url)

	// Initialize Ollama client
	ollamaClient := api.NewClient(url, http.DefaultClient)

	// Initialize MCP client
	mcpClient, err := client.NewStdioMCPClient(
		config.MCPServerPath,
		[]string{}, // Empty ENV
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP client: %v", err)
	}

	return &Agent{
		OllamaClient: ollamaClient,
		MCPClient:    mcpClient,
		Config:       config,
	}, nil
}

// Close cleans up resources
func (a *Agent) Close() {
	if a.MCPClient != nil {
		a.MCPClient.Close()
	}
}

// Initialize sets up the MCP client and gets available tools
func (a *Agent) Initialize(ctx context.Context) ([]api.Tool, error) {
	// Initialize MCP client
	fmt.Println("🚀 Initializing mcp client...")
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    a.Config.AgentName,
		Version: a.Config.AgentVersion,
	}

	initResult, err := a.MCPClient.Initialize(ctx, initRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize: %v", err)
	}

	fmt.Printf("🎉 Initialized with server: %s %s\n\n", 
		initResult.ServerInfo.Name,
		initResult.ServerInfo.Version)

	// List Tools
	fmt.Println("🛠️ Available tools...")
	toolsRequest := mcp.ListToolsRequest{}
	tools, err := a.MCPClient.ListTools(ctx, toolsRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %v", err)
	}

	for _, tool := range tools.Tools {
		fmt.Printf("- %s: %s\n", tool.Name, tool.Description)
		fmt.Println("Arguments:", tool.InputSchema.Properties)
	}
	fmt.Println()

	// Convert tools to Ollama format
	ollamaTools := ConvertToOllamaTools(tools.Tools)

	// Display the Ollama format
	fmt.Println("🦙 Ollama tools:")
	fmt.Println(ollamaTools)

	return ollamaTools, nil
}

// ExtractTextContent safely extracts text from tool result content
func ExtractTextContent(content interface{}) (string, error) {
	// Convert to JSON first
	bytes, err := json.Marshal(content)
	if err != nil {
		return "", fmt.Errorf("failed to marshal content: %v", err)
	}

	// Then convert back to map
	var data map[string]interface{}
	if err := json.Unmarshal(bytes, &data); err != nil {
		return "", fmt.Errorf("failed to unmarshal content: %v", err)
	}

	// Extract text
	if text, ok := data["text"].(string); ok {
		return text, nil
	}

	return "", fmt.Errorf("text field not found in content")
}

// Run executes the full workflow with tool execution and chat completion
func (a *Agent) Run(ctx context.Context, userPrompt string) error {
	// Initialize and get tools
	ollamaTools, err := a.Initialize(ctx)
	if err != nil {
		return fmt.Errorf("initialization failed: %v", err)
	}

	// Tool execution phase
	systemMCPInstructions := `You are a useful AI agent. 
	Your job is to understand the user prompt ans decide if you need to use a tool to run external commands.
	Ignore all things not related to the usage of a tool.
	`

	messages := []api.Message{
		{Role: "system", Content: systemMCPInstructions},
		{Role: "user", Content: userPrompt},
	}

	var FALSE = false
	req := &api.ChatRequest{
		Model:    a.Config.ToolsModel,
		Messages: messages,
		Options: map[string]interface{}{
			"temperature":   0.0,
			"repeat_last_n": 2,
		},
		Tools:  ollamaTools,
		Stream: &FALSE,
	}

	contentForThePrompt := ""

	err = a.OllamaClient.Chat(ctx, req, func(resp api.ChatResponse) error {
		// Ollama found tool(s) to call
		for _, toolCall := range resp.Message.ToolCalls {
			fmt.Println("🦙🛠️", toolCall.Function.Name, toolCall.Function.Arguments)

			// Call the MCP server
			fmt.Println("📣 calling", toolCall.Function.Name)
			fetchRequest := mcp.CallToolRequest{
				Request: mcp.Request{
					Method: "tools/call",
				},
			}
			fetchRequest.Params.Name = toolCall.Function.Name
			fetchRequest.Params.Arguments = toolCall.Function.Arguments

			result, err := a.MCPClient.CallTool(ctx, fetchRequest)
			if err != nil {
				return fmt.Errorf("failed to call the tool: %v", err)
			}

			// Display text content of result
			fmt.Println("🌍 content of the result:")

			if len(result.Content) > 0 {
				// Extract text using the safer method
				text, err := ExtractTextContent(result.Content[0])
				if err != nil {
					return fmt.Errorf("failed to extract text content: %v", err)
				}
				contentForThePrompt += text
				fmt.Println(contentForThePrompt)
			}
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("tool execution failed: %v", err)
	}

	fmt.Println("⏳ Generating the completion...")

	// Chat completion phase
	systemChatInstructions := `You are a useful AI agent. your job is to answer the user prompt.
	If you detect that the user prompt is related to a tool, ignore this part and focus on the other parts.
	`

	messages = []api.Message{
		{Role: "system", Content: systemChatInstructions},
		{Role: "user", Content: userPrompt},
		{Role: "user", Content: contentForThePrompt},
	}

	var TRUE = true
	reqChat := &api.ChatRequest{
		Model:    a.Config.ChatModel,
		Messages: messages,
		Options: map[string]interface{}{
			"temperature":   0.0,
			"repeat_last_n": -1,
		},
		Stream: &TRUE,
	}

	answer := ""
	errChat := a.OllamaClient.Chat(ctx, reqChat, func(resp api.ChatResponse) error {
		answer += resp.Message.Content
		fmt.Print(resp.Message.Content)
		return nil
	})

	if errChat != nil {
		return fmt.Errorf("chat completion failed: %v", errChat)
	}

	return nil
}
