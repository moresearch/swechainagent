package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ollama/ollama/api"
)

// Core configuration
type Config struct {
	Model    string
	Server   string
	MaxSteps int
}

// Tool executor
type Executor struct {
	mcpClient *client.StdioMCPClient
	ollamaAPI *api.Client
	tools     []mcp.Tool
}

func main() {
	// Setup logging to both file and stdout
	setupConsoleLogger()

	log.Println("Starting SWEChain Tool Executor")

	if len(os.Args) < 2 {
		log.Fatal("Usage: ./executor <prompt_file>")
	}

	// Read prompt
	promptFile := os.Args[1]
	log.Println("Reading prompt from file:", promptFile)
	promptContent, err := ioutil.ReadFile(promptFile)
	if err != nil {
		log.Fatalf("Failed to read prompt: %v", err)
	}

	// Configuration
	config := Config{
		Model:    "llama3.1:8b-instruct-q8_0",
		Server:   os.Getenv("MCP_SERVER"),
		MaxSteps: 10,
	}
	if config.Server == "" {
		config.Server = "./bin/swechain-mcp-server"
	}
	log.Println("Using model:", config.Model)
	log.Println("Using MCP server:", config.Server)

	// Initialize executor
	log.Println("Initializing executor...")
	exec, err := newExecutor(config)
	if err != nil {
		log.Fatalf("Initialization failed: %v", err)
	}
	defer exec.close()

	// Run session
	log.Println("Starting interaction session")
	err = exec.runSession(string(promptContent))
	if err != nil {
		log.Fatalf("Session failed: %v", err)
	}
	log.Println("Session completed successfully")
}

// Setup logging to console
func setupConsoleLogger() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ltime | log.Lshortfile)
}

// Create new executor
func newExecutor(config Config) (*Executor, error) {
	// Initialize MCP client
	log.Println("Creating MCP client...")
	mcpClient, err := client.NewStdioMCPClient(config.Server, []string{})
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP client: %w", err)
	}

	// Initialize MCP protocol
	log.Println("Initializing MCP protocol...")
	ctx := context.Background()
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION

	result, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		mcpClient.Close()
		return nil, fmt.Errorf("failed to initialize MCP: %w", err)
	}
	log.Printf("Connected to MCP server: %s %s", result.ServerInfo.Name, result.ServerInfo.Version)

	// Get Ollama API client
	log.Println("Setting up Ollama API client...")
	ollamaURL, err := url.Parse("http://localhost:11434")
	if err != nil {
		mcpClient.Close()
		return nil, fmt.Errorf("invalid Ollama URL: %w", err)
	}
	ollamaAPI := api.NewClient(ollamaURL, &http.Client{
		Timeout: 2 * time.Minute,
	})

	executor := &Executor{
		mcpClient: mcpClient,
		ollamaAPI: ollamaAPI,
	}

	// Fetch available tools
	log.Println("Fetching available tools...")
	toolsRequest := mcp.ListToolsRequest{}
	toolsResponse, err := mcpClient.ListTools(ctx, toolsRequest)
	if err != nil {
		mcpClient.Close()
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}
	executor.tools = toolsResponse.Tools
	log.Printf("Found %d available tools", len(executor.tools))

	for i, tool := range executor.tools {
		log.Printf("Tool %d: %s - %s", i+1, tool.Name, tool.Description)
	}

	return executor, nil
}

// Close connections
func (e *Executor) close() {
	log.Println("Closing connections...")
	if e.mcpClient != nil {
		e.mcpClient.Close()
	}
}

