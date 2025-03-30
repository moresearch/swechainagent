package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms/openai"
	"github.com/tmc/langchaingo/schema"
)

// Response represents the JSON structure for the output
type Response struct {
	Question string      `json:"question"`
	Answer   interface{} `json:"answer"` // Will hold structured data with summary and details
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	// Check if a question was provided as a command-line argument
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: %s \"question?\"", os.Args[0])
	}

	originalQuestion := os.Args[1]

	// Enhance the question with instructions for structured JSON answers with summary
	enhancedQuestion := enhanceQuestionWithInstructions(originalQuestion)

	// Initialize the OpenAI LLM
	llm, err := openai.New()
	if err != nil {
		return err
	}

	// Get documents from ./bin/feedback
	docs, err := getDocumentsFromFeedback()
	if err != nil {
		return fmt.Errorf("failed to get documents: %w", err)
	}

	// Create and call the StuffQA chain
	stuffQAChain := chains.LoadStuffQA(llm)
	result, err := chains.Call(context.Background(), stuffQAChain, map[string]any{
		"input_documents": docs,
		"question":        enhancedQuestion,
	})
	if err != nil {
		return err
	}

	// Get the answer from the result
	textAnswer, ok := result["text"].(string)
	if !ok {
		return fmt.Errorf("unexpected answer format")
	}

	// Parse the JSON string from the LLM response
	var structuredAnswer interface{}
	jsonString := extractJSONFromText(textAnswer)
	if err := json.Unmarshal([]byte(jsonString), &structuredAnswer); err != nil {
		// If parsing fails, create a fallback structured answer
		structuredAnswer = map[string]interface{}{
			"summary": "Failed to parse structured data",
			"details": textAnswer,
		}
	}

	// Format the response as JSON
	response := Response{
		Question: originalQuestion,
		Answer:   structuredAnswer,
	}

	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	// Print the JSON response
	fmt.Println(string(jsonData))
	return nil
}

// enhanceQuestionWithInstructions adds instructions for JSON-structured answers with summary
func enhanceQuestionWithInstructions(question string) string {
	lowerQuestion := strings.ToLower(question)

	instructionPrefix := "Please respond with a valid JSON object containing two fields: " +
		"1) 'summary': a natural language description of the answer, and " +
		"2) 'details': structured data relevant to the answer. " +
		"The JSON should be properly formatted without any text before or after it. "

	// Special instructions for auction-related questions
	if strings.Contains(lowerQuestion, "auction") || strings.Contains(lowerQuestion, "bid") {
		instructionPrefix = "Respond ONLY with a valid JSON object (no additional text) that contains: " +
			"1) 'summary': a concise natural language description of the auctions and their status, and " +
			"2) 'details': structured data with arrays of auction objects with these properties: " +
			"auctionId, creator, description, status, winner, currentBidAmount, bids (an array with bidder, amount, description). " +
			"Also include participantDetails with name, address, and balance. " +
			"Example format: {\"summary\":\"There are 2 open auctions, one with no bids and one with a 5000 token bid from Bob.\", " +
			"\"details\":{\"auctions\":[{\"auctionId\":0,\"creator\":\"bob\",...}], " +
			"\"participants\":[{\"name\":\"Alice\",\"address\":\"cosmos1...\",\"balance\":\"20000 token\"}]}} " +
			"For the following question: "
	}

	return instructionPrefix + question
}

// extractJSONFromText attempts to extract a JSON object from text
func extractJSONFromText(text string) string {
	// Find first { and last }
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")

	if start >= 0 && end > start {
		return text[start : end+1]
	}

	// Return an empty JSON object if no JSON is found
	return "{\"summary\":\"No structured data available\",\"details\":{}}"
}

func getDocumentsFromFeedback() ([]schema.Document, error) {
	// Execute the ./bin/feedback command
	cmd := exec.Command("./bin/feedback")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to execute ./bin/feedback: %w", err)
	}

	// Parse the output into documents
	// Assuming each line is a separate document
	lines := strings.Split(string(output), "\n")
	var docs []schema.Document

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			docs = append(docs, schema.Document{
				PageContent: line,
			})
		}
	}

	return docs, nil
}
