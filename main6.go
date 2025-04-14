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

// --- Global Regex Definitions ---
var functionNameRegex = regexp.MustCompile(`(?s)\{\s*"function"\s*:\s*\{\s*"name"\s*:\s*"([^"]+)"\s*,\s*"arguments"\s*:\s*(\{.*?\})\s*\}\s*\}`)
var chosenToolRegex = regexp.MustCompile(`(?s)\{\s*"chosen_tool"\s*:\s*"([^"]+)"\s*,\s*"arguments"\s*:\s*(\{.*?\})\s*\}`)
var codeBlockJsonRegex = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{(?:[^{}]|\\{[^{}]*\\})*\\})\\s*```")

// --- Constants for Focus State ---
const (
	focusAuction = "auction"
	focusBid     = "bid"
)

// Config holds the core configuration for the executor.
type Config struct {
	Model            string
	OllamaURL        string
	MCP_Server       string
	MaxStepsPerCycle int // Renamed from MaxSteps
	ContextSize      int
	Temperature      float64
	LoopDelaySeconds int    // Delay between cycles
	AuctioningPrompt string // Prompt focusing on creating auctions
	BiddingPrompt    string // Prompt focusing on placing bids
}

// Executor manages the interaction between the LLM and the MCP tools.
type Executor struct {
	mcpClient *client.StdioMCPClient
	ollamaAPI *api.Client
	tools     []mcp.Tool
	config    Config
}

// ToolCall represents the expected JSON structure for a tool call from the LLM.
type ToolCall struct {
	Function struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	} `json:"function"`
}

func main() {
	setupConsoleLogger()
	log.Println("INFO: Starting SWEChain Tool Executor (Infinite Loop - Alternating Focus Mode)")

	if len(os.Args) < 2 {
		log.Fatal("FATAL: Usage: ./executor <initial_prompt_file>")
	}
	initialPromptFile := os.Args[1]
	log.Printf("INFO: Reading initial prompt from file: %s", initialPromptFile)

	initialPromptContentBytes, err := ioutil.ReadFile(initialPromptFile)
	if err != nil {
		log.Fatalf("FATAL: Failed to read initial prompt file %s: %v", initialPromptFile, err)
	}
	initialPromptContent := string(initialPromptContentBytes)
	log.Printf("INFO: Successfully read initial prompt (%d bytes).", len(initialPromptContent))

	// Configuration with alternating prompts and loop delay
	config := Config{
		Model:            "llama3.1:8b-instruct-q8_0",
		OllamaURL:        "http://localhost:11434",
		MCP_Server:       os.Getenv("MCP_SERVER"),
		MaxStepsPerCycle: 15, // Max steps within a single cycle
		ContextSize:      4096,
		Temperature:      0.1,
		LoopDelaySeconds: 1, // Wait 1seconds between cycles
		// Refined prompts emphasizing keyring requirement
		AuctioningPrompt: "Identify suitable open issues and create auctions for them using the 'open-auction' tool. Prioritize issues that seem valuable or urgent. **CRITICAL: The 'from' address MUST correspond to a key available in the agent's keyring.** If no suitable issues are found, state that clearly.",
		BiddingPrompt:    "Review open auctions using the 'query-auctions' tool. Identify auctions you can realistically complete and place competitive bids using the 'create-bid' tool. Consider auction descriptions and starting bids. **CRITICAL: The 'bidder' and 'from' addresses MUST correspond to keys available in the agent's keyring.** If no suitable auctions to bid on are found, state that clearly.",
	}
	if config.MCP_Server == "" {
		config.MCP_Server = "./bin/swechain-mcp-server"
		log.Println("INFO: MCP_SERVER environment variable not set, using default:", config.MCP_Server)
	} else {
		log.Println("INFO: Using MCP_SERVER from environment variable:", config.MCP_Server)
	}

	log.Printf("INFO: Configuration loaded: Model=%s, Ollama=%s, MCP=%s, MaxStepsPerCycle=%d, LoopDelay=%ds",
		config.Model, config.OllamaURL, config.MCP_Server, config.MaxStepsPerCycle, config.LoopDelaySeconds)
	log.Printf("INFO: Auctioning Prompt: %s", config.AuctioningPrompt)
	log.Printf("INFO: Bidding Prompt: %s", config.BiddingPrompt)

	// Initialize executor
	log.Println("INFO: Initializing executor...")
	exec, err := newExecutor(config)
	if err != nil {
		log.Fatalf("FATAL: Executor initialization failed: %v", err)
	}
	log.Println("INFO: Executor initialized successfully.")
	defer exec.close()

	// --- Infinite Loop with Alternating Focus ---
	currentPrompt := initialPromptContent // Start with the prompt from the file
	cycleCount := 0
	nextCycleFocus := focusAuction // Start by focusing on auctioning after the initial prompt

	for { // Infinite loop starts here
		cycleCount++
		log.Printf("INFO: ===== Starting Execution Cycle %d =====", cycleCount)
		fmt.Printf("\n===== Starting Execution Cycle %d =====\n", cycleCount)

		// Determine the prompt for this cycle
		if cycleCount > 1 { // After the first cycle, use the alternating focus
			log.Printf("INFO: Cycle %d Focus: %s", cycleCount, nextCycleFocus)
			if nextCycleFocus == focusAuction {
				currentPrompt = config.AuctioningPrompt
			} else { // focusBid
				currentPrompt = config.BiddingPrompt
			}
		} else {
			log.Printf("INFO: Cycle %d Focus: Initial Prompt from file", cycleCount)
			// currentPrompt is already set to initialPromptContent
		}

		// Create a new context for each cycle
		cycleCtx := context.Background()

		log.Printf("INFO: Cycle %d: Using prompt (length %d)", cycleCount, len(currentPrompt)) // Don't log full prompt here
		// Execute the cycle logic (previously runSession)
		err := exec.runExecutionCycle(cycleCtx, currentPrompt)

		// Log cycle outcome
		if err != nil {
			// Log error from the cycle (e.g., max steps reached within cycle), but continue the outer loop
			log.Printf("ERROR: Cycle %d encountered an error: %v", cycleCount, err)
			fmt.Printf("\nCycle %d encountered an error: %v\n", cycleCount, err)
		} else {
			log.Printf("INFO: Cycle %d completed successfully.", cycleCount)
			fmt.Printf("\nCycle %d completed.\n", cycleCount)
		}

		// Toggle the focus for the NEXT cycle
		if nextCycleFocus == focusAuction {
			nextCycleFocus = focusBid
		} else {
			nextCycleFocus = focusAuction
		}
		log.Printf("INFO: Set next cycle focus to: %s", nextCycleFocus)

		// Wait before starting the next cycle
		log.Printf("INFO: Waiting %d seconds before next cycle...", config.LoopDelaySeconds)
		time.Sleep(time.Duration(config.LoopDelaySeconds) * time.Second)
	}
	// --- End of Infinite Loop ---
}

// setupConsoleLogger configures basic logging to stdout without file paths.
func setupConsoleLogger() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ldate | log.Ltime) // Removed Lshortfile
}

// newExecutor creates and initializes a new Executor instance.
func newExecutor(config Config) (*Executor, error) {
	log.Println("INFO: Attempting to connect to MCP server via stdio:", config.MCP_Server)
	mcpClient, err := client.NewStdioMCPClient(config.MCP_Server, []string{})
	if err != nil {
		log.Printf("ERROR: Failed to create MCP client: %v", err)
		return nil, fmt.Errorf("failed to create MCP client: %w", err)
	}
	log.Println("INFO: MCP client created successfully.")

	log.Println("INFO: Initializing MCP protocol...")
	initCtx, cancelInit := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelInit()
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "swechain-executor",
		Version: "0.7.0-alt-loop-final", // Updated version reflecting loop and refinements
	}
	log.Printf("INFO: Sending MCP Initialize request: %+v", initRequest.Params)
	initResult, err := mcpClient.Initialize(initCtx, initRequest)
	if err != nil {
		log.Printf("ERROR: Failed to initialize MCP protocol: %v", err)
		mcpClient.Close()
		return nil, fmt.Errorf("failed to initialize MCP protocol: %w", err)
	}
	log.Printf("INFO: MCP protocol initialized successfully. Server Info: Name=%s, Version=%s",
		initResult.ServerInfo.Name, initResult.ServerInfo.Version)

	log.Println("INFO: Setting up Ollama API client for URL:", config.OllamaURL)
	ollamaURL, err := url.Parse(config.OllamaURL)
	if err != nil {
		log.Printf("ERROR: Invalid Ollama URL %s: %v", config.OllamaURL, err)
		mcpClient.Close()
		return nil, fmt.Errorf("invalid Ollama URL %s: %w", config.OllamaURL, err)
	}

	ollamaClientTimeout := 20 * time.Minute
	log.Printf("INFO: Setting Ollama HTTP client timeout to %v.", ollamaClientTimeout)
	httpClient := &http.Client{
		Timeout: ollamaClientTimeout,
	}
	ollamaAPI := api.NewClient(ollamaURL, httpClient)
	log.Println("INFO: Ollama API client created.")

	log.Println("INFO: Fetching available tools from MCP server...")
	listToolsCtx, cancelList := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelList()
	toolsRequest := mcp.ListToolsRequest{}
	toolsResponse, err := mcpClient.ListTools(listToolsCtx, toolsRequest)
	if err != nil {
		log.Printf("ERROR: Failed to list tools from MCP server: %v", err)
		mcpClient.Close()
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}
	log.Printf("INFO: Successfully fetched %d available tools from MCP server.", len(toolsResponse.Tools))
	if len(toolsResponse.Tools) > 0 {
		log.Println("INFO: Available tools:")
		for i, tool := range toolsResponse.Tools {
			log.Printf("  [%d] Name: %s", i+1, tool.Name)
		}
	} else {
		log.Println("WARN: No tools reported by the MCP server.")
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
	log.Println("INFO: Shutting down executor, closing connections...")
	if e.mcpClient != nil {
		log.Println("INFO: Closing MCP client connection.")
		if err := e.mcpClient.Close(); err != nil {
			log.Printf("WARN: Error encountered while closing MCP client: %v", err)
		} else {
			log.Println("INFO: MCP client connection closed successfully.")
		}
	}
	log.Println("INFO: Executor shutdown complete.")
}

// runExecutionCycle (renamed from runSession) executes one cycle of the ReAct loop.
func (e *Executor) runExecutionCycle(ctx context.Context, cyclePrompt string) error {
	log.Println("INFO: Formatting tool information for cycle prompt.")
	toolsInfo := formatToolsInfo(e.tools)
	promptWithTools := strings.Replace(cyclePrompt, "{{TOOLS_INFO}}", toolsInfo, 1)
	// Don't log full prompt here, can be huge
	log.Printf("INFO: Cycle prompt prepared with tool info (Prompt length: %d bytes).", len(promptWithTools))

	log.Println("INFO: Converting MCP tool definitions to Ollama format for cycle.")
	ollamaTools := convertToOllamaTools(e.tools)
	log.Printf("INFO: Converted %d tools to Ollama format.", len(ollamaTools))

	// --- Refined System Prompt (Kept from previous version) ---
	systemPrompt := `You are a helpful and meticulous assistant interacting with the SWEChain environment using available tools.