// Run complete session
func (e *Executor) runSession(prompt string) error {
	// Format tools info
	log.Println("Formatting tools information...")
	toolsInfo := formatToolsInfo(e.tools)
	promptWithTools := strings.Replace(prompt, "{{TOOLS_INFO}}", toolsInfo, 1)

	// Convert tools to Ollama format
	log.Println("Converting tools to Ollama format...")
	ollamaTools := convertToOllamaTools(e.tools)

	// Initialize conversation
	log.Println("Initializing conversation...")
	messages := []api.Message{
		{
			Role: "system",
			Content: `You are a blockchain assistant for SWEChain. Use the tools provided to help users.
Format tool calls as JSON: {"function":{"name":"tool_name","arguments":{"param1":"value1"}}}`,
		},
		{
			Role:    "user",
			Content: promptWithTools,
		},
	}

	// Run ReAct loop
	ctx := context.Background()
	steps := 0
	toolsUsed := 0

	fmt.Println("\n=== SWEChain Assistant ===")
	log.Println("Starting ReAct loop...")

	for steps < 10 {
		steps++
		log.Printf("--- Iteration %d ---", steps)

		// Trim conversation if needed
		if len(messages) > 15 {
			log.Println("Trimming conversation history...")
			messages = append([]api.Message{messages[0]}, messages[len(messages)-14:]...)
		}

		// Get model reasoning
		log.Println("Getting response from model...")
		response, err := getModelResponse(ctx, e.ollamaAPI, messages, ollamaTools, false)
		if err != nil {
			log.Printf("ERROR: Model error: %v", err)
			return fmt.Errorf("model error: %w", err)
		}
		log.Printf("Received response of length: %d characters", len(response))

		// Add to conversation
		messages = append(messages, api.Message{
			Role:    "assistant",
			Content: response,
		})

		// Try to extract tool call
		log.Println("Attempting to extract tool call...")
		toolName, argsJson, err := extractToolCall(response)

		if err != nil || toolName == "" {
			log.Println("Direct extraction failed, trying with grammar constraint...")
			// If we couldn't extract a tool call, try with grammar constraint
			toolCallMsg := "Select a specific tool to use. Respond ONLY with JSON."
			log.Println("Requesting explicit tool call with grammar...")
			grammarResponse, err := getModelResponse(ctx, e.ollamaAPI, append(messages, api.Message{
				Role:    "user",
				Content: toolCallMsg,
			}), ollamaTools, true)

			if err != nil {
				log.Printf("ERROR: Failed to get grammar-constrained response: %v", err)
				messages = append(messages, api.Message{
					Role:    "user",
					Content: "There was an error. Please try again with a valid tool call.",
				})
				continue
			}

			log.Println("Extracting tool call from grammar-constrained response...")
			toolName, argsJson, err = extractToolCall(grammarResponse)
			if err != nil || toolName == "" {
				log.Println("Still failed to extract tool call, requesting again...")
				messages = append(messages, api.Message{
					Role:    "user",
					Content: "Please select a specific tool and provide a valid JSON tool call.",
				})
				continue
			}
		}

		log.Printf("Successfully extracted tool call: %s", toolName)
		log.Printf("Tool arguments: %s", argsJson)
		fmt.Printf("\n[Using tool: %s with arguments: %s]\n", toolName, argsJson)

		// Execute tool
		log.Printf("Executing tool: %s...", toolName)
		result, err := e.executeTool(ctx, toolName, argsJson)
		if err != nil {
			log.Printf("ERROR: Tool execution failed: %v", err)
			fmt.Printf("\n[Tool failed: %v]\n", err)
			messages = append(messages, api.Message{
				Role:    "user",
				Content: fmt.Sprintf("Tool %s failed: %v. Please try again with correct parameters.", toolName, err),
			})
			continue
		}

		log.Printf("Tool execution successful, result length: %d chars", len(result))
		toolsUsed++
		fmt.Printf("\n[Tool result: %s]\n", result)

		// Add tool result to conversation
		messages = append(messages, api.Message{
			Role:    "user",
			Content: fmt.Sprintf("Result from %s: %s\n\nAnalyze this result. Provide a conclusion if this addresses the request, or use another tool if needed.", toolName, result),
		})

		// Check if we're done
		if toolsUsed > 0 && containsCompletion(response) {
			log.Println("Task completion detected, ending session")
			fmt.Println("\n\nTask completed successfully.")
			break
		}
	}

	if steps >= 10 {
		log.Println("Maximum iterations reached")
		return fmt.Errorf("max iterations reached")
	}

	return nil
}

