package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ollama/ollama/api"
)

func main() {
	// Setup basic logging
	log.SetFlags(log.Ltime | log.Lshortfile)
	log.Println("Starting MCP-Ollama Tool Executor")

	omodel := "mistral:7b"
	// Get configuration from environment or use defaults
	ollamaRawUrl := getEnvOrDefault("OLLAMA_HOST", "http://localhost:11434")
	toolsLLM := getEnvOrDefault("TOOLS_LLM", omodel)
	chatLLM := getEnvOrDefault("CHAT_LLM", omodel) // Changed to match your requirement
	mcpServer := getEnvOrDefault("MCP_SERVER", "./bin/swechain-mcp-server")

	// Fix URL parsing issue - ensure proper URL format
	if !strings.HasPrefix(ollamaRawUrl, "http://") && !strings.HasPrefix(ollamaRawUrl, "https://") {
		ollamaRawUrl = "http://" + ollamaRawUrl
	}

	// Parse Ollama URL
	ollamaUrl, err := url.Parse(ollamaRawUrl)
	if err != nil {
		// Fallback to localhost if URL parsing fails
		log.Printf("Warning: Invalid Ollama URL: %v. Falling back to localhost", err)
		ollamaUrl, _ = url.Parse("http://localhost:11434")
	}

	// Create Ollama client
	log.Println("Initializing Ollama client with URL:", ollamaUrl.String())
	ollamaClient := api.NewClient(ollamaUrl, http.DefaultClient)

	// Create MCP client
	log.Println("Initializing MCP client...")
	mcpClient, err := client.NewStdioMCPClient(mcpServer, []string{})
	if err != nil {
		log.Fatalf("Failed to create MCP client: %v", err)
	}
	defer mcpClient.Close()

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 60000*time.Second)
	defer cancel()

	// Initialize MCP client
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "swechain-tool-executor",
		Version: "1.0.0",
	}

	initResult, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		log.Fatalf("Failed to initialize MCP: %v", err)
	}
	log.Printf("Connected to MCP server: %s %s", initResult.ServerInfo.Name, initResult.ServerInfo.Version)

	// Fetch available tools from MCP
	log.Println("Fetching available tools...")
	toolsRequest := mcp.ListToolsRequest{}
	tools, err := mcpClient.ListTools(ctx, toolsRequest)
	if err != nil {
		log.Fatalf("Failed to list tools: %v", err)
	}

	// Log available tools
	//log.Printf("Found %d tools:", len(tools.Tools))
	for _, tool := range tools.Tools {
		log.Printf("- %s: %s", tool.Name, tool.Description)
	}

	// Convert MCP tools to Ollama format
	ollamaTools := convertToOllamaTools(tools.Tools)

	// Log available tools
	//log.Printf("Found %d Ollama tools:", len(ollamaTools))
	//log.Printf("Found %v Ollama tools:", ollamaTools)

	// User query - using current time rather than hardcoded values
	userQuery := fmt.Sprintf("I want to know bob address use feedbackQA, here are your aviable tools %s which one will you choose, reply only with json in text format without the ``` ", ollamaTools)
	//log.Printf("Processing query: %s", userQuery)

	// System prompt for tool selection
	systemPrompt := `You are a blockchain assistant for SWEChain.
Your job is to select the appropriate tool based on the current request.
Choose the tool that best fits the user's needs and provide the necessary arguments.
Only use the tools that are available to you.`

	// First LLM call - select the appropriate tool
	log.Println("Making tool selection call to Ollama...")
	toolSelectionResult := ""
	toolExecutionContent := ""

	streaming := false
	err = ollamaClient.Chat(ctx, &api.ChatRequest{
		Model: toolsLLM,
		Messages: []api.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userQuery},
		},
		Options: map[string]interface{}{
			"temperature": 0.0,
		},
		Tools:  ollamaTools,
		Stream: &streaming,
	}, func(resp api.ChatResponse) error {
		// Process tool calls
		for _, toolCall := range resp.Message.ToolCalls {
			toolName := toolCall.Function.Name
			toolArgs := toolCall.Function.Arguments

			log.Printf("Selected tool: %s with args: %v", toolName, toolArgs)
			toolSelectionResult = fmt.Sprintf("Chosen tool: %s\nArguments: %v", toolName, toolArgs)

			// Execute the selected tool via MCP
			log.Printf("Executing MCP tool: %s", toolName)
			fetchRequest := mcp.CallToolRequest{
				Request: mcp.Request{
					Method: "tools/call",
				},
			}
			fetchRequest.Params.Name = toolName
			fetchRequest.Params.Arguments = toolArgs

			result, err := mcpClient.CallTool(ctx, fetchRequest)
			if err != nil {
				log.Printf("Tool execution error: %v", err)
				toolExecutionContent = fmt.Sprintf("Error executing tool %s: %v", toolName, err)
				return err
			}

			// Extract tool result - handle content properly
			if len(result.Content) > 0 {
				contentBytes, err := json.Marshal(result.Content[0])
				log.Printf("Executing...Y")
				//contentBytes, err := json.Marshal(result)
				if err != nil {
					log.Printf("Failed to marshal content: %v", err)
				} else {
					toolExecutionContent = string(contentBytes)
					// Try to extract text if available
					var contentMap map[string]interface{}
					log.Printf("Executing...X")
					if err := json.Unmarshal(contentBytes, &contentMap); err == nil {
						if text, ok := contentMap["text"].(string); ok {
							toolExecutionContent = text
							log.Printf("Executing.... %s", text)
						}
					}
				}
			}

			log.Printf("Tool execution complete. Result length: %d chars", len(toolExecutionContent))
		}
		return nil
	})

	if err != nil {
		log.Printf("Tool selection error: %v", err)
	}

	// Second LLM call - generate response using tool results
	log.Println("Generating final response...")

	streaming = true
	fmt.Println("\n=== SWEChain Assistant Response ===")
	err = ollamaClient.Chat(ctx, &api.ChatRequest{
		Model: chatLLM,
		Messages: []api.Message{
			{Role: "system", Content: "You are a helpful blockchain assistant for SWEChain. Provide clear and concise responses."},
			{Role: "user", Content: userQuery},
			{Role: "assistant", Content: toolSelectionResult},
			{Role: "user", Content: fmt.Sprintf("Tool result: %s\nPlease provide a helpful response based on this result.", toolExecutionContent)},
		},
		Options: map[string]interface{}{
			"temperature": 0.0,
		},
		Stream: &streaming,
	}, func(resp api.ChatResponse) error {
		fmt.Print(resp.Message.Content)
		return nil
	})

	if err != nil {
		log.Printf("Response generation error: %v", err)
	}

	fmt.Println("\n=== End Response ===")
}

