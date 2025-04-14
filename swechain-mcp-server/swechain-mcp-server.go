package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	commandTimeout = 6000000 * time.Second
)

func init() {
	log.SetOutput(os.Stdout)
}

// runCommand executes a command with timeout and returns combined output
func runCommand(name string, arg ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, arg...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	log.Printf("Executing command: %s %v", name, arg)
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("command failed: %v\nSTDOUT: %s\nSTDERR: %s",
			err, stdout.String(), stderr.String())
	}

	return stdout.String(), nil
}

func main() {
	s := server.NewMCPServer(
		"swechain-mcp-server",
		"1.0.0",
	)

	// Register all tools
	registerTools(s)

	log.Println("Server started")
	if err := server.ServeStdio(s); err != nil {
		log.Printf("Server error: %v\n", err)
	}
	log.Println("Server stopped")
}

func registerTools(s *server.MCPServer) {

	// Issue tool
	/*
		issueTool := mcp.NewTool("issueQA",
			mcp.WithDescription("Ask specific questions regarding your issues?"),
			mcp.WithString("question",
				mcp.Required(),
				mcp.Description("a question about your issues that can help you decide your next action"),
			),
		)
		s.AddTool(issueTool, issueHandler)
	*/

	// Feedback tool
	feedbackQATool := mcp.NewTool("feedbackQA",
		mcp.WithDescription("Ask specific questions regarding the swechain blockchain and enviroment feedback, i.e. Is there any open auctions that I can bid on? or should I close any of my own auctions? etc."),
		mcp.WithString("question",
			mcp.Required(),
			mcp.Description("a question about the enviroment that can help you decide your next action"),
		),
	)
	s.AddTool(feedbackQATool, feedbackQAHandler)

	/*
		// Feedback tool
		feedbackTool := mcp.NewTool("feedback",
			mcp.WithDescription("Get feedback from the swechain blockchain enviroment for observations before and after taking actions"),
		)
		s.AddTool(feedbackTool, feedbackHandler)
	*/

	// Auction tools
	openAuctionTool := mcp.NewTool("open-auction",
		mcp.WithDescription("Create new auction"),
		mcp.WithString("issue",
			mcp.Required(),
			mcp.Description("Auction issue identifier, that is the issue that is being auction as part of software engineering outsourcing"),
		),
		mcp.WithString("description",
			mcp.Required(),
			mcp.Description("Auction description"),
		),
		mcp.WithString("status",
			mcp.Required(),
			mcp.Description("Auction status can be open or closed"),
		),
		mcp.WithString("winner",
			mcp.Required(),
			mcp.Description("Auction winner, for a new auction keep it TBD until you know the address of the winner"),
		),
		mcp.WithString("from",
			mcp.Required(),
			mcp.Description("Auction creator, your address"),
		),
	)
	s.AddTool(openAuctionTool, openAuctionHandler)

	createBidTool := mcp.NewTool("create-bid",
		mcp.WithDescription("Place bid on auction"),
		mcp.WithString("auctionId",
			mcp.Required(),
			mcp.Description("Auction identifier"),
		),
		mcp.WithString("bidder",
			mcp.Required(),
			mcp.Description("Bidder account name"),
		),
		mcp.WithString("amount",
			mcp.Required(),
			mcp.Description("Bid amount"),
		),
		mcp.WithString("description",
			mcp.Required(),
			mcp.Description("Bid description"),
		),
		mcp.WithString("from",
			mcp.Required(),
			mcp.Description("Transaction sender"),
		),
	)
	s.AddTool(createBidTool, createBidHandler)

	payTool := mcp.NewTool("pay",
		mcp.WithDescription("pay bidder for your closed auction"),
		mcp.WithString("from",
			mcp.Required(),
			mcp.Description("the address of the auction creator"),
		),
		mcp.WithString("to",
			mcp.Required(),
			mcp.Description("the address of the winner bidder who made the bid on the auction. Important this must never be equal to the address of the auctineer who created the bid"),
		),
		mcp.WithString("amount",
			mcp.Required(),
			mcp.Description("Amount of tokens to send the bidder, thats his bid amount"),
		),
	)
	s.AddTool(payTool, payHandler)

	closeAuctionTool := mcp.NewTool("close-auction",
		mcp.WithDescription("Update auction status to closed and also pay the choosen and accepted bid by transfering tokens to the address of the bidder who made the bid on the auction, tokens of his exact bid amount. Only use this tool if and only if there are any bids on that specific auction"),
		mcp.WithString("auctionId",
			mcp.Required(),
			mcp.Description("The auctionId of the auction that you intend to close"),
		),
		mcp.WithString("status",
			mcp.Required(),
			mcp.Description("Updated status to closed"),
		),
		mcp.WithString("issue",
			mcp.Required(),
			mcp.Description("issue of the auction"),
		),
		mcp.WithString("description",
			mcp.Required(),
			mcp.Description("auction description"),
		),
		mcp.WithString("winner",
			mcp.Description("update with the winner bidder address"),
		),
		mcp.WithString("from",
			mcp.Required(),
			mcp.Description("the address of the auction creator"),
		),
	)
	s.AddTool(closeAuctionTool, closeAuctionHandler)
}

