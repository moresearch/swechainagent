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

// Config holds the core configuration for the executor.
type Config struct {
	Model       string
	OllamaURL   string
	MCP_Server  string
	MaxSteps    int
	ContextSize int
	Temperature float64
}

// Executor manages the interaction between the LLM and the MCP tools.
type Executor struct {
	mcpClient *client.StdioMCPClient
	ollamaAPI *api.Client
	tools     []mcp.Tool
	config    Config
}

// ToolCall represents the expected JSON structure for a tool call from the LLM.
// We internally map both "function"/"name" and "chosen_tool" to this structure.
type ToolCall struct {
	Function struct {
		Name      string                 `json:"name"` // Internal representation of the tool name
		Arguments map[string]interface{} `json:"arguments"`
	} `json:"function"`
}

func main() {
	setupConsoleLogger()
	log.Println("Starting SWEChain Tool Executor")

	if len(os.Args) < 2 {
		log.Fatal("Usage: ./executor <prompt_file>")
	}
	promptFile := os.Args[1]

	promptContent, err := ioutil.ReadFile(promptFile)
	if err != nil {
		log.Fatalf("Failed to read prompt file %s: %v", promptFile, err)
	}

	// Configuration
	config := Config{
		Model:       "llama3.1:8b-instruct-q8_0",
		OllamaURL:   "http://localhost:11434",
		MCP_Server:  os.Getenv("MCP_SERVER"),
		MaxSteps:    10,
		ContextSize: 4096, // Adjust if needed based on prompt size + history
		Temperature: 0.1,
	}
	if config.MCP_Server == "" {
		config.MCP_Server = "./bin/swechain-mcp-server"
	}

	log.Printf("Configuration: Model=%s, Ollama=%s, MCP=%s, MaxSteps=%d",
		config.Model, config.OllamaURL, config.MCP_Server, config.MaxSteps)

	// Initialize executor
	exec, err := newExecutor(config)
	if err != nil {
		log.Fatalf("Initialization failed: %v", err)
	}
	defer exec.close()

	// Run session
	log.Println("Starting interaction session")
	err = exec.runSession(context.Background(), string(promptContent))
	if err != nil {
		log.Fatalf("Session failed: %v", err)
	}
	log.Println("Session completed successfully")
}

// setupConsoleLogger configures basic logging to stdout.
func setupConsoleLogger() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ltime | log.Lshortfile)
}

// newExecutor creates and initializes a new Executor instance.
func newExecutor(config Config) (*Executor, error) {
	// 1. Initialize MCP Client
	log.Println("Connecting to MCP server:", config.MCP_Server)
	mcpClient, err := client.NewStdioMCPClient(config.MCP_Server, []string{})
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP client: %w", err)
	}

	// 2. Initialize MCP Protocol
	log.Println("Initializing MCP protocol...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "swechain-executor",
		Version: "0.1.0",
	}
	initResult, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		mcpClient.Close()
		return nil, fmt.Errorf("failed to initialize MCP protocol: %w", err)
	}
	log.Printf("Connected to MCP server: %s v%s", initResult.ServerInfo.Name, initResult.ServerInfo.Version)

	// 3. Initialize Ollama Client
	log.Println("Setting up Ollama API client for:", config.OllamaURL)
	ollamaURL, err := url.Parse(config.OllamaURL)
	if err != nil {
		mcpClient.Close()
		return nil, fmt.Errorf("invalid Ollama URL %s: %w", config.OllamaURL, err)
	}
	ollamaAPI := api.NewClient(ollamaURL, &http.Client{
		Timeout: 2 * time.Minute,
	})

	// 4. Fetch Available Tools
	log.Println("Fetching available tools from MCP server...")
	toolsRequest := mcp.ListToolsRequest{}
	toolsResponse, err := mcpClient.ListTools(ctx, toolsRequest)
	if err != nil {
		mcpClient.Close()
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}
	log.Printf("Found %d available tools:", len(toolsResponse.Tools))
	for i, tool := range toolsResponse.Tools {
		log.Printf("  Tool %d: %s - %s", i+1, tool.Name, tool.Description)
	}

	return &Executor{
		mcpClient: mcpClient,
		ollamaAPI: ollamaAPI,
		tools:     toolsResponse.Tools,
		config:    config,
	}, nil
}

// close cleans up resources used by the executor.
func (e *Executor) close() {
	log.Println("Closing connections...")
	if e.mcpClient != nil {
		if err := e.mcpClient.Close(); err != nil {
			log.Printf("Error closing MCP client: %v", err)
		}
	}
}

