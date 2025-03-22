package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ollama/ollama/api"
	"github.com/redis/go-redis/v9"
)

// Constants
const (
	CurrentUser = "moresearch"
	Timestamp   = "2025-03-20 22:51:01"
)

// Configuration
type Config struct {
	redisAddr     string
	ollamaAddr    string
	embedModel    string
	responseModel string
	mode          string
	agentName     string
	agentAddr     string
}

// Trajectory represents an agent's trajectory event
type Trajectory struct {
	AgentName string    `json:"agent_name"`
	AgentAddr string    `json:"agent_addr"`
	Query     string    `json:"query"`
	Timestamp time.Time `json:"timestamp"`
	UserID    string    `json:"user_id"`
}

// Response represents a response to a trajectory
type Response struct {
	AgentName string    `json:"agent_name"`
	AgentAddr string    `json:"agent_addr"`
	Response  string    `json:"response"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
}

// getStreamNames returns input and output stream names for an agent
func getStreamNames(agentName, agentAddr string) (string, string) {
	baseStream := fmt.Sprintf("trajectory:%s:%s", agentName, agentAddr)
	return baseStream + ":input", baseStream + ":output"
}

func main() {
	// Parse command line flags
	cfg := parseFlags()

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Get stream names based on agent
	inputStream, outputStream := getStreamNames(cfg.agentName, cfg.agentAddr)

	log.Printf("Using streams - Input: %s, Output: %s", inputStream, outputStream)
	log.Printf("Current user: %s, Timestamp: %s", CurrentUser, Timestamp)

	// Connect to Redis
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.redisAddr,
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	// Create a channel for graceful shutdown
	done := make(chan bool)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	switch cfg.mode {
	case "publish":
		if err := publishTrajectory(ctx, rdb, inputStream, cfg.agentName, cfg.agentAddr); err != nil {
			log.Fatalf("Failed to publish trajectory: %v", err)
		}
	case "consume":
		go func() {
			if err := startConsumer(ctx, rdb, inputStream, outputStream, cfg); err != nil {
				log.Printf("Consumer error: %v", err)
				done <- true
			}
		}()

		select {
		case <-sigChan:
			log.Println("Received termination signal")
		case <-done:
			log.Println("Consumer completed")
		}
	}
}

func parseFlags() *Config {
	cfg := &Config{}

	flag.StringVar(&cfg.redisAddr, "redis", "localhost:6379", "Redis address")
	flag.StringVar(&cfg.ollamaAddr, "ollama", "http://localhost:11434", "Ollama API address")
	flag.StringVar(&cfg.embedModel, "embed-model", "snowflake-arctic-embed:33m", "Embedding model name")
	flag.StringVar(&cfg.responseModel, "response-model", "llama3.2:3b-instruct-q8_0", "Response generation model name")
	flag.StringVar(&cfg.mode, "mode", "", "Operation mode: 'publish' or 'consume'")
	flag.StringVar(&cfg.agentName, "agent", "", "Agent name")
	flag.StringVar(&cfg.agentAddr, "addr", "", "Agent address")

	flag.Parse()

	if cfg.mode != "publish" && cfg.mode != "consume" {
		fmt.Println("AI Agent Trajectory RAG Pipeline")
		fmt.Println("\nUsage Examples:")
		fmt.Println("  Publish a trajectory:")
		fmt.Println("    go run rag.go -mode publish -agent alice -addr cosmos1xyz...")
		fmt.Println("\n  Start the consumer:")
		fmt.Println("    go run rag.go -mode consume -agent alice -addr cosmos1xyz...")
		fmt.Println("    go run rag.go -mode consume -redis localhost:6380 -agent alice -addr cosmos1xyz...")
		fmt.Println("    go run rag.go -mode consume -embed-model all-minilm:33m -agent bob -addr cosmos123...")
		os.Exit(1)
	}

	if cfg.agentName == "" || cfg.agentAddr == "" {
		fmt.Println("Error: Agent name and address are required")
		fmt.Println("Example: go run rag.go -mode publish -agent bob -addr cosmos1fgs3u5hvkrh50y7nphrqyjur27jaahh4h3c86w")
		os.Exit(1)
	}

	return cfg
}

func publishTrajectory(ctx context.Context, rdb *redis.Client, inputStream, agentName, agentAddr string) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Enter trajectory query: ")
	query, _ := reader.ReadString('\n')
	query = strings.TrimSpace(query)

	trajectory := Trajectory{
		AgentName: agentName,
		AgentAddr: agentAddr,
		Query:     query,
		Timestamp: time.Now().UTC(),
		UserID:    CurrentUser,
	}

	trajectoryJSON, err := json.Marshal(trajectory)
	if err != nil {
		return fmt.Errorf("failed to marshal trajectory: %v", err)
	}

	err = rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: inputStream,
		Values: map[string]interface{}{"trajectory": string(trajectoryJSON)},
	}).Err()
	if err != nil {
		return fmt.Errorf("failed to publish trajectory: %v", err)
	}

	log.Printf("Published trajectory for agent %s (%s): %s", agentName, agentAddr, query)
	return nil
}

func startConsumer(ctx context.Context, rdb *redis.Client, inputStream, outputStream string, cfg *Config) error {
	log.Printf("Starting consumer for agent %s (%s)", cfg.agentName, cfg.agentAddr)

	groupName := fmt.Sprintf("rag-group:%s:%s", cfg.agentName, cfg.agentAddr)

	// Create a new consumer group if it doesn't exist
	err := rdb.XGroupCreate(ctx, inputStream, groupName, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return fmt.Errorf("failed to create consumer group: %v", err)
	}

	log.Printf("Consuming trajectories from stream: %s", inputStream)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			streams, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
				Group:    groupName,
				Consumer: "consumer-1",
				Streams:  []string{inputStream, ">"},
				Count:    1,
				Block:    0,
			}).Result()

			if err != nil {
				return fmt.Errorf("failed to read from stream: %v", err)
			}

			for _, stream := range streams {
				for _, message := range stream.Messages {
					if err := processTrajectory(ctx, rdb, message, outputStream, cfg); err != nil {
						log.Printf("Error processing trajectory: %v", err)
						continue
					}

					err = rdb.XAck(ctx, inputStream, groupName, message.ID).Err()
					if err != nil {
						log.Printf("Failed to acknowledge message: %v", err)
					}
				}
			}
		}
	}
}

func processTrajectory(ctx context.Context, rdb *redis.Client, message redis.XMessage, outputStream string, cfg *Config) error {
	trajectoryJSON := message.Values["trajectory"].(string)
	var trajectory Trajectory
	if err := json.Unmarshal([]byte(trajectoryJSON), &trajectory); err != nil {
		return fmt.Errorf("failed to unmarshal trajectory: %v", err)
	}

	// Only process trajectories for the specified agent
	if trajectory.AgentName != cfg.agentName || trajectory.AgentAddr != cfg.agentAddr {
		return nil
	}

	// Generate embeddings
	embedding, err := generateOllamaEmbedding(ctx, trajectory.Query, cfg.embedModel, cfg.ollamaAddr)
	if err != nil {
		return fmt.Errorf("failed to generate embedding: %v", err)
	}

	// Retrieve relevant documents
	documents := retrieveDocuments(embedding)

	// Generate response
	response, err := generateOllamaResponse(ctx, trajectory.Query, documents, cfg.responseModel, cfg.ollamaAddr)
	if err != nil {
		return fmt.Errorf("failed to generate response: %v", err)
	}

	// Create response event
	responseEvent := Response{
		AgentName: trajectory.AgentName,
		AgentAddr: trajectory.AgentAddr,
		Response:  response,
		CreatedAt: time.Now().UTC(),
		CreatedBy: CurrentUser,
	}

	responseJSON, err := json.Marshal(responseEvent)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %v", err)
	}

	err = rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: outputStream,
		Values: map[string]interface{}{"response": string(responseJSON)},
	}).Err()
	if err != nil {
		return fmt.Errorf("failed to publish response: %v", err)
	}

	log.Printf("Processed trajectory for agent %s: %s", trajectory.AgentName, trajectory.Query)
	return nil
}

func generateOllamaEmbedding(ctx context.Context, query string, model, ollamaAddr string) ([]float64, error) {
	// Parse the Ollama API URL
	apiURL, err := url.Parse(ollamaAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid Ollama API URL: %v", err)
	}

	// Initialize the Ollama API client
	client := api.NewClient(apiURL, http.DefaultClient)

	// Create embedding request
	embedRequest := &api.EmbeddingRequest{
		Model:  model,
		Prompt: query,
	}

	// Call the API
	embedResponse, err := client.CreateEmbedding(ctx, embedRequest)
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %v", err)
	}

	return embedResponse.Embedding, nil
}

func generateOllamaResponse(ctx context.Context, query string, documents []string, model, ollamaAddr string) (string, error) {
	// Parse the Ollama API URL
	apiURL, err := url.Parse(ollamaAddr)
	if err != nil {
		return "", fmt.Errorf("invalid Ollama API URL: %v", err)
	}

	// Initialize the Ollama API client
	client := api.NewClient(apiURL, http.DefaultClient)

	prompt := fmt.Sprintf("Query: %s\nDocuments: %s", query, strings.Join(documents, "\n"))

	// Create generation request
	generateRequest := &api.GenerateRequest{
		Model:  model,
		Prompt: prompt,
		System: "You are a helpful assistant analyzing AI agent trajectories.",
		Options: map[string]interface{}{
			"temperature":       0.7,
			"top_p":             0.9,
			"frequency_penalty": 0,
			"presence_penalty":  0,
		},
	}

	// Call the API
	var response string
	var onResponse api.GenerateResponseFunc = func(resp *api.GenerateResponse) error {
		response += resp.Response
		return nil
	}

	err = client.Generate(ctx, generateRequest, onResponse)
	if err != nil {
		return "", fmt.Errorf("generate request failed: %v", err)
	}

	return response, nil
}

func retrieveDocuments(embedding []float64) []string {
	// TODO: Implement actual vector similarity search using Redis
	return []string{"Previous trajectory data for the agent..."}
}
