package main

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// swechaind query bank balances alice --output json;
//TODO generate report from trajs
//TODO analyze generated image report using multimodal model.
//TODO get address tool e.g. swechaind keys list --output json | jq '.[] | select(.name == "bob") | .address'

func main() {
	// Create MCP server
	s := server.NewMCPServer(
		"swechain-mcp-server",
		"1.0.0",
	)
	// Start Tools
	// Add a tool
	send_tool := mcp.NewTool("send",
		mcp.WithDescription("sends tokens from one account to another"),
		mcp.WithString("from",
			mcp.Required(),
			mcp.Description("sender account"),
		),
		mcp.WithString("to",
			mcp.Required(),
			mcp.Description("receiver account"),
		),
	)
	// Add a tool handler
	s.AddTool(send_tool, sendHandler)

	// Add balance tool
	balance_tool := mcp.NewTool("balance",
		mcp.WithDescription("gets a balance for an account"),
		mcp.WithString("account",
			mcp.Required(),
			mcp.Description("account to query the balance"),
		),
	)
	// Add a balance tool handler
	s.AddTool(balance_tool, balanceHandler)
	// End Tools
	fmt.Println("🚀 Server started")
	// Start the stdio server
	if err := server.ServeStdio(s); err != nil {
		fmt.Printf("😡 Server error: %v\n", err)
	}
	fmt.Println("👋 Server stopped")
}

func balanceHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {

	account, ok := request.Params.Arguments["account"].(string)
	//fmt.Println(account)
	if !ok {
		return mcp.NewToolResultError("account must be a string"), nil
	}
	//cmd := exec.Command("/home/maf/go/bin/swechaind query bank balances", "-s", account, "--output json")
	cmd := exec.Command("swechaind", "query", "bank", "balances", account, "--output", "json")
	output, err := cmd.Output()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	//content := string(output)
	content := string(output)

	return mcp.NewToolResultText(content), nil
}

func sendHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {

	to, ok := request.Params.Arguments["to"].(string)
	if !ok {
		return mcp.NewToolResultError("to and form must be strings"), nil
	}
	from, ok := request.Params.Arguments["from"].(string)
	if !ok {
		return mcp.NewToolResultError("to and form must be strings"), nil
	}

	// swechaind tx bank send [from_key_or_address] [to_address] [amount] [flags]
	cmd := exec.Command("swechaind", "tx", "bank", "send", from, to, "111token", "--from", from, "--output", "json", "--yes")

	output, err := cmd.Output()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	//content := string(output)
	content := string(output)

	return mcp.NewToolResultText(content), nil
}