// runSession executes the main ReAct loop.
func (e *Executor) runSession(ctx context.Context, initialPrompt string) error {
	toolsInfo := formatToolsInfo(e.tools)
	promptWithTools := strings.Replace(initialPrompt, "{{TOOLS_INFO}}", toolsInfo, 1)

	ollamaTools := convertToOllamaTools(e.tools)

	// --- Updated System Prompt with Examples ---
	systemPrompt := `You are a helpful assistant interacting with the SWEChain environment.
Use the available tools to fulfill the user's request.
When you need to use a tool, you MUST respond ONLY with a single JSON object in the specified format below.

**CORRECT Tool Call Format Example:**
` + "```json" + `
{
  "function": {
    "name": "create-bid",
    "arguments": {
      "bidder": "cosmos1abcdef...",
      "amount": "500",
      "auctionId": "auction-xyz-789",
      "from": "cosmos1abcdef..."
    }
  }
}
` + "```" + `

**ANOTHER CORRECT Example:**
` + "```json" + `
{
  "function": {
    "name": "open-auction",
    "arguments": {
      "issue": "owner/repo/issues/123",
      "description": "Detailed description of auction goals.",
      "starting_bid": "100",
      "from": "cosmos1ghijkl..."
    }
  }
}
` + "```" + `

**INCORRECT Format Example (Do NOT use this structure):**
` + "```json" + `
// Incorrect: Uses "chosen_tool" key instead of "function" and "name" keys.
{
  "chosen_tool": "some-tool",
  "arguments": { ... }
}
` + "```" + `

**INCORRECT Argument Value Example:**
` + "```json" + `
// Incorrect: "from" value is not a valid address.
{
  "function": {
    "name": "open-auction",
    "arguments": {
      "issue": "owner/repo/issues/123",
      "description": "...",
      "from": "MyAgentIdentifier"
    }
  }
}
` + "```" + `

Now, analyze the request and available tools. If a tool is needed, provide the JSON call. Otherwise, provide your reasoning or final answer.`
	// --- End of Updated System Prompt ---

	messages := []api.Message{
		{
			Role:    "system",
			Content: systemPrompt,
		},
		{
			Role:    "user",
			Content: promptWithTools, // Initial user prompt with {{TOOLS_INFO}} replaced
		},
	}

	fmt.Println("\n=== SWEChain Assistant ===")
	log.Println("Starting ReAct loop...")

	for step := 1; step <= e.config.MaxSteps; step++ {
		log.Printf("--- Step %d/%d ---", step, e.config.MaxSteps)
		fmt.Printf("\n--- Step %d ---\n", step)

		// 1. Get LLM Response
		log.Println("Querying LLM...")
		responseContent, err := e.getModelResponse(ctx, messages, ollamaTools)
		if err != nil {
			log.Printf("ERROR: Failed to get model response: %v", err)
			return fmt.Errorf("LLM communication failed: %w", err)
		}
		log.Printf("LLM raw response length: %d chars", len(responseContent))
		fmt.Printf("Assistant:\n%s\n", responseContent) // Show full response for debugging

		messages = append(messages, api.Message{Role: "assistant", Content: responseContent})

		// 2. Extract Tool Call
		log.Println("Attempting to extract tool call...")
		toolCall, err := extractToolCall(responseContent) // Use the updated extraction function
		if err != nil {
			log.Println("No valid tool call found in the response.")
			if containsCompletion(responseContent) {
				log.Println("Completion indicators found and no tool call. Ending session.")
				fmt.Println("\nTask appears complete.")
				return nil
			}
			log.Println("No tool call, continuing loop (or ending if max steps reached).")
			continue
		}

		// 3. Execute Tool
		toolName := toolCall.Function.Name // Use the internally mapped name
		argsJSON, _ := json.Marshal(toolCall.Function.Arguments)
		log.Printf("Extracted tool call: %s(%s)", toolName, string(argsJSON))
		fmt.Printf("\n[Using tool: %s with args: %s]\n", toolName, string(argsJSON))

		toolResult, err := e.executeTool(ctx, toolName, toolCall.Function.Arguments)
		if err != nil {
			log.Printf("ERROR: Tool execution failed for %s: %v", toolName, err)
			fmt.Printf("[Tool Error: %v]\n", err)
			messages = append(messages, api.Message{
				Role:    "user",
				Content: fmt.Sprintf("Error executing tool %s: %v. Please analyze the error and decide the next step.", toolName, err),
			})
			continue
		}

		log.Printf("Tool %s executed successfully. Result length: %d chars", toolName, len(toolResult))
		fmt.Printf("[Tool Result: %s]\n", truncateForDisplay(toolResult))

		// 4. Add Tool Result to Context
		messages = append(messages, api.Message{
			Role:    "user",
			Content: fmt.Sprintf("Result from tool %s: %s", toolName, toolResult),
		})
	}

	log.Println("Maximum steps reached.")
	fmt.Println("\nMaximum interaction steps reached.")
	return fmt.Errorf("max steps (%d) reached without clear completion", e.config.MaxSteps)
}