/*
func tokenHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.

		CallToolResult, error) {
		to, ok := request.Params.Arguments["to"].(string)
		if !ok {
			log.Printf("token to")
		}

		from, ok := request.Params.Arguments["from"].(string)
		if !ok {
			log.Printf("token from")
		}

		amount, ok := request.Params.Arguments["amount"].(string)
		if !ok {
			log.Printf("token amount")
		}

		output, err := runCommand(
			"swechaind",
			"tx", "bank", "send",
			strings.ToLower(from),
			strings.ToLower(to),
			amount,
			"--from", strings.ToLower(from),
			"--output", "json",
			"--yes",
		)

		if err != nil {
			log.Printf("feedback failed: %v", err)
		}

		//return withFeedback(output)
		return mcp.NewToolResultText(string(output)), nil
	}
*/
/*
func feedbackHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	output, err := runCommand(
		"./bin/feedback",
	)
	if err != nil {
		log.Printf("feedback failed: %v", err)
	}
	return mcp.NewToolResultText(string(output)), nil
}
*/
/*
func issueHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params := []string{
		request.Params.Arguments["question"].(string),
	}

	output, err := runCommand("./bin/issue", params...)
	if err != nil {
		log.Printf("issue tool failed: %v", err)
	}

	return mcp.NewToolResultText(string(output)), nil
}
*/

func feedbackQAHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params := []string{
		request.Params.Arguments["question"].(string),
	}

	output, err := runCommand("./bin/feedbackQA", params...)
	if err != nil {
		log.Printf("feedbackQA tool failed: %v", err)
	}

	return mcp.NewToolResultText(string(output)), nil
}

/*
	swechaind tx issuemarket create-auction \
	"a3" \
	"Fix critical security vulnerability" \
	"open" \
	"" \
	--from agent1 \
	--keyring-backend test \
	--chain-id swechain \
	--fees 200token
*/

/*

func openAuctionHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	//NOTE
	params := []string{
		"tx", "issuemarket", "create-auction",
		request.Params.Arguments["issue"].(string),
		request.Params.Arguments["description"].(string),
		request.Params.Arguments["status"].(string),
		request.Params.Arguments["winner"].(string),
		"--from", request.Params.Arguments["from"].(string),
		"--keyring-backend", "test",
		"--chain-id", "swechain",
		"--fees", "200token",
		"--yes",
		"--output", "json",
	}

	output, err := runCommand("swechaind", params...)
	if err != nil {
		log.Printf("Create auction failed: %v", err)
	}

	return mcp.NewToolResultText(string(output)), nil
}
*/

func openAuctionHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	log.Printf("INFO: Handling 'open-auction' tool request. Params: %+v", request.Params.Arguments)

	// Extract arguments with type assertion (consider adding error checking here too)
	issue, _ := request.Params.Arguments["issue"].(string)
	description, _ := request.Params.Arguments["description"].(string)
	status, _ := request.Params.Arguments["status"].(string)
	winner, _ := request.Params.Arguments["winner"].(string) // Often "" for open-auction
	from, _ := request.Params.Arguments["from"].(string)

	// Construct command parameters
	params := []string{
		"tx", "issuemarket", "create-auction",
		issue,
		description,
		status,
		winner, // Pass winner even if empty, command should handle it
		"--from", from,
		"--keyring-backend", "test",
		"--chain-id", "swechain",
		"--fees", "200token",
		"--yes",            // Auto-confirm
		"--output", "json", // Request JSON output
	}
	log.Printf("INFO: Prepared swechaind command args: %v", params)

	// Execute the command
	log.Println("INFO: Executing swechaind command...")
	output, err := runCommand("swechaind", params...) // Assuming runCommand is defined

	// --- Logging and Error Handling ---
	// Log the raw output regardless of success or error
	log.Printf("DEBUG: swechaind command raw output:\n%s", string(output))

	// Check if the command execution itself failed
	if err != nil {
		log.Printf("ERROR: swechaind command execution failed: %v", err)
		// Return nil result and a detailed error, including the output which might contain error info from swechaind
		// Use %w to wrap the original error
		return nil, fmt.Errorf("swechaind command failed: %w. Output: %s", err, string(output))
	}

	// --- Success Case ---
	log.Println("INFO: swechaind command executed successfully.")
	// Create the successful tool result with the output text
	result := mcp.NewToolResultText(string(output))
	log.Printf("INFO: Returning successful tool result (TextContent length: %d)", len(string(output)))

	// Return the result and nil error for success
	return result, nil
}

func closeAuctionHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {

	// swechaind tx issuemarket update-auction 1 "BUG-123" "Fix critical security vulnerability" "closed" bob --from alice --yes --output json;
	params := []string{
		"tx", "issuemarket", "update-auction",
		request.Params.Arguments["auctionId"].(string),
		request.Params.Arguments["issue"].(string),
		request.Params.Arguments["description"].(string),
		request.Params.Arguments["status"].(string),
		request.Params.Arguments["winner"].(string),
		"--from", request.Params.Arguments["from"].(string),
		"--keyring-backend", "test",
		"--chain-id", "swechain",
		"--fees", "200token",
		"--yes",
		"--output", "json",
	}

	output, err := runCommand("swechaind", params...)
	if err != nil {
		log.Printf("Create bid failed: %v", err)
	}
	return mcp.NewToolResultText(string(output)), nil
}

func payHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// swechaind tx bank send  cosmos1ztfcwxc9kadjzur9mvxayjycdtmpldjaujgt2f  cosmos1r3j0jnkpzgn5kwkkrw98lh2v753r2egg95p0k3 100token --from cosmos1ztfcwxc9kadjzur9mvxayjycdtmpldjaujgt2f
	params_bank := []string{
		"tx", "bank", "send",
		request.Params.Arguments["from"].(string),
		request.Params.Arguments["to"].(string),
		//request.Params.Arguments["amount"].(string) + "token",
		request.Params.Arguments["amount"].(string),
		"--from", request.Params.Arguments["from"].(string),
		"--keyring-backend", "test",
		"--chain-id", "swechain",
		"--fees", "200token",
		"--yes",
		"--output", "json",
	}

	output_bank, err := runCommand("swechaind", params_bank...)
	if err != nil {
		log.Printf("send failed: %v", err)
	}

	return mcp.NewToolResultText(string(output_bank)), nil
}

func createBidHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params := []string{
		"tx", "issuemarket", "create-bid",
		request.Params.Arguments["auctionId"].(string),
		request.Params.Arguments["bidder"].(string),
		request.Params.Arguments["amount"].(string),
		request.Params.Arguments["description"].(string),
		"--from", request.Params.Arguments["from"].(string),
		"--keyring-backend", "test",
		"--chain-id", "swechain",
		"--fees", "200token",
		"--yes",
		"--output", "json",
	}

	output, err := runCommand("swechaind", params...)
	if err != nil {
		log.Printf("Create bid failed: %v", err)
	}
	return mcp.NewToolResultText(string(output)), nil
}