Your primary goal is to follow the user's request, utilizing the provided tools when necessary.

**Tool Usage Rules:**
1.  Analyze the request and available tools carefully.
2.  If a tool is needed, you MUST respond ONLY with a single JSON object representing the tool call.
3.  The JSON object MUST use the exact structure: ` + "`{\"function\": {\"name\": \"TOOL_NAME\", \"arguments\": {ARGUMENTS_MAP}}}`" + `.
4.  Do NOT include any other text, explanations, or markdown formatting (like ` + "```json" + `) before or after the JSON object.
5.  Pay close attention to required arguments. Blockchain addresses (like 'from', 'bidder', 'winner') MUST start with 'cosmos1...' and correspond to a key available in the agent's keyring. **CRITICAL: The 'from' address MUST have a corresponding private key accessible to the agent, otherwise the transaction will fail.**
6.  If the request can be fulfilled without a tool, or if you need to report findings/status after using a tool, respond with a clear text message.

**Tool Call Examples:**

**CORRECT Example 1 (create-bid):** Note the valid 'cosmos1...' addresses.
` + "```json" + `
{
  "function": {
    "name": "create-bid",
    "arguments": {
      "bidder": "cosmos1aqq8kv2m3zmnzr7k7z34j0zrsvl9p7un2xn4kf",
      "amount": "550",
      "auctionId": "auction-projX-456",
      "from": "cosmos1aqq8kv2m3zmnzr7k7z34j0zrsvl9p7un2xn4kf"
    }
  }
}
` + "```" + `

**CORRECT Example 2 (open-auction):** Using 'open-auction'. The 'from' address must exist in the keyring.
` + "```json" + `
{
  "function": {
    "name": "open-auction",
    "arguments": {
      "issue": "megacorp/project-gamma/issues/7",
      "description": "Open auction for bug fix related to issue #7.",
      "status": "open",
      "winner": "",
      "from": "cosmos1zxcvbnm098765asdfghjklqwertyuiopmnbvcxza"
    }
  }
}
` + "```" + `

**CORRECT Example 3 (query-auctions):** Querying doesn't require a 'from' address.
` + "```json" + `
{
  "function": {
    "name": "query-auctions",
    "arguments": {
      "status": "open"
    }
  }
}
` + "```" + `

**INCORRECT Example 1 (Wrong Keys):** Do NOT use "chosen_tool". Use "function" containing "name" and "arguments".
` + "```json" + `
// Incorrect: Uses "chosen_tool" key.
{
  "chosen_tool": "some-tool",
  "arguments": { ... }
}
` + "```" + `

**INCORRECT Example 2 (Invalid Address Format):** 'from' requires a valid blockchain address starting with 'cosmos1...', not just a name.
` + "```json" + `
// Incorrect: "from" value 'MyAgentIdentifier' is NOT a valid 'cosmos1...' address.
{
  "function": {
    "name": "open-auction",
    "arguments": {
      "issue": "owner/repo/issues/123",
      "description": "Auction description",
      "from": "MyAgentIdentifier"
    }
  }
}
` + "```" + `

**INCORRECT Example 3 (Address Key Likely Missing):** Even if the address format is correct, the transaction will fail if the agent doesn't have the private key for 'cosmos1nosuchkey...' in its keyring.
` + "```json" + `
// Incorrect: Agent likely doesn't have the key for 'cosmos1nosuchkey...'.
{
  "function": {
    "name": "create-bid",
    "arguments": {
      "bidder": "cosmos1nosuchkey...",
      "amount": "100",
      "auctionId": "auction-abc-123",
      "from": "cosmos1nosuchkey..."
    }
  }
}
` + "```" + `

**CORRECT Text Response Example (No Tool Needed):**
If the request asks for a status update after you've queried, or if no action is possible.
` + "```text" + `
I have queried the open auctions. There are currently 5 open auctions. Auction 'auction-def-456' has the highest bid at 300token. No suitable actions to take based on the current request.
` + "```" + `

Now, proceed with the user's request based on these instructions and the available tools list.`
	// --- End of Refined System Prompt ---

	// Initialize messages for this cycle
	messages := []api.Message{
		{
			Role:    "system",
			Content: systemPrompt,
		},
		{
			Role:    "user",
			Content: promptWithTools, // Cycle-specific prompt with tools info
		},
	}
	log.Println("INFO: Initial conversation history prepared for this cycle.")

	// ReAct loop for the *current cycle*
	for step := 1; step <= e.config.MaxStepsPerCycle; step++ {
		// Use the context passed into this function (cycleCtx)
		stepCtx := ctx

		log.Printf("INFO: --- Cycle Step %d/%d ---", step, e.config.MaxStepsPerCycle)
		fmt.Printf("\n--- Cycle Step %d ---\n", step) // Console step separator

		// 1. Get LLM Response
		log.Println("INFO: Querying LLM for next action/response...")
		responseContent, err := e.getModelResponse(stepCtx, messages, ollamaTools)
		if err != nil {
			log.Printf("ERROR: Failed to get model response in step %d: %v", step, err)
			// Return error to indicate cycle failure due to LLM communication issue
			return fmt.Errorf("LLM communication failed in step %d: %w", step, err)
		}
		log.Printf("INFO: Received LLM response (length: %d chars):\n%s", len(responseContent), responseContent)
		fmt.Printf("Assistant:\n%s\n", responseContent)

		messages = append(messages, api.Message{Role: "assistant", Content: responseContent})
		log.Println("INFO: Added assistant response to conversation history.")

		// 2. Extract Tool Call
		log.Println("INFO: Attempting to extract tool call from LLM response...")
		toolCall, err := extractToolCall(responseContent)
		if err != nil {
			log.Println("INFO: No valid tool call JSON found in the response.")
			if containsCompletion(responseContent) {
				log.Println("INFO: Completion indicators found and no tool call detected. Cycle task considered complete.")
				return nil // Cycle completed successfully
			}
			log.Println("INFO: No tool call detected, proceeding to next reasoning step (if any).")
			continue // Let LLM reason further in the next step of this cycle
		}
		log.Printf("INFO: Successfully extracted tool call for '%s'.", toolCall.Function.Name)

		// 3. Execute Tool
		toolName := toolCall.Function.Name
		argsJSON, err := json.MarshalIndent(toolCall.Function.Arguments, "", "  ")
		if err != nil {
			log.Printf("WARN: Failed to marshal arguments for logging: %v", err)
			argsJSON = []byte("{...error marshalling args...}")
		}
		log.Printf("INFO: Preparing to execute tool '%s' with arguments:\n%s", toolName, string(argsJSON))
		fmt.Printf("\n[Using tool: %s with args:\n%s\n]\n", toolName, string(argsJSON))

		toolResult, err := e.executeTool(stepCtx, toolName, toolCall.Function.Arguments)
		if err != nil {
			log.Printf("ERROR: Tool execution failed for '%s': %v", toolName, err)
			fmt.Printf("[Tool Error: %v]\n", err)
			messages = append(messages, api.Message{
				Role:    "user",
				Content: fmt.Sprintf("Error executing tool %s: %v. Please analyze the error and decide the next step. Remember the 'from' address MUST have a key in the agent's keyring.", toolName, err),
			})
			log.Println("INFO: Added tool execution error to conversation history.")
			continue // Let LLM react to the tool error in the next step
		}

		log.Printf("INFO: Tool '%s' executed successfully. Result length: %d chars", toolName, len(toolResult))
		fmt.Printf("[Tool Result:\n%s\n]\n", toolResult)

		// 4. Add Tool Result to Context
		messages = append(messages, api.Message{
			Role:    "user",
			Content: fmt.Sprintf("Result from tool %s:\n%s", toolName, toolResult),
		})
		log.Printf("INFO: Added successful result of tool '%s' to conversation history.", toolName)

	} // End of steps within a cycle

	log.Printf("WARN: Reached maximum steps (%d) for this execution cycle without explicit completion.", e.config.MaxStepsPerCycle)
	// Return an error indicating max steps reached for this specific cycle
	return fmt.Errorf("max steps (%d) reached for cycle without explicit completion", e.config.MaxStepsPerCycle)
}

