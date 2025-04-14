package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/ollama"
)

// ToolChoice represents the LLM's decision about which tool to use
type ToolChoice struct {
	ChosenTool string                 `json:"chosen_tool"`
	Arguments  map[string]interface{} `json:"arguments"`
}

func main() {
	// Check if a prompt file path was provided
	if len(os.Args) < 2 {
		log.Fatalf("Usage: %s <prompt_file_path>", os.Args[0])
	}
	promptFilePath := os.Args[1]

	// Read the prompt from the file
	promptTemplate, err := ioutil.ReadFile(promptFilePath)
	if err != nil {
		log.Fatalf("Failed to read prompt file: %v", err)
	}

	// Create a new MCP client with the server data connection
	mcpClient, err := client.NewStdioMCPClient(
		"./bin/swechain-mcp-server",
		[]string{},
	)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer mcpClient.Close()

	// Create a context with a longer timeout for the entire session
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Minute)
	defer cancel()

	// Initialize the MCP client
	fmt.Println("Initializing mcp client...")
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "mcp-curl client",
		Version: "1.0.0",
	}
	initResult, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		log.Fatalf("Failed to initialize: %v", err)
	}
	fmt.Printf(
		"Initialized with server: %s %s\n\n",
		initResult.ServerInfo.Name,
		initResult.ServerInfo.Version,
	)

	// Get the list of tools from the server
	fmt.Println("Available tools...")
	toolsRequest := mcp.ListToolsRequest{}
	tools, err := mcpClient.ListTools(ctx, toolsRequest)
	if err != nil {
		log.Fatalf("Failed to list tools: %v", err)
	}

	// Format tools with detailed information about parameters
	var toolsInfoBuilder strings.Builder
	for _, tool := range tools.Tools {
		// Convert the tool to pretty JSON
		toolJSON, _ := json.MarshalIndent(tool, "", "  ")
		toolsInfoBuilder.WriteString("TOOL:\n")
		toolsInfoBuilder.WriteString(string(toolJSON))
		toolsInfoBuilder.WriteString("\n\n")

		// Add explicit info about required parameters
		toolsInfoBuilder.WriteString("Required parameters for " + tool.Name + ":\n")
		for _, reqParam := range tool.InputSchema.Required {
			if propInfo, exists := tool.InputSchema.Properties[reqParam].(map[string]interface{}); exists {
				description := ""
				if desc, ok := propInfo["description"].(string); ok {
					description = desc
				}
				toolsInfoBuilder.WriteString("- " + reqParam + ": " + description + " (REQUIRED)\n")
			} else {
				toolsInfoBuilder.WriteString("- " + reqParam + " (REQUIRED)\n")
			}
		}

		// Add info about optional parameters
		toolsInfoBuilder.WriteString("\nOptional parameters:\n")
		for paramName, propInfo := range tool.InputSchema.Properties {
			if !contains(tool.InputSchema.Required, paramName) {
				if propMap, ok := propInfo.(map[string]interface{}); ok {
					description := ""
					if desc, ok := propMap["description"].(string); ok {
						description = desc
					}
					toolsInfoBuilder.WriteString("- " + paramName + ": " + description + "\n")
				} else {
					toolsInfoBuilder.WriteString("- " + paramName + "\n")
				}
			}
		}
		toolsInfoBuilder.WriteString("\n---\n\n")
	}

	toolsInfo := toolsInfoBuilder.String()
	fmt.Println("Tool information prepared")
	fmt.Println(toolsInfo)

	// Create an LLM instance
	//llm, err := openai.New()
	//llm, err := openai.New(openai.WithModel("gpt-4o-mini"))
	//llm, err := openai.New(openai.WithModel("gpt-4-turbo"))
	//llm, err := anthropic.New(anthropic.WithModel("claude-3-5-sonnet-20240620"))
	//llm, err := openai.New(openai.WithModel("deepseek-chat"),)

	//llm, err := ollama.New(ollama.WithModel("smollm2:1.7b"))
	//llm, err := ollama.New(ollama.WithModel("qwen2.5-coder:32b"))
	//llm, err := ollama.New(ollama.WithModel("llama3.2:3b-instruct-fp16"))
	//llm, err := ollama.New(ollama.WithModel("command-r7b:7b"))
	//llm, err := ollama.New(ollama.WithModel("llama3.1:8b"))

	//llm, err := anthropic.New(anthropic.WithModel("claude-3-7-sonnet-20250219"))
	//llm, err := ollama.New(ollama.WithModel("deepseek-r1:14b"))
	llm, err := ollama.New(ollama.WithModel("gemma3:12b"))

	//llm, err := ollama.New(ollama.WithModel("cogito:3b"))
	//llm, err := ollama.New(ollama.WithModel("qwen2.5:0.5b"))
	//llm, err := cohere.New()

	if err != nil {
		log.Fatal(err)
	}

	// Create a conversation history to maintain context
	var conversationHistory []map[string]interface{}

	// Main interaction loop
	for {
		// Replace the {{TOOLS_INFO}} placeholder in the prompt template
		promptWithTools := strings.Replace(string(promptTemplate), "{{TOOLS_INFO}}", toolsInfo, 1)

		// Replace the {{CONVERSATION_HISTORY}} placeholder if it exists
		var historyStr string
		if len(conversationHistory) > 0 {
			historyJSON, _ := json.MarshalIndent(conversationHistory, "", "  ")
			historyStr = string(historyJSON)
		} else {
			historyStr = "No previous interactions"
		}
		promptWithHistory := strings.Replace(promptWithTools, "{{CONVERSATION_HISTORY}}", historyStr, 1)

		fmt.Println("\nSending prompt to LLM...")
		// Generate a response from the LLM
		completion, err := llms.GenerateFromSinglePrompt(ctx, llm, promptWithHistory)
		if err != nil {
			log.Fatalf("Failed to get LLM response: %v", err)
		}
		fmt.Println("LLM Response: ", completion)

		// Parse the LLM's response to extract the chosen tool and arguments
		var toolChoice ToolChoice
		err = json.Unmarshal([]byte(strings.TrimSpace(completion)), &toolChoice)
		if err != nil {
			log.Printf("Failed to parse LLM response as JSON: %v", err)
			// Try again after a short delay
			time.Sleep(2 * time.Second)
			continue
		}

		if toolChoice.ChosenTool == "" {
			log.Printf("LLM did not specify a tool to use. Trying again...")
			time.Sleep(2 * time.Second)
			continue
		}

		// Check for exit condition
		if toolChoice.ChosenTool == "exit" {
			fmt.Println("Exiting the loop as requested.")
			break
		}

		// Validate the tool parameters before calling
		selectedTool := findTool(tools.Tools, toolChoice.ChosenTool)
		if selectedTool == nil {
			log.Printf("Selected tool '%s' not found. Trying again...", toolChoice.ChosenTool)
			time.Sleep(2 * time.Second)
			continue
		}

		missingParams := validateRequiredParams(selectedTool, toolChoice.Arguments)
		if len(missingParams) > 0 {
			log.Printf("Missing required parameters: %v. Trying again...", missingParams)
			time.Sleep(2 * time.Second)
			continue
		}

		// Call the tool that the LLM selected
		fmt.Printf("Calling the '%s' tool with arguments: %v\n", toolChoice.ChosenTool, toolChoice.Arguments)
		callRequest := mcp.CallToolRequest{
			Request: mcp.Request{
				Method: "tools/call",
			},
		}
		callRequest.Params.Name = toolChoice.ChosenTool
		callRequest.Params.Arguments = toolChoice.Arguments

		// Call the tool
		result, err := mcpClient.CallTool(ctx, callRequest)
		if err != nil {
			log.Printf("Failed to call the tool: %v. Trying again...", err)
			time.Sleep(2 * time.Second)
			continue
		}

		// Display the content of the result
		fmt.Println("Tool execution result:")
		fmt.Printf("%+v\n", result.Content)

		// Update conversation history
		interaction := map[string]interface{}{
			"tool_called": toolChoice.ChosenTool,
			"arguments":   toolChoice.Arguments,
			"result":      result.Content,
		}
		conversationHistory = append(conversationHistory, interaction)

		// Optional: Add a small delay between iterations
		time.Sleep(1 * time.Second)
	}
	/*
	   inputToken := llms.CountTokens("", input)
	   outputToken := llms.CountTokens("", completion)

	   fmt.Printf("%v/%v\n", inputToken, outputToken)
	*/
}

// Helper function to check if a string is in a slice
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// Helper function to find a tool by name
func findTool(tools []mcp.Tool, name string) *mcp.Tool {
	for _, tool := range tools {
		if tool.Name == name {
			return &tool
		}
	}
	return nil
}

// Helper function to validate that all required parameters are present
func validateRequiredParams(tool *mcp.Tool, args map[string]interface{}) []string {
	var missingParams []string
	for _, required := range tool.InputSchema.Required {
		if _, exists := args[required]; !exists {
			missingParams = append(missingParams, required)
		}
	}
	return missingParams
}
