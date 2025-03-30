package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	commandTimeout = 10 * time.Second
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

	// Feedback tool
	feedbackTool := mcp.NewTool("feedback",
		mcp.WithDescription("Get feedback from the swechain blockchain enviroment for observations before and after taking actions"),
	)
	s.AddTool(feedbackTool, feedbackHandler)

	// Auction tools
	createAuctionTool := mcp.NewTool("create-auction",
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
	s.AddTool(createAuctionTool, createAuctionHandler)

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

	// Token tool
	tokenTool := mcp.NewTool("token",
		mcp.WithDescription("Transfer tokens between accounts"),
		mcp.WithString("from",
			mcp.Required(),
			mcp.Description("Sender's blockchain address"),
		),
		mcp.WithString("to",
			mcp.Required(),
			mcp.Description("Recipient's blockchain address"),
		),
		mcp.WithString("amount",
			mcp.Required(),
			mcp.Description("Amount of tokens to send"),
		),
	)
	s.AddTool(tokenTool, tokenHandler)
	winnderTool := mcp.NewTool("winnder-auction",

		mcp.WithDescription("Update the winnder of an auction"),
		mcp.WithString("status",
			mcp.Required(),
			mcp.Description("Updated status to closed"),
		),
		mcp.WithString("winner",
			mcp.Description("update with the winner bid address"),
		),
		mcp.WithString("from",
			mcp.Required(),
			mcp.Description("the address of the auction creator"),
		),
	)
	s.AddTool(winnderTool, winnerHandler)
}

/*
func winnerHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.

		CallToolResult, error) {
		args, err := validateAuctionParams(request)
		if err != nil {
			return returnErrorWithFeedback(err.Error())
		}

		output, err := runCommand(
			"swechaind",
			append([]string{"tx", "issuemarket", "update-auction"}, args...)...,
		)
		if err != nil {
			log.Printf("Update auction failed: %v", err)
			return returnErrorWithFeedback("Error: update-auction failed")
		}

		return withFeedback(output)
	}
*/

func winnerHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract arguments directly from the request
	args := []string{}

	// Add auction ID if provided
	if auctionID, ok := request.Params.Arguments["auction_id"].(string); ok {
		args = append(args, auctionID)
	}

	// Add other necessary parameters as needed
	// For example, if there's a winner parameter:
	if winner, ok := request.Params.Arguments["winner"].(string); ok {
		args = append(args, "--winner", winner)
	}

	output, err := runCommand(
		"swechaind",
		append([]string{"tx", "issuemarket", "update-auction"}, args...)...,
	)
	if err != nil {
		log.Printf("Update auction failed: %v", err)
		return nil, fmt.Errorf("Error: update-auction failed: %v", err)
	}
	return mcp.NewToolResultText(string(output)), nil
}

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

func feedbackHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	output, err := runCommand(
		"./bin/feedback",
	)
	if err != nil {
		log.Printf("feedback failed: %v", err)
	}
	return mcp.NewToolResultText(string(output)), nil
}

func createAuctionHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {

	params := []string{
		"tx", "issuemarket", "create-auction",
		request.Params.Arguments["issue"].(string),
		request.Params.Arguments["description"].(string),
		request.Params.Arguments["status"].(string),
		request.Params.Arguments["winner"].(string),
		"--from", request.Params.Arguments["from"].(string),
		"--yes",
		"--output", "json",
	}

	output, err := runCommand("swechaind", params...)
	if err != nil {
		log.Printf("Create auction failed: %v", err)
	}

	return mcp.NewToolResultText(string(output)), nil
}

func createBidHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params := []string{
		"tx", "issuemarket", "create-bid",
		request.Params.Arguments["auctionId"].(string),
		request.Params.Arguments["bidder"].(string),
		request.Params.Arguments["amount"].(string),
		request.Params.Arguments["description"].(string),
		"--from", request.Params.Arguments["from"].(string),
		"--yes",
		"--output", "json",
	}

	output, err := runCommand("swechaind", params...)
	if err != nil {
		log.Printf("Create bid failed: %v", err)
	}
	return mcp.NewToolResultText(string(output)), nil
}
