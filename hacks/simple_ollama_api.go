package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"

	"github.com/ollama/ollama/api"
)

func main() {
	// Step 1: Create a new client for the Ollama API
	serverURL, err := url.Parse("http://localhost:11434") // Replace with your Ollama server URL
	if err != nil {
		log.Fatalf("Failed to parse URL: %v", err)
	}

	client := api.NewClient(serverURL, http.DefaultClient)

	// Step 2: Define the model and input prompt
	modelName := "llama3.2:3b-instruct-fp16" // Replace with the desired model name
	prompt := "Explain the concept of gravity in simple terms."

	// Step 3: Create a context for the request
	ctx := context.Background()

	// Step 4: Generate a response from the model
	var fullResponse string
	err = client.Generate(ctx, &api.GenerateRequest{
		Model:  modelName,
		Prompt: prompt,
	}, func(response api.GenerateResponse) error {
		// This callback gets called for each chunk of the response
		fullResponse += response.Response
		return nil
	})

	if err != nil {
		log.Fatalf("Failed to generate response: %v", err)
	}

	// Step 5: Print the generated response
	fmt.Println("Model Response:")
	fmt.Println(fullResponse)
}