// Get model response
func getModelResponse(ctx context.Context, client *api.Client, messages []api.Message, tools []api.Tool, useGrammar bool) (string, error) {
	streaming := !useGrammar
	var responseBuilder strings.Builder
	var response api.ChatResponse

	options := map[string]interface{}{
		"temperature": 0.1,
		"num_ctx":     4096,
	}

	// Define grammar for JSON constraint when needed
	if useGrammar {
		log.Println("Using grammar constraint for structured output")
		options["grammar"] = `
root   ::= object
object ::= "{" (pair ("," pair)*)? "}"
pair   ::= string ":" value
array  ::= "[" (value ("," value)*)? "]"
string ::= "\"" ([^"\\] | [\\]["\\/bfnrt])* "\""
number ::= [0-9]+ ("." [0-9]+)? ([eE] [-+]? [0-9]+)?
value  ::= object | array | string | number | ("true" | "false" | "null")
`
		options["temperature"] = 0.05
	}

	if streaming {
		log.Println("Making streaming API call to Ollama...")
	} else {
		log.Println("Making non-streaming API call to Ollama...")
	}

	err := client.Chat(ctx, &api.ChatRequest{
		Model:    "llama3.1:8b-instruct-q8_0",
		Messages: messages,
		Options:  options,
		Tools:    tools,
		Stream:   &streaming,
	}, func(resp api.ChatResponse) error {
		if streaming {
			fmt.Print(resp.Message.Content)
			responseBuilder.WriteString(resp.Message.Content)
		} else {
			response = resp
		}
		return nil
	})

	if err != nil {
		log.Printf("ERROR: Ollama API call failed: %v", err)
		return "", err
	}

	if streaming {
		return responseBuilder.String(), nil
	}

	return response.Message.Content, nil
}

// Extract tool call with enhanced parsing
func extractToolCall(response string) (string, string, error) {
	// Try to find complete JSON object with function
	re := regexp.MustCompile(`\{[\s\S]*"function"[\s\S]*\}`)
	match := re.FindString(response)
	if match != "" {
		log.Println("Found function-style JSON")
		return parseToolCallJson(match)
	}

	// Look for code blocks with JSON
	re = regexp.MustCompile("```(?:json)?\\s*(\\{[\\s\\S]*?\\})\\s*```")
	if matches := re.FindStringSubmatch(response); len(matches) > 1 {
		log.Println("Found JSON in code block")
		return parseToolCallJson(matches[1])
	}

	// Try to find tool_calls array
	re = regexp.MustCompile(`\{[\s\S]*"tool_calls"[\s\S]*\}`)
	match = re.FindString(response)
	if match != "" {
		log.Println("Found tool_calls-style JSON")
		return parseToolCallJson(match)
	}

	// Find any JSON objects
	re = regexp.MustCompile(`\{(?:[^{}]|(?:\{[^{}]*\}))*\}`)
	matches := re.FindAllString(response, -1)
	for _, match := range matches {
		if strings.Contains(match, "\"function\"") && strings.Contains(match, "\"name\"") {
			log.Println("Found function name in general JSON object")
			return parseToolCallJson(match)
		}
		if strings.Contains(match, "\"tool_calls\"") {
			log.Println("Found tool_calls in general JSON object")
			return parseToolCallJson(match)
		}
	}

	log.Println("No valid tool call JSON found in response")
	return "", "", fmt.Errorf("no valid tool call found")
}