// Helper function to get environment variable with default
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// Convert MCP tools to Ollama's tool format
func convertToOllamaTools(tools []mcp.Tool) []api.Tool {
	ollamaTools := make([]api.Tool, len(tools))
	for i, tool := range tools {
		ollamaTools[i] = api.Tool{
			Type: "function",
			Function: api.ToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters: struct {
					Type       string   `json:"type"`
					Required   []string `json:"required"`
					Properties map[string]struct {
						Type        string   `json:"type"`
						Description string   `json:"description"`
						Enum        []string `json:"enum,omitempty"`
					} `json:"properties"`
				}{
					Type:       tool.InputSchema.Type,
					Required:   tool.InputSchema.Required,
					Properties: convertProperties(tool.InputSchema.Properties),
				},
			},
		}
	}
	return ollamaTools
}

// Helper function to convert properties to Ollama's format
func convertProperties(props map[string]interface{}) map[string]struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
} {
	result := make(map[string]struct {
		Type        string   `json:"type"`
		Description string   `json:"description"`
		Enum        []string `json:"enum,omitempty"`
	})

	for name, prop := range props {
		if propMap, ok := prop.(map[string]interface{}); ok {
			prop := struct {
				Type        string   `json:"type"`
				Description string   `json:"description"`
				Enum        []string `json:"enum,omitempty"`
			}{
				Type:        getString(propMap, "type"),
				Description: getString(propMap, "description"),
			}

			// Handle enum if present
			if enumRaw, ok := propMap["enum"].([]interface{}); ok {
				for _, e := range enumRaw {
					if str, ok := e.(string); ok {
						prop.Enum = append(prop.Enum, str)
					}
				}
			}

			result[name] = prop
		}
	}

	return result
}

// Helper function to safely get string values from map
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
