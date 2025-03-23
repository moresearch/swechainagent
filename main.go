package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moresearch/swechainagent/core"
)

func main() {
	// Check command line args
	if len(os.Args) < 2 {
		log.Fatalf("Usage: %s <prompt_file>", os.Args[0])
	}

	// Create context with timeout (2 minutes)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// Get configuration
	config := core.NewConfig()

	// Add prompt file to config
	promptFile := os.Args[1]
	config.PromptFile = promptFile

	// Extract agent name from prompt file (remove .prompt extension)
	agentName := strings.TrimSuffix(filepath.Base(promptFile), ".prompt")
	config.AgentName = agentName

	fmt.Printf("Running as agent: %s\n", agentName)

	// Create agent
	agent, err := core.NewAgent(config)
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}
	defer agent.Close()

	// Load user prompt from file
	userPrompt, err := loadPromptFile(promptFile)
	if err != nil {
		log.Fatalf("Failed to load prompt file: %v", err)
	}

	// Execute the workflow
	if err := agent.Run(ctx, userPrompt); err != nil {
		log.Fatalf("Workflow execution failed: %v", err)
	}
}

// loadPromptFile loads and returns the contents of a prompt file
func loadPromptFile(filename string) (string, error) {
	// Check file exists
	_, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("prompt file not found: %s", filename)
	}

	// Read file contents
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		return "", fmt.Errorf("failed to read prompt file: %v", err)
	}

	return string(content), nil
}
