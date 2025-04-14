package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/documentloaders"
	"github.com/tmc/langchaingo/llms/openai"
	"github.com/tmc/langchaingo/textsplitter"
)

func main() {
	ctx := context.Background()
	llm, err := openai.New()
	if err != nil {
		log.Fatal(err)
	}

	feedback_output, err := exec.Command("./bin/feedback").Output()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
	fmt.Printf("LIVE: %s", feedback_output)

	llmSummarizationChain := chains.LoadRefineSummarization(llm)
	doc := "blahblah " + string(feedback_output)

	docs, err := documentloaders.NewText(strings.NewReader(doc)).LoadAndSplit(ctx,
		textsplitter.NewRecursiveCharacter(),
	)
	outputValues, err := chains.Call(ctx, llmSummarizationChain, map[string]any{"input_documents": docs})
	if err != nil {
		log.Fatal(err)
	}
	out := outputValues["text"].(string)
	fmt.Println(out)

	// Output:
	// Large language models are a type of deep learning model that can understand, learn,
	// summarize, translate, predict, and generate text and other content based on knowledge
	// gained from massive datasets. They are used in a variety of applications, including
	// natural language processing, healthcare, and software development.
}
