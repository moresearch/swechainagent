package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/logging"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
	"github.com/XiaoConstantine/dspy-go/pkg/optimizers"
)

type Issue struct {
	Title       []map[string]interface{} `json:"title"`
	Description []map[string]interface{} `json:"description"`
}

type DatasetItem struct {
	EnvironmentFeedback EnvironmentFeedback `json:"environment_feedback"`
	Question            string              `json:"question"`
	Answer              string              `json:"answer"`
}

func loadDataset(filepath string) ([]DatasetItem, error) {
	file, err := os.ReadFile(filepath)
	if err != nil {
		return nil, err
	}

	var dataset []DatasetItem
	err = json.Unmarshal(file, &dataset)
	if err != nil {
		return nil, err
	}

	return dataset, nil
}

func RunEnvironmentFeedbackExample() {
	output := logging.NewConsoleOutput(true, logging.WithColor(true))

	logger := logging.NewLogger(logging.Config{
		Severity: logging.INFO,
		Outputs:  []logging.Output{output},
	})
	logging.SetLogger(logger)

	ctx := core.WithExecutionState(context.Background())

	llms.EnsureFactory()
	err := core.ConfigureDefaultLLM("ollama", core.ModelID("ollama:granite3.2-vision:2b"))
	if err != nil {
		logger.Fatalf(ctx, "Failed to setup llm")
	}

	// Load dataset from file
	dataset, err := loadDataset("./datasets/issue_dataset.json")
	if err != nil {
		log.Fatalf("Failed to load dataset: %v", err)
	}

	// Signature
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "issue_list"}},
			{Field: core.Field{Name: "question"}},
		},
		[]core.OutputField{{Field: core.NewField("answer")}},
	)

	// Module
	issueModule := modules.NewChainOfThought(signature) // or a custom module

	// Program
	program := core.NewProgram(map[string]core.Module{"issue_list": issueModule}, func(ctx context.Context, inputs map[string]interface{}) (map[string]interface{}, error) {
		return issueModule.Process(ctx, inputs, core.WithGenerateOptions(
			core.WithTemperature(0.0),
			core.WithMaxTokens(8192),
		))
	})

	// Optimizer
	optimizer := optimizers.NewBootstrapFewShot(func(example, prediction map[string]interface{}, ctx context.Context) bool {
		return example["answer"] == prediction["answer"]
	}, 5)

	// Prepare training set
	trainset := make([]map[string]interface{}, len(dataset[:2])) // Use first 2 for training
	for i, ex := range dataset[:2] {
		trainset[i] = map[string]interface{}{
			"issue_list": ex.IssueList,
			"question":   ex.Question,
			"answer":     ex.Answer,
		}
	}

	// Compile the program
	compiledProgram, err := optimizer.Compile(ctx, program, program, trainset)
	if err != nil {
		logger.Fatalf(ctx, "Failed to compile program: %v", err)
	}

	// Test the compiled program
	for _, ex := range dataset[2:] { // test with remaining
		result, err := compiledProgram.Execute(ctx, map[string]interface{}{
			"environment_feedback": ex.EnvironmentFeedback,
			"question":             ex.Question,
		})
		if err != nil {
			log.Printf("Error executing program: %v", err)
			continue
		}

		logger.Info(ctx, "Question: %s\n", ex.Question)
		logger.Info(ctx, "Predicted Answer: %s\n", result["answer"])
		logger.Info(ctx, "Actual Answer: %s\n\n", ex.Answer)
	}

	feedback_output, err := exec.Command("./bin/feedback").Output()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
	fmt.Printf("LIVE: %s", feedback_output)

	// Execute the program with a question
	live_result, err := compiledProgram.Execute(ctx, map[string]interface{}{
		"environment_feedback": string(feedback_output),
		"question":             "give me a summary of all the bids on auction 1?",
	})
	if err != nil {
		log.Fatalf("Error executing program: %v", err)
	}

	fmt.Printf("Answer: %s\n", live_result["answer"])

}

func main() {
	RunEnvironmentFeedbackExample()
}