// Parse tool call JSON
func parseToolCallJson(jsonStr string) (string, string, error) {
	// Clean up the JSON string
	jsonStr = strings.TrimSpace(jsonStr)
	log.Printf("Attempting to parse JSON: %s", truncateForLog(jsonStr))

	// Structure 1: {"function": {"name": "...", "arguments": {...}}}
	var format1 struct {
		Function struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		} `json:"function"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &format1); err == nil {
		if format1.Function.Name != "" {
			argsBytes, err := json.Marshal(format1.Function.Arguments)
			if err == nil {
				log.Printf("Successfully parsed format 1: function.name = %s", format1.Function.Name)
				return format1.Function.Name, string(argsBytes), nil
			}
			log.Printf("ERROR: Failed to marshal arguments: %v", err)
		}
	}

	// Structure 2: {"tool_calls": [{"function": {"name": "...", "arguments": {...}}}]}
	var format2 struct {
		ToolCalls []struct {
			Function struct {
				Name      string                 `json:"name"`
				Arguments map[string]interface{} `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &format2); err == nil {
		if len(format2.ToolCalls) > 0 && format2.ToolCalls[0].Function.Name != "" {
			argsBytes, err := json.Marshal(format2.ToolCalls[0].Function.Arguments)
			if err == nil {
				log.Printf("Successfully parsed format 2: tool_calls[0].function.name = %s",
					format2.ToolCalls[0].Function.Name)
				return format2.ToolCalls[0].Function.Name, string(argsBytes), nil
			}
			log.Printf("ERROR: Failed to marshal arguments: %v", err)
		}
	}

	log.Println("Failed to parse JSON in any recognized format")
	return "", "", fmt.Errorf("unrecognized tool call format")
}

// Helper function to truncate strings for logging
func truncateForLog(s string) string {
	maxLen := 100
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// Execute a tool
func (e *Executor) executeTool(ctx context.Context, toolName, argsString string) (string, error) {
	// Create tool request
	fetchRequest := mcp.CallToolRequest{
		Request: mcp.Request{
			Method: "tools/call",
		},
	}
	fetchRequest.Params.Name = toolName

	// Parse arguments
	var argsMap map[string]interface{}
	if err := json.Unmarshal([]byte(argsString), &argsMap); err != nil {
		log.Printf("ERROR: Failed to parse arguments: %v", err)
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}
	fetchRequest.Params.Arguments = argsMap

	// Call the tool
	log.Printf("Calling MCP tool %s with %d parameters", toolName, len(argsMap))
	result, err := e.mcpClient.CallTool(ctx, fetchRequest)
	if err != nil {
		log.Printf("ERROR: MCP tool execution failed: %v", err)
		return "", err
	}

	// Extract result
	log.Println("Processing tool result...")
	toolResult := "No content returned from tool"
	if len(result.Content) > 0 {
		contentBytes, err := json.Marshal(result.Content[0])
		if err == nil {
			var contentMap map[string]interface{}
			if err := json.Unmarshal(contentBytes, &contentMap); err == nil {
				if text, ok := contentMap["text"].(string); ok {
					toolResult = text
				} else {
					toolResult = string(contentBytes)
				}
			} else {
				toolResult = string(contentBytes)
			}
		}
	}

	log.Printf("Tool execution complete, result length: %d chars", len(toolResult))
	return toolResult, nil
}

// Format tools info for prompt
func formatToolsInfo(tools []mcp.Tool) string {
	var sb strings.Builder
	sb.WriteString("AVAILABLE TOOLS:\n")

	for _, tool := range tools {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", tool.Name, tool.Description))
		sb.WriteString("  Parameters:\n")

		for name, propInterface := range tool.InputSchema.Properties {
			if propMap, ok := propInterface.(map[string]interface{}); ok {
				propType := getString(propMap, "type")
				propDesc := getString(propMap, "description")
				sb.WriteString(fmt.Sprintf("    - %s (%s): %s\n", name, propType, propDesc))
			}
		}

		sb.WriteString("\n")
	}

	return sb.String()
}

// Convert MCP tools to Ollama format
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

// Helper for parameter conversion
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

// Safe string getter from map
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// Check for completion indicators
func containsCompletion(message string) bool {
	lowerMsg := strings.ToLower(message)
	return strings.Contains(lowerMsg, "conclusion") ||
		strings.Contains(lowerMsg, "final answer") ||
		strings.Contains(message, "completed the requested tasks") ||
		strings.Contains(lowerMsg, "to summarize")
}
