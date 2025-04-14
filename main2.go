package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"
	"github.com/XiaoConstantine/dspy-go/pkg/optimizers"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

/*
type DatasetItem struct {
	Toolbox   string `json:"toolbox"`
	Objective string `json:"objective"`
	Tool      string `json:"chosen_tool"`
}
*/

type Tool struct {
	Description string `json:"description"`
	InputSchema struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Description string `json:"description"`
			Type        string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	} `json:"inputSchema"`
	Name string `json:"name"`
}

type ChosenTool struct {
	ToolName string                 `json:"tool_name"`
	Input    map[string]interface{} `json:"input"`
}

type Item struct {
	Toolbox    []Tool     `json:"toolbox"`
	Objective  string     `json:"objective"`
	ChosenTool ChosenTool `json:"chosen_tool"`
}

func loadDataset(filepath string) ([]Item, error) {
	file, err := os.ReadFile(filepath)
	if err != nil {
		return nil, err
	}

	//var dataset []DatasetItem
	//err = json.Unmarshal(file, &dataset)

	var items []Item
	merr := json.Unmarshal([]byte(file), &items)
	if err != nil {
		return nil, merr
	}

	return items, nil
}

func main() {

	// Create a new MCP client with the server data connection
	mcpClient, err := client.NewStdioMCPClient(
		"./bin/swechain-mcp-server",
		[]string{}, // environment variables
	)
	if err != nil {
		log.Fatalf("😡 Failed to create client: %v", err)
	}
	defer mcpClient.Close()

	//ctx, cancel := context.WithTimeout(context.Background(), 6000000*time.Second)
	//defer cancel()
	ctx := context.Background()

	// Define and Initialize the MCP request
	fmt.Println("🚀 Initializing mcp client...")
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "mcp-curl client 🌍",
		Version: "1.0.0",
	}

	// Initialize the MCP client and connect to the server
	initResult, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		log.Fatalf("Failed to initialize: %v", err)
	}
	fmt.Printf(
		"🎉 Initialized with server: %s %s\n\n",
		initResult.ServerInfo.Name,
		initResult.ServerInfo.Version,
	)

	// Get the list of tools from the server
	fmt.Println("🛠️ Available tools...")
	toolsRequest := mcp.ListToolsRequest{}
	tools, err := mcpClient.ListTools(ctx, toolsRequest)
	if err != nil {
		log.Fatalf("😡 Failed to list tools: %v", err)
	}
	// Display the list of tools
	//for _, tool := range tools.Tools {
	//	fmt.Printf("- %s: %s\n", tool.Name, tool.Description)
	//	fmt.Println("Arguments:", tool.InputSchema.Properties)
	//}

	// Serialize to JSON
	jsonToolData, err := json.MarshalIndent(tools.Tools, "", "  ")
	if err != nil {
		fmt.Println("Error serializing to JSON:", err)
		return
	}

	toolbox := string(jsonToolData)
	// Print the JSON
	fmt.Println(string("god damn tools in json"))
	fmt.Println(string(toolbox))

	//fmt.Println(tools.Tools)
	/*

		// Prepare the call of the tool
		fmt.Println("📣 calling the tool!")
		fetchRequest := mcp.CallToolRequest{
			Request: mcp.Request{
				Method: "tools/call",
			},
		}
		fetchRequest.Params.Name = "feedbackQA"
		fetchRequest.Params.Arguments = map[string]interface{}{
			"question": "whats bob blockchain address? and whats his budget?",
		}

		// Call the tool
		result, err := mcpClient.CallTool(ctx, fetchRequest)
		if err != nil {
			log.Fatalf("MCP client Failed to call the tool: %v", err)
		}
		// display the text content of result
		fmt.Println("Response From MCP tool:")
		//fmt.Println(result.Content[0].(map[string]interface{})["text"])
		//fmt.Println(result)
		fmt.Println("MCP Tool Error?, %+v", result.IsError)

		//jsonBytes, err := json.Marshal(result.Content[0])
		jsonBytes, err := json.Marshal(result.Content[0])
		if err != nil {
			// handle error
		}
		tool_response := string(jsonBytes)

		fmt.Println(tool_response)
		//fmt.Println("ok, %+v", result.Content[0])
	*/

	// Start of dspy
	// Configure the default LLM
	llms.EnsureFactory()
	//err := config.ConfigureDefaultLLM("your-api-key", core.ModelAnthropicSonnet)
	//llm_err := core.ConfigureDefaultLLM("ollama", core.ModelID("ollama:gemma3:12b")) // 7m
	//llm_err := core.ConfigureDefaultLLM("ollama", core.ModelID("ollama:cogito:3b")) // 7m
	llm_err := core.ConfigureDefaultLLM("ollama", core.ModelID("ollama:llama3.1:8b-instruct-q8_0")) // 7m
	if llm_err != nil {
		log.Fatalf("Failed to configure LLM: %v", llm_err)
	}

	dataset, err := loadDataset("./datasets/cot_dataset.json")
	if err != nil {
		log.Fatalf("Failed to load dataset: %v", err)
	}

	// Create a signature for question answering
	signature := core.NewSignature(
		[]core.InputField{
			{Field: core.Field{Name: "toolbox"}},
			{Field: core.Field{Name: "objective"}},
		},
		[]core.OutputField{{Field: core.Field{Name: "tool"}}}, //answer
	)

	// Create a ChainOfThought module that implements step-by-step reasoning
	cot := modules.NewChainOfThought(signature)

	// Create a program that executes the module
	program := core.NewProgram(
		map[string]core.Module{"cot": cot},
		func(ctx context.Context, inputs map[string]interface{}) (map[string]interface{}, error) {
			return cot.Process(ctx, inputs)
		},
	)

	// Optimizer
	optimizer := optimizers.NewBootstrapFewShot(func(example, prediction map[string]interface{}, ctx context.Context) bool {
		return example["tool"] == prediction["tool"]
	}, 5)

	// Prepare training set
	trainset := make([]map[string]interface{}, len(dataset[:2])) // Use first 2 for training
	for i, ex := range dataset[:2] {
		trainset[i] = map[string]interface{}{
			"toolbox":   ex.Toolbox,
			"objective": ex.Objective,
			"tool":      ex.ChosenTool,
		}
	}

	// Compile the program
	compiledProgram, err := optimizer.Compile(ctx, program, program, trainset)
	if err != nil {
		log.Println(ctx, "Failed to compile program: %v", err)
	}

	// Test the compiled program
	for _, ex := range dataset[2:] { // test with remaining
		result, err := compiledProgram.Execute(ctx, map[string]interface{}{
			"toolbox":   ex.Toolbox,
			"objective": ex.Objective,
		})
		if err != nil {
			log.Printf("Error executing program: %v", err)
			continue
		}

		log.Println(ctx, "Objective: %s\n", ex.Objective)
		//log.Println(ctx, "Predicted tool: %s\n", result["chosen_tool"])
		log.Println(ctx, "Predicted tool: %s\n", result)
		log.Println(ctx, "Actual tool: %s\n\n", ex.ChosenTool)
	}

	//prompt := tool_response + "You are a software agent bob, do auctions and bids on issues, using the following tools"
	//TODO prompt := tools.Tools + "You are a software agent bob, do auctions and bids on issues, using the following tools"
	//prompt := string(jsonToolData) + "You are a software agent bob, do auctions and bids on issues in SWEChain. SWEChain is a blockchain enviroment for SWE-Agents that provide agents like yourself with tools to create git issue auctions and bids. Using these tools, how would you interact with your swechain blockchain enviroment?"
	prompt := "You are a software agent bob, do auctions and bids on issues in SWEChain. SWEChain is a blockchain enviroment for SWE-Agents that provide agents like yourself with tools to create git issue auctions and bids. Using these tools, how would you interact with your swechain blockchain enviroment? answer only with chosen_tool in text format without ```json"
	for {
		// Execute the program with a question
		//cot_result, cot_err := program.Execute(context.Background(), map[string]interface{}{
		cot_result, cot_err := compiledProgram.Execute(context.Background(), map[string]interface{}{
			//cot_result, cot_err := compiledProgram.Execute(ctx context.Context, map[string]interface{}{
			//"question": "What is the capital of France?",
			"toolbox":   toolbox,
			"objective": prompt,
		})
		if cot_err != nil {
			log.Fatalf("Error executing program: %v", cot_err)
		}

		//fmt.Printf("Answer: %s\n", result["answer"])
		log.Printf("COT Result: %s\n", cot_result)

		cot_jsonBytes, json_err := json.Marshal(cot_result)
		if json_err != nil {
			// handle error
		}
		cot_response := string(cot_jsonBytes)
		log.Println("cot_response:")
		log.Println(cot_response)

		log.Println("Here we should use the cot_response to actually call the tool:")

		// Prepare the call of the tool
		fmt.Println("📣 calling the tool!")
		fetchRequest := mcp.CallToolRequest{
			Request: mcp.Request{
				Method: "tools/call",
			},
		}
		fetchRequest.Params.Name = "feedbackQA"
		fetchRequest.Params.Arguments = map[string]interface{}{
			//"question": "whats bob blockchain address? and whats his budget?",
			//TODO Setup the choosen tool and add its Name and Arugments
			"question": "ss",
		}

		// Call the tool
		result, err := mcpClient.CallTool(ctx, fetchRequest)
		if err != nil {
			log.Fatalf("MCP client Failed to call the tool: %v", err)
		}
		// display the text content of result
		fmt.Println("Response From MCP tool:")
		//fmt.Println(result.Content[0].(map[string]interface{})["text"])
		//fmt.Println(result)
		fmt.Println("MCP Tool Error?, %+v", result.IsError)

		//jsonBytes, err := json.Marshal(result.Content[0])
		jsonBytes, err := json.Marshal(result.Content[0])
		if err != nil {
			// handle error
		}
		tool_response := string(jsonBytes)

		fmt.Println(tool_response)

	}
}
