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

func main() {
	// Create MCP server
	s := server.NewMCPServer(
		"swechain-mcp-server",
		"1.0.0",
	)
	/// Start Tools
	// Add a tool
	send_tool := mcp.NewTool("send",
		mcp.WithDescription("sends tokens from one account to another"),
		mcp.WithString("from",
			mcp.Required(),
			mcp.Description("sender account address"),
		),
		mcp.WithString("to",
			mcp.Required(),
			mcp.Description("receiver account address"),
		),
	)
	// Add a tool handler
	s.AddTool(send_tool, sendHandler)
	// End of send_tool //

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