// getModelResponse sends the conversation to the LLM and gets a response.
func (e *Executor) getModelResponse(ctx context.Context, messages []api.Message, tools []api.Tool) (string, error) {
	maxMessages := 20
	if len(messages) > maxMessages {
		// A simple trimming strategy: keep system + last N-1 messages
		log.Printf("Trimming conversation history from %d to %d messages", len(messages), maxMessages)
		messages = append([]api.Message{messages[0]}, messages[len(messages)-maxMessages+1:]...)
	}

	streaming := false
	var response api.ChatResponse

	req := &api.ChatRequest{
		Model:    e.config.Model,
		Messages: messages,
		Options: map[string]interface{}{
			"temperature": e.config.Temperature,
			"num_ctx":     e.config.ContextSize,
		},
		Tools:  tools,
		Stream: &streaming,
	}

	err := e.ollamaAPI.Chat(ctx, req, func(resp api.ChatResponse) error {
		response = resp
		return nil
	})

	if err != nil {
		log.Printf("ERROR: Ollama API call failed: %v", err)
		return "", fmt.Errorf("ollama API error: %w", err)
	}

	if response.Message.Content == "" {
		log.Println("WARNING: Ollama returned an empty message content.")
		// Check if Ollama populated the ToolCalls structure directly
		if len(response.Message.ToolCalls) > 0 {
			toolCallData, _ := json.Marshal(response.Message.ToolCalls[0])
			log.Printf("Found tool call directly in Ollama response structure: %s", string(toolCallData))
			// Attempt to reconstruct one of the expected JSON string formats for extractToolCall
			// Prefer the 'function/name' format as it's the one we requested in the prompt
			argsBytes, err := json.Marshal(response.Message.ToolCalls[0].Function.Arguments)
			if err != nil {
				log.Printf("ERROR: Failed to marshal arguments from Ollama ToolCalls structure: %v", err)
				return "", nil // Return empty string, extraction will fail later
			}
			// Construct the JSON string matching the desired format
			return fmt.Sprintf(`{"function": {"name": "%s", "arguments": %s}}`,
				response.Message.ToolCalls[0].Function.Name,
				string(argsBytes)), nil
		}
	}

	return response.Message.Content, nil
}

// Regex for the originally intended format: {"function": {"name": "...", "arguments": {...}}}
var functionNameRegex = regexp.MustCompile(`(?s)\{\s*"function"\s*:\s*\{\s*"name"\s*:\s*"([^"]+)"\s*,\s*"arguments"\s*:\s*(\{.*?\})\s*\}\s*\}`)

// Regex for the format the LLM might produce: {"chosen_tool": "...", "arguments": {...}}
var chosenToolRegex = regexp.MustCompile(`(?s)\{\s*"chosen_tool"\s*:\s*"([^"]+)"\s*,\s*"arguments"\s*:\s*(\{.*?\})\s*\}`)