// getModelResponse (Kept from previous version - handles history trimming, Ollama call)
func (e *Executor) getModelResponse(ctx context.Context, messages []api.Message, tools []api.Tool) (string, error) {
	maxMessages := 20
	if len(messages) > maxMessages {
		log.Printf("INFO: Trimming conversation history from %d to %d messages.", len(messages), maxMessages)
		messages = append([]api.Message{messages[0]}, messages[len(messages)-maxMessages+1:]...)
	} else {
		log.Printf("INFO: Conversation history length (%d messages) within limit (%d).", len(messages), maxMessages)
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
	log.Printf("INFO: Sending Chat request to Ollama model '%s'. Messages count: %d", req.Model, len(req.Messages))

	startTime := time.Now()
	err := e.ollamaAPI.Chat(ctx, req, func(resp api.ChatResponse) error {
		response = resp
		return nil
	})
	duration := time.Since(startTime)

	if err != nil {
		log.Printf("ERROR: Ollama API call failed after %v: %v", duration, err)
		return "", fmt.Errorf("ollama API error: %w", err)
	}
	log.Printf("INFO: Ollama API call completed successfully in %v.", duration)

	if response.Message.Content == "" {
		log.Println("WARN: Ollama response message content is empty.")
		if len(response.Message.ToolCalls) > 0 {
			toolCallData, _ := json.Marshal(response.Message.ToolCalls[0])
			log.Printf("INFO: Found tool call directly in Ollama response.ToolCalls structure: %s", string(toolCallData))
			argsBytes, err := json.Marshal(response.Message.ToolCalls[0].Function.Arguments)
			if err != nil {
				log.Printf("ERROR: Failed to marshal arguments from Ollama ToolCalls structure: %v", err)
				return "", nil
			}
			reconstructedJson := fmt.Sprintf(`{"function": {"name": "%s", "arguments": %s}}`,
				response.Message.ToolCalls[0].Function.Name,
				string(argsBytes))
			log.Println("INFO: Reconstructed JSON from ToolCalls structure for parser.")
			return reconstructedJson, nil
		} else {
			log.Println("INFO: Ollama response content is empty and no direct tool call found in ToolCalls structure.")
			return "", nil
		}
	}

	log.Printf("INFO: Ollama response content length: %d bytes.", len(response.Message.Content))
	return response.Message.Content, nil
}

// extractToolCall (Kept from previous version - handles multiple formats)
func extractToolCall(response string) (*ToolCall, error) {
	log.Printf("DEBUG: Attempting to extract tool call from response:\n%s\n", response)

	var potentialJson string
	var toolName string
	var argumentsJson string

	matches := functionNameRegex.FindStringSubmatch(response)
	if len(matches) == 3 {
		log.Println("DEBUG: Found 'function/name' format directly.")
		potentialJson = matches[0]
		toolName = matches[1]
		argumentsJson = matches[2]
	} else {
		matches = chosenToolRegex.FindStringSubmatch(response)
		if len(matches) == 3 {
			log.Println("DEBUG: Found 'chosen_tool' format directly.")
			potentialJson = matches[0]
			toolName = matches[1]
			argumentsJson = matches[2]
		} else {
			log.Println("DEBUG: Direct formats not found, checking markdown code blocks...")
			codeBlockMatches := codeBlockJsonRegex.FindStringSubmatch(response)
			if len(codeBlockMatches) > 1 {
				jsonInBlock := codeBlockMatches[1]
				log.Printf("DEBUG: Found JSON in code block: %s", jsonInBlock)
				matches = functionNameRegex.FindStringSubmatch(jsonInBlock)
				if len(matches) == 3 {
					log.Println("DEBUG: Found 'function/name' format inside code block.")
					potentialJson = jsonInBlock
					toolName = matches[1]
					argumentsJson = matches[2]
				} else {
					matches = chosenToolRegex.FindStringSubmatch(jsonInBlock)
					if len(matches) == 3 {
						log.Println("DEBUG: Found 'chosen_tool' format inside code block.")
						potentialJson = jsonInBlock
						toolName = matches[1]
						argumentsJson = matches[2]
					}
				}
			}
		}
	}

	if toolName == "" || argumentsJson == "" {
		log.Println("DEBUG: No known tool call JSON structure found.")
		return nil, fmt.Errorf("no known tool call JSON structure found")
	}

	log.Printf("DEBUG: Identified Tool Name: %s", toolName)
	log.Printf("DEBUG: Identified Arguments JSON string: %s", argumentsJson)

	var argumentsMap map[string]interface{}
	decoder := json.NewDecoder(strings.NewReader(argumentsJson))
	if err := decoder.Decode(&argumentsMap); err != nil {
		log.Printf("ERROR: Failed to decode arguments JSON using strict decoder: %v. JSON string was: %s. Full matched JSON context: %s", err, argumentsJson, potentialJson)
		if errFallback := json.Unmarshal([]byte(argumentsJson), &argumentsMap); errFallback != nil {
			log.Printf("ERROR: Fallback unmarshal also failed for arguments: %v", errFallback)
			return nil, fmt.Errorf("failed to parse tool arguments JSON: %w (primary decode error: %v)", errFallback, err)
		}
		log.Println("WARN: Strict JSON decoding failed, but fallback unmarshal succeeded for arguments.")
	}

	if argumentsMap == nil {
		argumentsMap = make(map[string]interface{})
		log.Println("DEBUG: Arguments map was nil after parsing, initialized to empty map.")
	}

	toolCall := &ToolCall{
		Function: struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}{
			Name:      toolName,
			Arguments: argumentsMap,
		},
	}

	log.Printf("INFO: Successfully parsed tool call: Name=%s, Args=%+v", toolCall.Function.Name, toolCall.Function.Arguments)
	return toolCall, nil
}

