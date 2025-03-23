package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// swechaind query bank balances alice --output json;
//TODO generate report from trajs
//TODO analyze generated image report using multimodal model.
//TODO get address tool e.g. swechaind keys list --output json | jq '.[] | select(.name == "bob") | .address'

//TODO  get-auction  Gets a auction by id
//TODO  get-bid      Gets a bid
//TODO  list-auction List all auction
//TODO  list-bid     List all bid

//TODO swechaind keys list

func main() {
	// Create MCP server
	s := server.NewMCPServer(
		"swechain-mcp-server",
		"1.0.0",
	)

	///
	/// Start Tools
	///

	//
	// mem tool
	//
	mem_tool := mcp.NewTool("memory",
		mcp.WithDescription("Memory enables adaptive decision-making, allowing you to adjust its strategies based on accumulated knowledge"),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("query the memory for real-time information"),
		),
	)

	s.AddTool(mem_tool, memHandler)

	//
	// balance tool
	//
	balance_tool := mcp.NewTool("balance",
		mcp.WithDescription("gets a balance for an account"),
		mcp.WithString("account",
			mcp.Required(),
			mcp.Description("account to query the balance"),
		),
	)

	s.AddTool(balance_tool, balanceHandler)

	//
	// send tool
	//
	send_tool := mcp.NewTool("send",
		mcp.WithDescription("sends tokens from one account's address to another"),
		mcp.WithString("from",
			mcp.Required(),
			mcp.Description("sender account address"),
		),
		mcp.WithString("to",
			mcp.Required(),
			mcp.Description("receiver account address"),
		),
	)
	s.AddTool(send_tool, sendHandler)

	//
	// keys tool
	//
	keys_tool := mcp.NewTool("keys",
		mcp.WithDescription("get account information such as account address, account name, account public key"),
	)
	s.AddTool(keys_tool, keysHandler)

	fmt.Println("🚀 Server started")
	// Start the stdio server
	if err := server.ServeStdio(s); err != nil {
		fmt.Printf("😡 Server error: %v\n", err)
	}
	fmt.Println("👋 Server stopped")
}

func memHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {

	// ./bin/feedback --env ghissuemarket --mode infer --query "What are the details of auction 456?"
	query, ok := request.Params.Arguments["query"].(string)
	if !ok {
		return nil, errors.New("Error: mem query parameter")
	}

	cmd := exec.Command("./bin/feedback", "--env", "ghissuemarket", "--mode", "infer", "--query", query)

	output, err := cmd.Output()
	if err != nil {
		return nil, errors.New("Error: rag/memory")
	}
	content := string(output)
	return mcp.NewToolResultText(content), nil
}

func balanceHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {

	account, ok := request.Params.Arguments["account"].(string)
	if !ok {
		return nil, errors.New("name must be a string")
	}
	//cmd := exec.Command("/home/maf/go/bin/swechaind query bank balances", "-s", account, "--output json")
	cmd := exec.Command("swechaind", "query", "bank", "balances", account, "--output", "json")
	output, err := cmd.Output()

	if err != nil {
		return nil, errors.New("name must be a string")
	}
	content := string(output)
	return mcp.NewToolResultText(content), nil
}

func sendHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {

	to, ok := request.Params.Arguments["to"].(string)

	if !ok {
		return nil, errors.New("the to parameter is bad!")
	}

	from, ok := request.Params.Arguments["from"].(string)

	if !ok {
		return nil, errors.New("name must be a string")
	}

	// swechaind tx bank send [from_key_or_address] [to_address] [amount] [flags]
	cmd := exec.Command("swechaind", "tx", "bank", "send", from, to, "111token", "--from", from, "--output", "json", "--yes")

	output, err := cmd.Output()
	if err != nil {
		return nil, errors.New("name must be a string")
	}
	content := string(output)
	return mcp.NewToolResultText(content), nil
}

func keysHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	//  swechaind keys list --output json | jq '.[] | select(.name == "bob") | .address'
	// swechaind tx bank send [from_key_or_address] [to_address] [amount] [flags]
	// swechaind keys list
	cmd := exec.Command("swechaind", "keys", "list")

	output, err := cmd.Output()
	if err != nil {
		return nil, errors.New("Error: `swechain keys list`")
	}
	content := string(output)
	return mcp.NewToolResultText(content), nil
}