// Regex for finding JSON within markdown code blocks (fallback)
var codeBlockJsonRegex = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{(?:[^{}]|\\{[^{}]*\\})*\\})\\s*```")

// extractToolCall attempts to find and parse known tool call JSON formats.
func extractToolCall(response string) (*ToolCall, error) {
	log.Printf("Attempting to extract tool call from response:\n%s\n", truncateForDisplay(response))

	var potentialJson string
	var toolName string
	var argumentsJson string // Store arguments as a JSON string initially

	// 1. Check for `{"function": {"name": ...}}` format directly
	matches := functionNameRegex.FindStringSubmatch(response)
	if len(matches) == 3 {
		log.Println("Found 'function/name' format.")
		potentialJson = matches[0]
		toolName = matches[1]
		argumentsJson = matches[2]
	} else {
		// 2. Check for `{"chosen_tool": ...}` format directly
		matches = chosenToolRegex.FindStringSubmatch(response)
		if len(matches) == 3 {
			log.Println("Found 'chosen_tool' format.")
			potentialJson = matches[0]
			toolName = matches[1]
			argumentsJson = matches[2]
		} else {
			// 3. Fallback: Check for JSON within markdown code blocks
			log.Println("Direct formats not found, checking markdown code blocks...")
			codeBlockMatches := codeBlockJsonRegex.FindStringSubmatch(response)
			if len(codeBlockMatches) > 1 {
				jsonInBlock := codeBlockMatches[1]
				log.Printf("Found JSON in code block: %s", truncateForDisplay(jsonInBlock))
				// Now try matching the known formats *within* the code block
				matches = functionNameRegex.FindStringSubmatch(jsonInBlock)
				if len(matches) == 3 {
					log.Println("Found 'function/name' format inside code block.")
					potentialJson = jsonInBlock
					toolName = matches[1]
					argumentsJson = matches[2]
				} else {
					matches = chosenToolRegex.FindStringSubmatch(jsonInBlock)
					if len(matches) == 3 {
						log.Println("Found 'chosen_tool' format inside code block.")
						potentialJson = jsonInBlock
						toolName = matches[1]
						argumentsJson = matches[2]
					}
				}
			}
		}
	}

	// 4. If no format was matched, return error
	if toolName == "" || argumentsJson == "" {
		log.Println("No known tool call JSON structure found.")
		return nil, fmt.Errorf("no known tool call JSON structure found")
	}

	log.Printf("Identified Tool Name: %s", toolName)
	log.Printf("Identified Arguments JSON string: %s", argumentsJson)

	// 5. Parse the arguments JSON string into a map
	var argumentsMap map[string]interface{}
	decoder := json.NewDecoder(strings.NewReader(argumentsJson))
	if err := decoder.Decode(&argumentsMap); err != nil {
		log.Printf("ERROR: Failed to decode arguments JSON: %v. JSON string was: %s. Full matched JSON context: %s", err, argumentsJson, potentialJson)
		if errFallback := json.Unmarshal([]byte(argumentsJson), &argumentsMap); errFallback != nil {
			log.Printf("ERROR: Fallback unmarshal also failed for arguments: %v", errFallback)
			return nil, fmt.Errorf("failed to parse tool arguments JSON: %w (primary error: %v)", errFallback, err)
		}
		log.Println("WARN: Strict JSON decoding failed, but fallback unmarshal succeeded for arguments.")
	}

	if argumentsMap == nil {
		argumentsMap = make(map[string]interface{})
	}

	// 6. Construct the ToolCall struct (using the internal 'Function.Name' field)
	toolCall := &ToolCall{
		Function: struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}{
			Name:      toolName,
			Arguments: argumentsMap,
		},
	}

	log.Printf("Successfully parsed tool call: Name=%s, Args=%+v", toolCall.Function.Name, toolCall.Function.Arguments)
	return toolCall, nil
}

// executeTool calls the specified tool via the MCP client.
func (e *Executor) executeTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	fetchRequest := mcp.CallToolRequest{
		Request: mcp.Request{Method: "tools/call"},
	}
	fetchRequest.Params.Name = toolName
	fetchRequest.Params.Arguments = args

	log.Printf("Calling MCP tool '%s'...", toolName)
	toolCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()

	result, err := e.mcpClient.CallTool(toolCtx, fetchRequest)
	if err != nil {
		log.Printf("ERROR: MCP tool '%s' execution failed: %v", toolName, err)
		return "", fmt.Errorf("MCP tool '%s' failed: %w", toolName, err)
	}

	if result.IsError {
		log.Printf("ERROR: MCP tool '%s' returned an error response.", toolName)
		errorMsg := "Tool returned an unspecified error."
		if len(result.Content) > 0 {
			contentItem := result.Content[0]
			if textContent, ok := contentItem.(mcp.TextContent); ok {
				errorMsg = textContent.Text
				log.Printf("Tool error content (TextContent): %s", errorMsg)
			} else {
				errorBytes, _ := json.Marshal(contentItem)
				errorMsg = fmt.Sprintf("Tool returned non-text error content: %s", string(errorBytes))
				log.Printf("Tool error content (Unknown type %T): %s", contentItem, string(errorBytes))
			}
		}
		return "", fmt.Errorf("tool '%s' reported an error: %s", toolName, errorMsg)
	}

	log.Println("Processing successful tool result...")
	toolResult := "Tool executed successfully but returned no content."
	if len(result.Content) > 0 {
		contentItem := result.Content[0]
		if textContent, ok := contentItem.(mcp.TextContent); ok {
			toolResult = textContent.Text
		} else {
			log.Printf("Tool result content is not TextContent (type: %T), returning as JSON.", contentItem)
			resultBytes, err := json.Marshal(contentItem)
			if err != nil {
				log.Printf("ERROR: Failed to marshal non-text tool result content: %v", err)
				toolResult = "[Error marshalling tool result]"
			} else {
				toolResult = string(resultBytes)
			}
		}
	}

	log.Printf("Tool execution complete. Result length: %d chars", len(toolResult))
	return toolResult, nil
}

// formatToolsInfo creates a string describing the available tools for the LLM prompt.
func formatToolsInfo(tools []mcp.Tool) string {
	var sb strings.Builder
	sb.WriteString("AVAILABLE TOOLS:\n")
	if len(tools) == 0 {
		sb.WriteString("  (No tools available)\n")
		return sb.String()
	}

	for _, tool := range tools {
		sb.WriteString(fmt.Sprintf("- Name: %s\n", tool.Name))
		sb.WriteString(fmt.Sprintf("  Description: %s\n", tool.Description))
		sb.WriteString("  Parameters:\n")

		if len(tool.InputSchema.Properties) == 0 {
			sb.WriteString("    (No parameters)\n")
		} else {
			for name, propInterface := range tool.InputSchema.Properties {
				if propMap, ok := propInterface.(map[string]interface{}); ok {
					propType := getStringMap(propMap, "type", "unknown")
					propDesc := getStringMap(propMap, "description", "No description.")
					required := "optional"
					for _, reqName := range tool.InputSchema.Required {
						if name == reqName {
							required = "required"
							break
						}
					}
					sb.WriteString(fmt.Sprintf("    - %s (%s, %s): %s\n", name, propType, required, propDesc))
				} else {
					sb.WriteString(fmt.Sprintf("    - %s: (Invalid schema format)\n", name))
				}
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// convertToOllamaTools converts MCP tool definitions to the format Ollama expects.
func convertToOllamaTools(tools []mcp.Tool) []api.Tool {
	ollamaTools := make([]api.Tool, 0, len(tools))

	for _, tool := range tools {
		// Define the properties map with the EXACT anonymous struct type expected by the library
		properties := make(map[string]struct {
			Type        string   `json:"type"`
			Description string   `json:"description"`
			Enum        []string `json:"enum,omitempty"`
		})

		for name, propInterface := range tool.InputSchema.Properties {
			if propMap, ok := propInterface.(map[string]interface{}); ok {
				// Create instances of the anonymous struct directly
				prop := struct {
					Type        string   `json:"type"`
					Description string   `json:"description"`
					Enum        []string `json:"enum,omitempty"`
				}{
					Type:        getStringMap(propMap, "type", "string"),
					Description: getStringMap(propMap, "description", ""),
				}

				// Handle enum if present
				if enumRaw, ok := propMap["enum"].([]interface{}); ok {
					for _, e := range enumRaw {
						if str, ok := e.(string); ok {
							prop.Enum = append(prop.Enum, str)
						}
					}
				}
				properties[name] = prop
			}
		}

		ollamaTool := api.Tool{
			Type: "function",
			Function: api.ToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				// Assign the correctly typed properties map to the Parameters struct literal
				Parameters: struct {
					Type       string              `json:"type"`
					Required   []string            `json:"required"`
					Properties map[string]struct { // Use the anonymous struct type here
						Type        string   `json:"type"`
						Description string   `json:"description"`
						Enum        []string `json:"enum,omitempty"`
					} `json:"properties"`
				}{
					Type:       "object",
					Properties: properties,
					Required:   tool.InputSchema.Required,
				},
			},
		}
		ollamaTools = append(ollamaTools, ollamaTool)
	}

	return ollamaTools
}

// --- Helper Functions ---

// getStringMap safely gets a string value from a map[string]interface{}, providing a default.
func getStringMap(m map[string]interface{}, key string, defaultValue string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return defaultValue
}

// containsCompletion checks for simple keywords indicating the task might be done.
func containsCompletion(message string) bool {
	lowerMsg := strings.ToLower(message)
	keywords := []string{"conclusion", "final answer", "task complete", "request fulfilled", "summarize"}
	for _, kw := range keywords {
		if strings.Contains(lowerMsg, kw) {
			log.Printf("Completion keyword '%s' found in message.", kw)
			return true
		}
	}
	return false
}

// truncateForDisplay shortens long strings for cleaner console output.
func truncateForDisplay(s string) string {
	maxLength := 200
	if len(s) > maxLength {
		return s[:maxLength] + "... (truncated)"
	}
	return s
}
