package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ollama/ollama/api"
)

// Agent handles the cognition process and client interaction
type Agent struct {
	OllamaClient     *api.Client
	MCPClient        client.MCPClient
	Config           *Config
	TrajectoryLogger *TrajectoryLogger // New field for trajectory logging
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

	// Extract agent name from config or derive from the prompt file
	agentName := config.AgentName
	if agentName == "" || agentName == "agent" {
		// Try to derive a more specific name
		if config.PromptFile != "" {
			// Extract filename without extension
			base := filepath.Base(config.PromptFile)
			agentName = strings.TrimSuffix(base, filepath.Ext(base))
		}
	}

	// Initialize trajectory logger
	trajLogger, err := NewTrajectoryLogger(agentName)
	if err != nil {
		// Log warning but continue - trajectory logging is non-critical
		fmt.Printf("Warning: Failed to create trajectory logger: %v\n", err)
	}

	return &Agent{
		OllamaClient:     ollamaClient,
		MCPClient:        mcpClient,
		Config:           config,
		TrajectoryLogger: trajLogger,
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

	// Log initialization
	if a.TrajectoryLogger != nil {
		a.TrajectoryLogger.LogEvent("initialize",
			map[string]interface{}{
				"server_name":    initResult.ServerInfo.Name,
				"server_version": initResult.ServerInfo.Version,
			},
			map[string]interface{}{
				"agent_name":    a.Config.AgentName,
				"agent_version": a.Config.AgentVersion,
			})
	}

	// List Tools
	fmt.Println("🛠️ Available tools...")
	toolsRequest := mcp.ListToolsRequest{}
	tools, err := a.MCPClient.ListTools(ctx, toolsRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %v", err)
	}

	// Log available tools
	if a.TrajectoryLogger != nil {
		toolNames := make([]string, len(tools.Tools))
		for i, tool := range tools.Tools {
			toolNames[i] = tool.Name
		}
		a.TrajectoryLogger.LogEvent("tools_available",
			map[string]interface{}{
				"tools": toolNames,
			},
			nil)
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

// ValidateToolArguments checks and fixes common issues with tool arguments
func (a *Agent) ValidateToolArguments(toolName string, arguments map[string]interface{}) map[string]interface{} {
	// Make a copy of arguments to avoid modifying the original
	fixedArgs := make(map[string]interface{})
	for k, v := range arguments {
		fixedArgs[k] = v
	}

	// Special handling for memory tool
	if toolName == "memory" {
		// Ensure it has the required text field
		if _, ok := fixedArgs["text"]; !ok {
			// Add a default text value if missing
			fixedArgs["text"] = fmt.Sprintf("Memory entry from %s at %s",
				a.Config.AgentName, time.Now().Format("2006-01-02 15:04:05"))
			fmt.Printf("Warning: Added missing 'text' parameter to memory tool arguments\n")
		}

		// Ensure it has an operation field
		if _, ok := fixedArgs["operation"]; !ok {
			// Default to 'write' operation
			fixedArgs["operation"] = "write"
			fmt.Printf("Warning: Added missing 'operation' parameter to memory tool arguments\n")
		}
	}

	// Special handling for send tool
	if toolName == "send" {
		// Check for reference to previous tool results in arguments
		for key, value := range fixedArgs {
			if strVal, ok := value.(string); ok {
				if strings.Contains(strings.ToLower(strVal), "result of") ||
					strings.Contains(strings.ToLower(strVal), "addr") {
					// This is a reference to previous results
					fmt.Printf("Warning: Found reference to previous results in argument '%s'. Resolving required.\n", key)
					// Later we can add logic to resolve these references automatically
				}
			}
		}
	}

	// Add more tool-specific validations as needed
	return fixedArgs
}

// GenerateIterationSummary provides a brief summary of the current state using the chat model
func (a *Agent) GenerateIterationSummary(ctx context.Context, iteration int, userPrompt, history string) (string, error) {
	systemSummaryPrompt := `You are an AI assistant providing a concise summary of the agent's progress.
Based on the conversation history, summarize:
1. What has been accomplished so far
2. What remains to be done
3. Any errors or issues that need to be addressed

Keep your summary clear, factual and under 5 sentences.`

	messages := []api.Message{
		{Role: "system", Content: systemSummaryPrompt},
		{Role: "user", Content: fmt.Sprintf("User's original task: %s\n\nConversation history:\n%s",
			userPrompt, history)},
	}

	var FALSE = false
	req := &api.ChatRequest{
		Model:    a.Config.ChatModel,
		Messages: messages,
		Options: map[string]interface{}{
			"temperature":   0.2,
			"repeat_last_n": -1,
		},
		Stream: &FALSE,
	}

	var summary string
	err := a.OllamaClient.Chat(ctx, req, func(resp api.ChatResponse) error {
		summary = resp.Message.Content
		return nil
	})

	if err != nil {
		return fmt.Sprintf("Failed to generate summary: %v", err), err
	}

	return summary, nil
}

// Run executes the full workflow with ReAct loop for iterative reasoning and tool use
func (a *Agent) Run(ctx context.Context, userPrompt string) error {
	// Log the start of the run
	if a.TrajectoryLogger != nil {
		a.TrajectoryLogger.LogEvent("run_start",
			map[string]interface{}{
				"prompt": userPrompt,
				"time":   time.Now().UTC().Format("2006-01-02 15:04:05"),
				"user":   "moresearch",
			},
			map[string]interface{}{
				"models": map[string]string{
					"react": a.Config.ToolsModel,
					"chat":  a.Config.ChatModel,
				},
			})
	}

	// Initialize and get tools
	ollamaTools, err := a.Initialize(ctx)
	if err != nil {
		return fmt.Errorf("initialization failed: %v", err)
	}

	// Get current date and time
	currentTime := time.Now().UTC().Format("2006-01-02 15:04:05")

	// ReAct system prompt with current info
	systemReactPrompt := fmt.Sprintf(`You are a problem-solving agent that follows the ReAct framework:
1. REASONING: Think step-by-step about the current state and what to do next
2. ACTION: Use a tool to make progress
3. OBSERVATION: Review the result
4. Repeat until you've solved the problem

Always start your response with one of:
- "REASONING: <your step-by-step thought process>"
- "ACTION: <tool name>" followed by the specific arguments needed
- "FINAL ANSWER: <your complete solution>" when you've finished the task

Current Date and Time (UTC - YYYY-MM-DD HH:MM:SS formatted): %s
Current User's Login: moresearch

Break down complex problems into steps and solve them methodically.
Be specific with tool arguments and make sure to use correct values.
Do NOT use placeholder text like "result of addr" in your arguments -
instead, use the actual value from previous observations.`, currentTime)

	// Start the conversation with the user prompt
	messages := []api.Message{
		{Role: "system", Content: systemReactPrompt},
		{Role: "user", Content: userPrompt},
	}

	// Track overall agent output for logging
	var fullConversation string

	// Track completion state
	completedTask := false
	maxIterations := 100 // Practically infinite, but with a safety cap

	// Log the ReAct loop start
	if a.TrajectoryLogger != nil {
		a.TrajectoryLogger.LogEvent("react_loop_start",
			map[string]interface{}{
				"max_iterations": maxIterations,
			}, nil)
	}

	// ReAct Loop
	for iteration := 0; iteration < maxIterations && !completedTask; iteration++ {
		// Log the iteration start
		if a.TrajectoryLogger != nil {
			a.TrajectoryLogger.LogEvent("react_iteration",
				map[string]interface{}{
					"number": iteration + 1,
					"of_max": maxIterations,
				}, nil)
		}

		fmt.Printf("\n===== 🔄 ReAct Iteration %d =====\n", iteration+1)

		// Get LLM reasoning or action using the tools model
		var FALSE = false
		req := &api.ChatRequest{
			Model:    a.Config.ToolsModel,
			Messages: messages,
			Options: map[string]interface{}{
				"temperature":   0.2, // Low but non-zero temperature for more deterministic but slightly creative responses
				"repeat_last_n": -1,  // Avoid repetition
			},
			Tools:  ollamaTools,
			Stream: &FALSE,
		}

		// Variables to capture the response
		var agentResponse string
		var toolCallMade bool
		var toolCallName string
		var toolCallArgs map[string]interface{}

		// Get the agent's next step (thinking/reasoning or tool call)
		err := a.OllamaClient.Chat(ctx, req, func(resp api.ChatResponse) error {
			// Store the text response
			agentResponse = resp.Message.Content

			// Check if there's a tool call
			if len(resp.Message.ToolCalls) > 0 {
				toolCallMade = true
				toolCallName = resp.Message.ToolCalls[0].Function.Name
				toolCallArgs = resp.Message.ToolCalls[0].Function.Arguments

				// If the text is empty (pure tool call), generate a placeholder
				if agentResponse == "" {
					agentResponse = fmt.Sprintf("ACTION: Using tool %s", toolCallName)
				}
			}
			return nil
		})

		if err != nil {
			// Log the error
			if a.TrajectoryLogger != nil {
				a.TrajectoryLogger.LogEvent("react_reasoning_error",
					map[string]interface{}{
						"error": err.Error(),
					}, nil)
			}
			return fmt.Errorf("agent reasoning failed: %v", err)
		}

		// Display and log the agent's response
		fmt.Printf("\n🧠 Agent: %s\n", agentResponse)
		fullConversation += fmt.Sprintf("\n--- Agent Iteration %d ---\n%s\n", iteration+1, agentResponse)

		// Check if this is the final answer
		if strings.Contains(strings.ToUpper(agentResponse), "FINAL ANSWER") {
			fmt.Println("\n✅ Agent has completed the task!")
			completedTask = true

			// Log completion
			if a.TrajectoryLogger != nil {
				a.TrajectoryLogger.LogEvent("react_final_answer",
					map[string]interface{}{
						"response":        agentResponse,
						"iterations_used": iteration + 1,
					}, nil)
			}

			// Add the final response to conversation history
			messages = append(messages, api.Message{
				Role:    "assistant",
				Content: agentResponse,
			})

			// Generate final summary
			fmt.Println("\n⏳ Generating final summary...")
			summary, err := a.GenerateIterationSummary(ctx, iteration+1, userPrompt, fullConversation)
			if err == nil {
				fmt.Printf("\n📋 Final Summary: %s\n", summary)

				if a.TrajectoryLogger != nil {
					a.TrajectoryLogger.LogEvent("react_final_summary",
						map[string]interface{}{
							"summary": summary,
						}, nil)
				}
			}

			continue // End the loop
		}

		// Either process the tool call or prompt for next step
		var observation string
		if toolCallMade {
			// Log the tool selection
			if a.TrajectoryLogger != nil {
				argumentsJSON, _ := json.Marshal(toolCallArgs)
				a.TrajectoryLogger.LogEvent("react_tool_selected",
					map[string]interface{}{
						"tool_name": toolCallName,
						"arguments": string(argumentsJSON),
					}, nil)
			}

			// Display tool call information
			fmt.Printf("🛠️ Tool call: %s\n", toolCallName)
			argsJSON, _ := json.Marshal(toolCallArgs)
			fmt.Printf("📝 Arguments: %s\n", string(argsJSON))

			// Validate arguments
			validatedArgs := a.ValidateToolArguments(toolCallName, toolCallArgs)

			// Execute the tool call
			fetchRequest := mcp.CallToolRequest{
				Request: mcp.Request{
					Method: "tools/call",
				},
			}
			fetchRequest.Params.Name = toolCallName
			fetchRequest.Params.Arguments = validatedArgs

			result, err := a.MCPClient.CallTool(ctx, fetchRequest)

			// Handle the tool result
			if err != nil {
				// Tool call failed
				observation = fmt.Sprintf("ERROR: Tool %s failed: %v", toolCallName, err)
				fmt.Printf("⚠️ %s\n", observation)

				// Log the error
				if a.TrajectoryLogger != nil {
					a.TrajectoryLogger.LogEvent("react_tool_error",
						map[string]interface{}{
							"tool_name": toolCallName,
							"error":     err.Error(),
						}, nil)
				}
			} else if len(result.Content) > 0 {
				// Extract text from content
				text, err := ExtractTextContent(result.Content[0])
				if err != nil {
					observation = fmt.Sprintf("ERROR: Couldn't extract result: %v", err)
					fmt.Printf("⚠️ %s\n", observation)

					// Log the extraction error
					if a.TrajectoryLogger != nil {
						a.TrajectoryLogger.LogEvent("react_extraction_error",
							map[string]interface{}{
								"tool_name": toolCallName,
								"error":     err.Error(),
							}, nil)
					}
				} else {
					// Successful result
					observation = text
					fmt.Printf("📊 Result: %s\n", observation)

					// Log successful tool call
					if a.TrajectoryLogger != nil {
						argumentsJSON, _ := json.Marshal(toolCallArgs)
						a.TrajectoryLogger.LogToolCall(
							toolCallName,
							string(argumentsJSON),
							observation,
						)
					}
				}
			} else {
				observation = "Tool returned an empty result."
				fmt.Printf("⚠️ %s\n", observation)
			}

			// Add this exchange to the conversation history
			messages = append(messages, api.Message{
				Role:    "assistant",
				Content: agentResponse,
			})

			// Format the observation with the tool name for clarity
			formattedObservation := fmt.Sprintf("OBSERVATION from %s:\n%s\n\nWhat will you do next?",
				toolCallName, observation)

			messages = append(messages, api.Message{
				Role:    "user",
				Content: formattedObservation,
			})

			// Add to full conversation log
			fullConversation += fmt.Sprintf("\n--- Observation ---\n%s\n", observation)

		} else {
			// No tool call was made, but agent is still thinking or needs a prompt
			messages = append(messages, api.Message{
				Role:    "assistant",
				Content: agentResponse,
			})

			// Prompt the agent to continue if no clear direction
			if !strings.Contains(strings.ToUpper(agentResponse), "REASONING") &&
				!strings.Contains(strings.ToUpper(agentResponse), "ACTION") {
				messages = append(messages, api.Message{
					Role:    "user",
					Content: "Please continue. Either reason about the next step or use a tool action. Remember to be specific with tool arguments.",
				})
			} else {
				// Encourage next step
				messages = append(messages, api.Message{
					Role:    "user",
					Content: "Thanks for your reasoning. What specific action will you take now? Be precise with tool arguments.",
				})
			}
		}

		// Generate summary after each iteration
		fmt.Println("\n⏳ Generating iteration summary...")
		summary, err := a.GenerateIterationSummary(ctx, iteration+1, userPrompt, fullConversation)
		if err == nil {
			fmt.Printf("\n📋 Summary after iteration %d: %s\n", iteration+1, summary)

			if a.TrajectoryLogger != nil {
				a.TrajectoryLogger.LogEvent("react_iteration_summary",
					map[string]interface{}{
						"iteration": iteration + 1,
						"summary":   summary,
					}, nil)
			}
		}

		// Small delay to prevent overwhelming APIs
		time.Sleep(500 * time.Millisecond)
	}

	// Check if we hit the iteration limit without completion
	if !completedTask {
		fmt.Println("\n⚠️ Reached maximum number of iterations without completion.")

		// Log the timeout
		if a.TrajectoryLogger != nil {
			a.TrajectoryLogger.LogEvent("react_iteration_limit",
				map[string]interface{}{
					"max_iterations": maxIterations,
				}, nil)
		}

		// Force a summary/final answer
		fmt.Println("Requesting final summary...")

		messages = append(messages, api.Message{
			Role:    "user",
			Content: "We've reached the maximum number of steps. Please provide your FINAL ANSWER summarizing what you've learned and accomplished.",
		})

		// Get the final summary
		var FALSE = false
		reqSummary := &api.ChatRequest{
			Model:    a.Config.ToolsModel,
			Messages: messages,
			Options: map[string]interface{}{
				"temperature":   0.0,
				"repeat_last_n": -1,
			},
			Stream: &FALSE,
		}

		finalSummary := ""
		err := a.OllamaClient.Chat(ctx, reqSummary, func(resp api.ChatResponse) error {
			finalSummary = resp.Message.Content
			return nil
		})

		if err == nil && finalSummary != "" {
			fmt.Printf("\n✅ Final Summary:\n%s\n", finalSummary)
			fullConversation += fmt.Sprintf("\n--- Final Summary ---\n%s\n", finalSummary)
		}
	}

	// Log the complete conversation
	if a.TrajectoryLogger != nil {
		a.TrajectoryLogger.LogEvent("react_completion",
			map[string]interface{}{
				"success":      completedTask,
				"iterations":   maxIterations,
				"conversation": fullConversation,
			}, nil)
	}

	return nil
}
