package core

import (
	"os"
)

// Config holds application configuration
type Config struct {
	OllamaHost    string
	MCPServerPath string
	ChatModel     string
	ToolsModel    string
	AgentName     string
	AgentVersion  string
}

// NewConfig creates a new configuration with default values
func NewConfig() *Config {
	config := &Config{
		OllamaHost:    "http://127.0.0.1:11434",
		MCPServerPath: "./bin/swechain-mcp-server",
		ChatModel:     "llama3.2:3b",
		ToolsModel:    "llama3.2:3b-instruct-fp16",
		AgentName:     "agent",
		AgentVersion:  "1.0.0",
	}

	// Override with environment variables if they exist
	if chatLLM := os.Getenv("CHAT_LLM"); chatLLM != "" {
		config.ChatModel = chatLLM
	}

	if toolsLLM := os.Getenv("TOOLS_LLM"); toolsLLM != "" {
		config.ToolsModel = toolsLLM
	}

	return config
}
