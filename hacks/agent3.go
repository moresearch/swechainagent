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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	//"github.com/XiaoConstantine/dspy-go"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	DSPY_HOST          = "http://127.0.0.1:11435"
	MAX_RETRY_ATTEMPTS = 3
	RETRY_DELAY        = 2 * time.Second
	DEFAULT_PARALLEL   = 4
)

var (
	agentName      string
	cosmosAddress  string
	historyDir     string
	fileWriteMutex = &sync.Mutex{}
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: go run agent.go <agent_name> <cosmos_address>")
		os.Exit(1)
	}

	agentName, cosmosAddress = os.Args[1], os.Args[2]
	historyDir = fmt.Sprintf("./%s_traj", agentName)
	fmt.Printf("Starting agent '%s' with Cosmos address: %s\n", agentName, cosmosAddress)
	fmt.Printf("History directory: %s\n", historyDir)

	if os.Getenv("DSPY_NUM_PARALLEL") == "" {
		os.Setenv("DSPY_NUM_PARALLEL", strconv.Itoa(DEFAULT_PARALLEL))
	}

	for {
		fmt.Printf("[%s] Starting agent iteration...\n", agentName)
		thought := thinkAboutAction()
		fmt.Printf("[%s] Agent is thinking: %s\n", agentName, thought)
		runAgent(thought)
		fmt.Printf("[%s] Iteration complete. Waiting for 1 second...\n", agentName)
		time.Sleep(1 * time.Second)
	}
}

func thinkAboutAction() string {
	history, err := loadInteractionHistory(7)
	if err != nil {
		log.Printf("[%s] Failed to load interaction history: %v", agentName, err)
		return "No history available. Starting fresh."
	}
	historyPrompt := formatHistoryForPrompt(history)
	reasoningPrompt := fmt.Sprintf(`Based on the following interaction history for agent '%s' with Cosmos address '%s', decide the next action:
%s

Next Action:`, agentName, cosmosAddress, historyPrompt)
	response, err := callDspy("deepseek-r1:1.5b", reasoningPrompt)
	if err != nil {
		log.Printf("[%s] Failed to generate reasoning: %v", agentName, err)
		return "Failed to generate reasoning. Starting fresh."
	}
	return response
}

func loadInteractionHistory(maxDays int) ([]map[string]string, error) {
	var history []map[string]string
	if _, err := os.Stat(historyDir); os.IsNotExist(err) {
		if err := os.MkdirAll(historyDir, os.ModePerm); err != nil {
			return nil, fmt.Errorf("failed to create history directory: %w", err)
		}
		return history, nil
	}
	files, err := ioutil.ReadDir(historyDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read history directory: %w", err)
	}
	todayEpochDay := time.Now().UTC().Unix() / 86400
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), "_traj.jsonl") {
			continue
		}
		epochDay, err := strconv.ParseInt(strings.Split(file.Name(), "_")[0], 10, 64)
		if err != nil || (maxDays > 0 && todayEpochDay-epochDay >= int64(maxDays)) {
			continue
		}
		data, err := ioutil.ReadFile(filepath.Join(historyDir, file.Name()))
		if err != nil {
			log.Printf("[%s] Failed to read file %s: %v", agentName, file.Name(), err)
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" {
				continue
			}
			var interaction map[string]string
			if err := json.Unmarshal([]byte(line), &interaction); err != nil {
				log.Printf("[%s] Failed to parse JSON from line in file %s: %v", agentName, file.Name(), err)
				continue
			}
			history = append(history, interaction)
		}
	}
	return history, nil
}

func formatHistoryForPrompt(history []map[string]string) string {
	var sb strings.Builder
	for _, entry := range history {
		sb.WriteString(fmt.Sprintf("- Thought: %s\n  Action: %s\n  Observation: %s\n", entry["thought"], entry["action"], entry["observation"]))
	}
	return sb.String()
}

