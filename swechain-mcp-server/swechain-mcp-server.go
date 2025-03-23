package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

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
		mcp.WithString("agent_name",
			mcp.Required(),
			mcp.Description("agent_name is your given name, and it is used to RAG over your memories"),
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
		mcp.WithDescription("sends an amount from one account's address to another, use this tool when trying to send tokens to another address from your address"),
		mcp.WithString("from",
			mcp.Required(),
			mcp.Description("your account address"),
		),
		mcp.WithString("to",
			mcp.Required(),
			mcp.Description("account address you want to send to"),
		),
		mcp.WithString("amount",
			mcp.Required(),
			mcp.Description("amount to send, example: it just numbers like 100 or 200 or 300, etc."),
		),
	)
	s.AddTool(send_tool, sendHandler)

	//
	// keys tool
	//
	addr_tool := mcp.NewTool("addr",
		mcp.WithDescription("give an account name to get the account address"),

		mcp.WithString("account_name",
			mcp.Required(),
			mcp.Description("the name of the person you want to get its address"),
		),
	)
	s.AddTool(addr_tool, addrHandler)

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

	agent_name, ok := request.Params.Arguments["agent_name"].(string)
	if !ok {
		return nil, errors.New("Error: mem agent_name parameter")
	}

	//cmd := exec.Command("./bin/feedback", "--env", "ghissuemarket", "--mode", "infer", "--query", query)
	cmd := exec.Command("./bin/feedback", "--env", strings.ToLower(agent_name), "--mode", "infer", "--query", query)
	//cmd := exec.Command("./bin/feedback", "--env", "bob", "--mode", "infer", "--query", query)

	output, err := cmd.Output()
	fmt.Println(output)
	if err != nil {
		fmt.Println(err)
		return nil, errors.New("Error: rag/memory")
	}
	content := string(output)
	return mcp.NewToolResultText(content), nil
}

func balanceHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {

	account, ok := request.Params.Arguments["account"].(string)
	if !ok {
		return nil, errors.New("account must be a string")
	}
	//cmd := exec.Command("/home/maf/go/bin/swechaind query bank balances", "-s", account, "--output json")
	cmd := exec.Command("swechaind", "query", "bank", "balances", account, "--output", "json")
	output, err := cmd.Output()
	if err != nil {
		return nil, errors.New("Error: swechaind query bank balances")
	}
	content := string(output)
	return mcp.NewToolResultText(content), nil
}

func sendHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {

	to, ok := request.Params.Arguments["to"].(string)
	if !ok {
		return nil, errors.New("the 'to' parameter is bad!")
	}

	from, ok := request.Params.Arguments["from"].(string)
	if !ok {
		return nil, errors.New("the 'from' parameter is bad")
	}

	amount, ok := request.Params.Arguments["amount"].(string)
	if !ok {
		return nil, errors.New("the 'amount' parameter is bad")
	}

	// swechaind tx bank send [from_key_or_address] [to_address] [amount] [flags]
	// swechaind tx bank send <from_address> <to_address> <amount> --chain-id=<chain_id> --fees=<fee_amount> --gas=<gas_limit> --yes
	//cmd := exec.Command("swechaind", "tx", "bank", "send", from, to, "111token", "--from", from, "--output", "json", "--yes")
	cmd := exec.Command("swechaind", "tx", "bank", "send", strings.ToLower(from), strings.ToLower(to), amount, "--from", strings.ToLower(from), "--output", "json", "--yes")

	output, err := cmd.Output()
	if err != nil {
		return nil, errors.New("Error: all addresses are Cosmos sdk addresses like cosmos1fgs3u5hvkrh50y7nphrqyjur27jaahh4h3c86w which is a Bech32-encoded, alphanumeric string with a prefix, uniquely identifying an account on a Cosmos-based blockchain.")
	}
	content := string(output)
	return mcp.NewToolResultText(content), nil
}

func addrHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	account_name, ok := request.Params.Arguments["account_name"].(string)
	if !ok {
		return nil, errors.New("use your intended 'account_name' to get its valid address!")
	}

	//  swechaind keys list --output json | jq '.[] | select(.name == "bob") | .address'
	// swechaind tx bank send [from_key_or_address] [to_address] [amount] [flags]
	// swechaind keys list
	//  swechaind keys list --output json | jq '.[] | select(.name == "bob") | .address'

	//cmd := exec.Command("swechaind keys list --output json | jq '.[] | select(.name == ", account_name, ") | .address'")

	//cmdString := fmt.Sprintf("/home/maf/go/bin/swechaind keys list --output json | jq '.[] | select(.name == \"%s\") | .address'", accountName)
	// Execute the command using a shell
	//cmd := exec.Command("bash", "-c", cmdString)

	//cmd := exec.Command("swechaind", "keys", "list")
	cmd := exec.Command("swechaind", "keys", "show", strings.ToLower(account_name), "-a")

	output, err := cmd.Output()
	if err != nil {
		return nil, errors.New("Error: if you use a valid account_name, you will get a valid address to be able to send to")
	}
	content := string(output)
	return mcp.NewToolResultText(content), nil
}