// executeTool (Kept from previous version - handles MCP call and error reporting)
func (e *Executor) executeTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	fetchRequest := mcp.CallToolRequest{
		Request: mcp.Request{Method: "tools/call"},
	}
	fetchRequest.Params.Name = toolName
	fetchRequest.Params.Arguments = args

	log.Printf("INFO: Calling MCP tool '%s' via client...", toolName)
	toolCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()

	startTime := time.Now()
	result, err := e.mcpClient.CallTool(toolCtx, fetchRequest)
	duration := time.Since(startTime)

	if err != nil {
		log.Printf("ERROR: MCP client communication failed for tool '%s' after %v: %v", toolName, duration, err)
		return "", fmt.Errorf("MCP client failed for tool '%s': %w", toolName, err)
	}
	log.Printf("INFO: MCP client received response for tool '%s' in %v.", toolName, duration)

	if result.IsError {
		log.Printf("ERROR: MCP tool '%s' reported an error from the server.", toolName)
		errorMsg := fmt.Sprintf("Tool '%s' failed with an unspecified error from the server.", toolName)
		if len(result.Content) > 0 {
			contentItem := result.Content[0]
			if textContent, ok := contentItem.(mcp.TextContent); ok {
				if textContent.Text != "" {
					errorMsg = textContent.Text
					log.Printf("INFO: Server error details (TextContent): %s", errorMsg)
				} else {
					log.Printf("WARN: Server error response (TextContent) has empty 'text' field.")
					errorBytes, jsonErr := json.Marshal(contentItem)
					if jsonErr == nil {
						errorMsg = fmt.Sprintf("Tool '%s' failed. Server error content (empty text field): %s", toolName, string(errorBytes))
					} else {
						errorMsg = fmt.Sprintf("Tool '%s' failed with empty text field and unmarshallable error content.", toolName)
					}
				}
			} else {
				log.Printf("WARN: Server error content is not TextContent (type: %T). Marshalling content.", contentItem)
				errorBytes, jsonErr := json.MarshalIndent(contentItem, "", "  ")
				if jsonErr == nil {
					errorMsg = fmt.Sprintf("Tool '%s' failed. Server error content:\n%s", toolName, string(errorBytes))
				} else {
					errorMsg = fmt.Sprintf("Tool '%s' failed with unmarshallable error content (type %T).", toolName, contentItem)
					log.Printf("ERROR: Failed to marshal non-TextContent error content: %v", jsonErr)
				}
			}
		} else {
			log.Println("WARN: Server error response contained no content items.")
			errorMsg = fmt.Sprintf("Tool '%s' failed with no error details provided by the server.", toolName)
		}
		return "", fmt.Errorf(errorMsg)
	}

	log.Println("INFO: Processing successful tool result...")
	toolResult := "Tool executed successfully but returned no content."
	if len(result.Content) > 0 {
		contentItem := result.Content[0]
		if textContent, ok := contentItem.(mcp.TextContent); ok {
			toolResult = textContent.Text
			log.Printf("INFO: Successfully extracted TextContent result (%d bytes).", len(toolResult))
		} else {
			log.Printf("INFO: Tool result content is not TextContent (type: %T), marshalling as JSON.", contentItem)
			resultBytes, err := json.MarshalIndent(contentItem, "", "  ")
			if err != nil {
				log.Printf("ERROR: Failed to marshal non-text tool result content: %v", err)
				toolResult = "[Error marshalling tool result]"
			} else {
				toolResult = string(resultBytes)
				log.Printf("INFO: Successfully marshalled non-TextContent result (%d bytes).", len(toolResult))
			}
		}
	} else {
		log.Println("INFO: Tool executed successfully but server returned no content items.")
	}

	log.Printf("INFO: Tool execution complete for '%s'. Result length: %d chars", toolName, len(toolResult))
	return toolResult, nil
}