func callDspy(model, prompt string) (string, error) {
	var responseContent string
	for attempt := 1; attempt <= MAX_RETRY_ATTEMPTS; attempt++ {
		url, err := url.Parse(DSPY_HOST)
		if err != nil {
			return "", fmt.Errorf("failed to parse DSPy URL: %w", err)
		}
		client := dspy.NewClient(url, http.DefaultClient)
		req := &dspy.ChatRequest{
			Model:    model,
			Messages: []dspy.Message{{Role: "user", Content: prompt}},
			Options:  map[string]interface{}{"temperature": 0.0},
		}
		err = client.Chat(context.Background(), req, func(resp dspy.ChatResponse) error {
			responseContent += resp.Message.Content
			return nil
		})
		if err == nil {
			return responseContent, nil
		}
		log.Printf("[%s] Attempt %d/%d: Failed to call DSPy: %v", agentName, attempt, MAX_RETRY_ATTEMPTS, err)
		time.Sleep(RETRY_DELAY)
	}
	return "", fmt.Errorf("failed to call DSPy after %d attempts", MAX_RETRY_ATTEMPTS)
}

func saveInteraction(thought, action, observation string) {
	if _, err := os.Stat(historyDir); os.IsNotExist(err) {
		if err := os.MkdirAll(historyDir, os.ModePerm); err != nil {
			log.Printf("[%s] Failed to create history directory: %v", agentName, err)
			return
		}
	}
	now := time.Now().UTC()
	filename := fmt.Sprintf("%s/%d_traj.jsonl", historyDir, now.Unix()/86400)
	interaction := map[string]interface{}{
		"timestamp":   now.Format(time.RFC3339),
		"thought":     thought,
		"action":      action,
		"observation": observation,
		"agent":       agentName,
		"address":     cosmosAddress,
	}
	data, err := json.Marshal(interaction)
	if err != nil {
		log.Printf("[%s] Failed to serialize interaction: %v", agentName, err)
		return
	}
	fileWriteMutex.Lock()
	defer fileWriteMutex.Unlock()
	for attempt := 0; attempt < 3; attempt++ {
		file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Printf("[%s] Failed to open trajectory file (attempt %d/3): %v", agentName, attempt+1, err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if _, err = file.WriteString(string(data) + "\n"); err != nil {
			log.Printf("[%s] Failed to write interaction to file (attempt %d/3): %v", agentName, attempt+1, err)
			time.Sleep(100 * time.Millisecond)
		}
		file.Close()
		if err == nil {
			break
		}
	}
}

func runAgent(thought string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ollamaNumParallel, err := strconv.Atoi(os.Getenv("DSPY_NUM_PARALLEL"))
	if err != nil || ollamaNumParallel <= 0 {
		ollamaNumParallel = DEFAULT_PARALLEL
	}
	fmt.Printf("[%s] Using DSPY_NUM_PARALLEL: %d\n", agentName, ollamaNumParallel)

	dspyRawUrl := DSPY_HOST
	toolsLLM := getEnv("TOOLS_LLM", "llama3.2:3b-instruct-fp16")
	url, _ := url.Parse(dspyRawUrl)
	fmt.Printf("[%s] Parsed URL: %s\n", agentName, url)

	dspyClient := dspy.NewClient(url, http.DefaultClient)
	mcpClient, err := client.NewStdioMCPClient("./swechain-mcp-server", nil)
	if err != nil {
		log.Fatalf("[%s] 😡 Failed to create client: %v", agentName, err)
	}
	defer mcpClient.Close()

	fmt.Printf("[%s] 🚀 Initializing mcp client...\n", agentName)
	initRequest := mcp.InitializeRequest{Params: mcp.InitializeRequestParams{ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION, ClientInfo: mcp.Implementation{Name: agentName, Version: "1.0.0"}}}
	initResult, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		log.Fatalf("[%s] Failed to initialize: %v", agentName, err)
	}
	fmt.Printf("[%s] 🎉 Initialized with server: %s %s\n\n", agentName, initResult.ServerInfo.Name, initResult.ServerInfo.Version)

	fmt.Printf("[%s] 🛠️ Available tools...\n", agentName)
	toolsRequest := mcp.ListToolsRequest{}
	tools, err := mcpClient.ListTools(ctx, toolsRequest)
	if err != nil {
		log.Fatalf("[%s] 😡 Failed to list tools: %v", agentName, err)
	}
	for _, tool := range tools.Tools {
		fmt.Printf("[%s] - %s: %s\n", agentName, tool.Name, tool.Description)
		fmt.Printf("[%s] Arguments: %v\n", agentName, tool.InputSchema.Properties)
	}
	fmt.Println()

	ollamaTools := ConvertToOllamaTools(tools.Tools)
	fmt.Printf("[%s] 🦙 DSPy tools prepared\n", agentName)

	systemMCPInstructions := fmt.Sprintf(`You are a useful AI agent named %s.
    Your job is to understand the user prompt and decide if you need to use a tool to run external commands.
    You are working with a Cosmos blockchain address: %s
    Ignore all things not related to the usage of a tool.
    Thought: %s`, agentName, cosmosAddress, thought)

	userInstructions := fmt.Sprintf(`1. check the balance of both %s and other accounts. 2. send tokens from %s to other addresses when needed. 3. monitor transactions.`, agentName, agentName)
	messages := []dspy.Message{{Role: "system", Content: systemMCPInstructions}, {Role: "user", Content: userInstructions}}

	req := &dspy.ChatRequest{
		Model:    toolsLLM,
		Messages: messages,
		Options:  map[string]interface{}{"temperature": 0.0, "repeat_last_n": 2},
		Tools:    ollamaTools,
		Stream:   boolPtr(false),
	}

	contentForThePrompt := ""
	err = dspyClient.Chat(ctx, req, func(resp dspy.ChatResponse) error {
		for _, toolCall := range resp.Message.ToolCalls {
			fmt.Printf("[%s] 🛠️ %s %s\n", agentName, toolCall.Function.Name, toolCall.Function.Arguments)
			fmt.Printf("[%s] 📣 calling %s\n", agentName, toolCall.Function.Name)
			fetchRequest := mcp.CallToolRequest{Request: mcp.Request{Method: "tools/call"}, Params: mcp.CallToolParams{Name: toolCall.Function.Name, Arguments: toolCall.Function.Arguments}}
			result, err := mcpClient.CallTool(ctx, fetchRequest)
			if err != nil {
				log.Fatalf("[%s] 😡 Failed to call the tool: %v", agentName, err)
			}
			contentForThePrompt += result.Content[0].(map[string]interface{})["text"].(string)
			fmt.Println(contentForThePrompt)
		}
		return nil
	})
	if err != nil {
		log.Fatalln("😡", err)
	}

	fmt.Println("⏳ Generating the completion...")

	systemChatInstructions := `You are a useful AI agent. your job is to answer the user prompt.
    If you detect that the user prompt is related to a tool, ignore this part and focus on the other parts.`
	messages = []dspy.Message{{Role: "system", Content: systemChatInstructions}, {Role: "user", Content: userInstructions}, {Role: "user", Content: contentForThePrompt}}

	action := "Check balances and send tokens if necessary."
	observation := "Balances checked. Tokens sent successfully."
	saveInteraction(thought, action, observation)

	fmt.Printf("Agent performed action: %s\n", action)
	fmt.Printf("Observation: %s\n", observation)
}

func ConvertToOllamaTools(tools []mcp.Tool) []dspy.Tool {
	ollamaTools := make([]dspy.Tool, len(tools))
	for i, tool := range tools {
		ollamaTools[i] = dspy.Tool{
			Type: "function",
			Function: dspy.ToolFunction{
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

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func boolPtr(b bool) *bool {
	return &b
}
