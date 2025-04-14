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

type EnvironmentFeedback struct {
	Keys        []map[string]interface{} `json:"keys"`
	Auctions    []map[string]interface{} `json:"auctions"`
	Bids        []map[string]interface{} `json:"bids"`
	DenomOwners []map[string]interface{} `json:"denom_owners"`
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

func RunEnvironmentFeedbackExample(originalQuestion string) {
	output := logging.NewConsoleOutput(true, logging.WithColor(true))

	logger := logging.NewLogger(logging.Config{
		Severity: logging.INFO,
		Outputs:  []logging.Output{output},
	})
	logging.SetLogger(logger)

	ctx := core.WithExecutionState(context.Background())

	llms.EnsureFactory()
	//err := core.ConfigureDefaultLLM(apiKey, core.ModelAnthropicSonnet)
	// Works
	//err := core.ConfigureDefaultLLM("your key here", core.ModelAnthropicSonnet)
	//err := core.ConfigureDefaultLLM("ollama", core.ModelID("ollama:granite3.2:8b-instruct-q8_0")) // 8m
	//err := core.ConfigureDefaultLLM("ollama", core.ModelID("ollama:deepseek-r1:14b")) //8m
	//err := core.ConfigureDefaultLLM("ollama", core.ModelID("ollama:command-r7b:7b")) //8m
	//err := core.ConfigureDefaultLLM("ollama", core.ModelID("ollama:mistral:7b")) // 8m
	//err := core.ConfigureDefaultLLM("ollama", core.ModelID("ollama:phi4-mini:3.8b")) // 8m
	//err := core.ConfigureDefaultLLM("ollama", core.ModelID("ollama:gemma3:12b")) // 7m
	err := core.ConfigureDefaultLLM("ollama", core.ModelID("ollama:cogito:3b")) // 7m
	if err != nil {
		logger.Fatalf(ctx, "Failed to setup llm")
	}

	// Load dataset from file
	dataset, err := loadDataset("./datasets/feedback_dataset.json")
	if err != nil {
		log.Fatalf("Failed to load dataset: %v", err)
	}

	// Signature
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "environment_feedback"}},
			{Field: core.Field{Name: "question"}},
		},
		[]core.OutputField{{Field: core.NewField("answer")}},
	)

	// Module
	feedbackModule := modules.NewChainOfThought(signature) // or a custom module

	// Program
	program := core.NewProgram(map[string]core.Module{"feedback": feedbackModule}, func(ctx context.Context, inputs map[string]interface{}) (map[string]interface{}, error) {
		return feedbackModule.Process(ctx, inputs, core.WithGenerateOptions(
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
			"environment_feedback": ex.EnvironmentFeedback,
			"question":             ex.Question,
			"answer":               ex.Answer,
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
	//fmt.Printf("LIVE: %s", feedback_output)
	//fmt.Printf("this question %s", originalQuestion)

	// Execute the program with a question
	//live_result, err := compiledProgram.Execute(context.Background(), map[string]interface{}{
	live_result, err := compiledProgram.Execute(ctx, map[string]interface{}{
		// "question": string(feedback_output) + "whats bob address?",
		//"question":             string(feedback_output) + "who made the bids on auction 1?",
		"environment_feedback": string(feedback_output),
		//"question":             "who made the bids on auction 1?",
		//"question": "give me a summary of all the bids on auction 1?",
		"question": originalQuestion,
	})
	if err != nil {
		log.Fatalf("Error executing program: %v", err)
	}

	fmt.Printf("total result: %s\n", live_result)
	//fmt.Printf("total answer: %s\n", live_result["answer"])
	//fmt.Printf("Answer: %s\n", live_result["answer"])

}

func main() {
	//if len(os.Args) < 1 {
	//	return fmt.Errorf("usage: %s \"question?\"", os.Args[0])
	//}
	originalQuestion := os.Args[1]
	log.Println("This is the original originalQuestion: ", originalQuestion)

	RunEnvironmentFeedbackExample(originalQuestion)
}