// formatToolsInfo (Kept from previous version)
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
					log.Printf("WARN: Invalid schema format for parameter '%s' in tool '%s'", name, tool.Name)
					sb.WriteString(fmt.Sprintf("    - %s: (Invalid schema format)\n", name))
				}
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// convertToOllamaTools (Kept from previous version)
func convertToOllamaTools(tools []mcp.Tool) []api.Tool {
	ollamaTools := make([]api.Tool, 0, len(tools))
	for _, tool := range tools {
		properties := make(map[string]struct {
			Type        string   `json:"type"`
			Description string   `json:"description"`
			Enum        []string `json:"enum,omitempty"`
		})
		for name, propInterface := range tool.InputSchema.Properties {
			if propMap, ok := propInterface.(map[string]interface{}); ok {
				prop := struct {
					Type        string   `json:"type"`
					Description string   `json:"description"`
					Enum        []string `json:"enum,omitempty"`
				}{
					Type:        getStringMap(propMap, "type", "string"),
					Description: getStringMap(propMap, "description", ""),
				}
				if enumRaw, ok := propMap["enum"].([]interface{}); ok {
					for _, e := range enumRaw {
						if str, ok := e.(string); ok {
							prop.Enum = append(prop.Enum, str)
						}
					}
				}
				properties[name] = prop
			} else {
				log.Printf("WARN: Invalid schema format for parameter '%s' in tool '%s' during Ollama conversion", name, tool.Name)
			}
		}
		ollamaTool := api.Tool{
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

// getStringMap (Kept from previous version)
func getStringMap(m map[string]interface{}, key string, defaultValue string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return defaultValue
}

// containsCompletion (Kept from previous version - checks for completion keywords)
func containsCompletion(message string) bool {
	lowerMsg := strings.ToLower(message)
	keywords := []string{
		"conclusion", "final answer", "task complete", "request fulfilled",
		"summarize", "summary:", "all done", "no further action",
		"no actions needed", "no suitable auctions", "no suitable issues",
	}
	for _, kw := range keywords {
		if strings.Contains(lowerMsg, kw) {
			if !strings.Contains(message, `"function":`) && !strings.Contains(message, `"chosen_tool":`) {
				log.Printf("DEBUG: Potential completion keyword '%s' found in message.", kw)
				return true
			} else {
				log.Printf("DEBUG: Keyword '%s' ignored as it might be within a tool call structure.", kw)
			}
		}
	}
	return false
}

// Removed truncateForDisplay function
