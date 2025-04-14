package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors" // Added for errors.Is
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp" // Added import
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// --- Configuration ---

const defaultCommandTimeout = 2 * time.Minute

type config struct {
	swechaindPath  string
	keyringBackend string
	chainID        string
	defaultFees    string
	commandTimeout time.Duration
}

func loadConfig() config {
	cfg := config{
		swechaindPath:  getEnv("SWECHAIND_PATH", "swechaind"),
		keyringBackend: getEnv("KEYRING_BACKEND", "test"),
		chainID:        getEnv("CHAIN_ID", "swechain"),
		defaultFees:    getEnv("DEFAULT_FEES", "200token"),
		commandTimeout: defaultCommandTimeout,
	}
	if timeoutStr := os.Getenv("COMMAND_TIMEOUT_SECONDS"); timeoutStr != "" {
		if timeoutSec, err := time.ParseDuration(timeoutStr + "s"); err == nil {
			cfg.commandTimeout = timeoutSec
		} else {
			log.Printf("WARN: Invalid COMMAND_TIMEOUT_SECONDS value '%s', using default %v", timeoutStr, defaultCommandTimeout)
		}
	}
	log.Printf("INFO: Configuration loaded: swechaindPath=%s, keyringBackend=%s, chainID=%s, defaultFees=%s, commandTimeout=%v",
		cfg.swechaindPath, cfg.keyringBackend, cfg.chainID, cfg.defaultFees, cfg.commandTimeout)
	return cfg
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// --- Main Setup ---

func init() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ldate | log.Ltime)
}

func main() {
	cfg := loadConfig()

	s := server.NewMCPServer(
		"swechain-mcp-server",
		"1.3.1", // Version bump reflecting error handling fix
	)

	registerTools(s, cfg)

	log.Println("INFO: Server started, listening on stdio...")
	if err := server.ServeStdio(s); err != nil {
		log.Printf("CRITICAL: Server error: %v", err)
		os.Exit(1)
	}
	log.Println("INFO: Server stopped gracefully.")
}

// --- Tool Registration ---

func registerTools(s *server.MCPServer, cfg config) {
	// Feedback QA Tool
	feedbackQATool := mcp.NewTool("feedbackQA",
		mcp.WithDescription(`Ask specific questions about the swechain blockchain environment state (e.g., open auctions, specific auction details) to inform your next action.
Example Usage (Illustrative): {"function": {"name": "feedbackQA", "arguments": {"question": "List open auctions with bids higher than 500token."}}} `),
		mcp.WithString("question", mcp.Required(), mcp.Description("A specific question about the blockchain environment state.")),
	)
	s.AddTool(feedbackQATool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return feedbackQAHandler(ctx, request, cfg)
	})

	// Open Auction Tool
	openAuctionTool := mcp.NewTool("open-auction",
		mcp.WithDescription(`Create a new auction for a specific issue. IMPORTANT: The 'from' address MUST be an address you control and exists in the agent's keyring. The address shown below is just an EXAMPLE for format guidance.
Example Usage (Illustrative): {"function": {"name": "open-auction", "arguments": {"issue": "your-org/your-repo/issues/15", "description": "Refactor authentication module for better performance", "status": "open", "winner": "TBD", "from": "cosmos1aqq8kv2m3zmnzr7k7z34j0zrsvl9p7un2xn4kf"}}} `),
		mcp.WithString("issue", mcp.Required(), mcp.Description("Issue identifier (e.g., 'owner/repo/issues/123').")),
		mcp.WithString("description", mcp.Required(), mcp.Description("Detailed description of the task/auction.")),
		mcp.WithString("status", mcp.Required(), mcp.Description("Initial status, should typically be 'open'.")),
		mcp.WithString("winner", mcp.Description("Leave empty or set to 'TBD' for a new auction.")),
		mcp.WithString("from", mcp.Required(), mcp.Description("Your blockchain address (must exist in keyring).")),
	)
	s.AddTool(openAuctionTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return openAuctionHandler(ctx, request, cfg)
	})

	// Create Bid Tool
	createBidTool := mcp.NewTool("create-bid",
		mcp.WithDescription(`Place a bid on an existing open auction. IMPORTANT: The 'bidder' and 'from' addresses MUST be addresses you control and exist in the agent's keyring. The addresses shown below are just EXAMPLES for format guidance.
Example Usage (Illustrative): {"function": {"name": "create-bid", "arguments": {"auctionId": "auction-gamma-42", "bidder": "cosmos1r3j0jnkpzgn5kwkkrw98lh2v753r2egg95p0k3", "amount": "750", "description": "Strong experience with auth modules, ready to start.", "from": "cosmos1r3j0jnkpzgn5kwkkrw98lh2v753r2egg95p0k3"}}} `),
		mcp.WithString("auctionId", mcp.Required(), mcp.Description("Identifier of the auction to bid on.")),
		mcp.WithString("bidder", mcp.Required(), mcp.Description("Your blockchain address (must exist in keyring).")),
		mcp.WithString("amount", mcp.Required(), mcp.Description("Bid amount (e.g., '500'). Denomination 'token' will be added automatically.")),
		mcp.WithString("description", mcp.Required(), mcp.Description("Optional description for your bid.")),
		mcp.WithString("from", mcp.Required(), mcp.Description("Your blockchain address (must exist in keyring, usually same as bidder).")),
	)
	s.AddTool(createBidTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return createBidHandler(ctx, request, cfg)
	})

	// Close Auction Tool
	closeAuctionTool := mcp.NewTool("close-auction",
		mcp.WithDescription(`Close an auction you created, specifying the winner. IMPORTANT: The 'from' address MUST be the auction creator's address you control and exists in the agent's keyring. The 'winner' address MUST be the actual winner. Addresses shown below are just EXAMPLES for format guidance. Payment must be done separately using the 'pay' tool.
Example Usage (Illustrative): {"function": {"name": "close-auction", "arguments": {"auctionId": "auction-gamma-42", "issue": "your-org/your-repo/issues/15", "description": "Refactor authentication module for better performance", "status": "closed", "winner": "cosmos1r3j0jnkpzgn5kwkkrw98lh2v753r2egg95p0k3", "from": "cosmos1aqq8kv2m3zmnzr7k7z34j0zrsvl9p7un2xn4kf"}}} `),
		mcp.WithString("auctionId", mcp.Required(), mcp.Description("Identifier of the auction you are closing.")),
		mcp.WithString("issue", mcp.Required(), mcp.Description("The original issue identifier of the auction.")),
		mcp.WithString("description", mcp.Required(), mcp.Description("The original description of the auction.")),
		mcp.WithString("status", mcp.Required(), mcp.Description("Set status to 'closed'.")),
		mcp.WithString("winner", mcp.Required(), mcp.Description("The blockchain address of the winning bidder.")),
		mcp.WithString("from", mcp.Required(), mcp.Description("Your blockchain address (the auction creator).")),
	)
	s.AddTool(closeAuctionTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return closeAuctionHandler(ctx, request, cfg)
	})

	// Pay Tool (Simplified Bank Send)
	payTool := mcp.NewTool("pay",
		mcp.WithDescription(`Send tokens to another address (e.g., pay a winning bidder). IMPORTANT: The 'from' address MUST be an address you control, exists in the agent's keyring, and has sufficient funds. The 'to' address MUST be the intended recipient. Addresses shown below are just EXAMPLES for format guidance.
Example Usage (Illustrative): {"function": {"name": "pay", "arguments": {"from": "cosmos1aqq8kv2m3zmnzr7k7z34j0zrsvl9p7un2xn4kf", "to": "cosmos1r3j0jnkpzgn5kwkkrw98lh2v753r2egg95p0k3", "amount": "750"}}} `),
		mcp.WithString("from", mcp.Required(), mcp.Description("Your blockchain address (sender, must exist in keyring).")),
		mcp.WithString("to", mcp.Required(), mcp.Description("The recipient's blockchain address.")),
		mcp.WithString("amount", mcp.Required(), mcp.Description("Amount of tokens to send (e.g., '100'). Denomination 'token' will be added automatically.")),
	)
	s.AddTool(payTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return payHandler(ctx, request, cfg)
	})

	// Create and Fund Address Tool
	createAndFundTool := mcp.NewTool("create-and-fund-address",
		mcp.WithDescription(`Creates a new blockchain address for a given username in the keyring and funds it from a specified funder address. IMPORTANT: The 'funder_address' MUST be an address you control, exists in the agent's keyring, and has sufficient funds. The address shown below is just an EXAMPLE for format guidance.
Example Usage (Illustrative): {"function": {"name": "create-and-fund-address", "arguments": {"username": "swe-agent-beta", "amount": "10000", "funder_address": "cosmos1ztfcwxc9kadjzur9mvxayjycdtmpldjaujgt2f"}}} `),
		mcp.WithString("username", mcp.Required(), mcp.Description("The name/label for the new key to be created in the keyring (e.g., 'agent1', 'testuser').")),
		mcp.WithString("amount", mcp.Required(), mcp.Description("Amount of tokens to send to the new address (e.g., '10000'). Denomination 'token' will be added automatically.")),
		mcp.WithString("funder_address", mcp.Required(), mcp.Description("The existing blockchain address that will send the funds (must exist in the keyring and have sufficient balance).")),
	)
	s.AddTool(createAndFundTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return createAndFundAddressHandler(ctx, request, cfg)
	})

	log.Printf("INFO: Tool registration complete.")
}

// --- Tool Handlers ---

// Helper function for safe argument extraction
func getStringArg(args map[string]interface{}, key string) (string, error) {
	val, ok := args[key]
	if !ok {
		return "", fmt.Errorf("Error preparing tool arguments: Missing required argument '%s'. Suggestion: Provide a value for the '%s' argument.", key, key)
	}
	strVal, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("Error preparing tool arguments: Argument '%s' has incorrect type. Expected string, got %T. Suggestion: Ensure the value for '%s' is provided as a JSON string.", key, val, key)
	}
	return strVal, nil
}

// feedbackQAHandler executes the feedbackQA script/binary.
func feedbackQAHandler(ctx context.Context, request mcp.CallToolRequest, cfg config) (*mcp.CallToolResult, error) {
	toolName := "feedbackQA"
	log.Printf("INFO: Handling '%s' tool request.", toolName)

	question, err := getStringArg(request.Params.Arguments, "question")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}

	stdout, stderr, err := runCommand(ctx, cfg, "./bin/feedbackQA", question)
	if err != nil {
		// Pass cfg to parseAndEnhanceError
		enhancedErr := parseAndEnhanceError(cfg, toolName, err, stderr)
		log.Printf("ERROR: [%s] Enhanced error: %v", toolName, enhancedErr)
		return nil, enhancedErr
	}

	log.Printf("INFO: [%s] executed successfully.", toolName)
	return mcp.NewToolResultText(stdout), nil
}

// openAuctionHandler creates an auction using swechaind.
func openAuctionHandler(ctx context.Context, request mcp.CallToolRequest, cfg config) (*mcp.CallToolResult, error) {
	toolName := "open-auction"
	log.Printf("INFO: Handling '%s' tool request.", toolName)

	issue, err := getStringArg(request.Params.Arguments, "issue")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	description, err := getStringArg(request.Params.Arguments, "description")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	status, err := getStringArg(request.Params.Arguments, "status")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	winner, _ := getStringArg(request.Params.Arguments, "winner") // Optional
	if winner == "" {
		winner = "TBD"
	}
	from, err := getStringArg(request.Params.Arguments, "from")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}

	params := []string{"tx", "issuemarket", "create-auction", issue, description, status, winner, "--from", from, "--keyring-backend", cfg.keyringBackend, "--chain-id", cfg.chainID, "--fees", cfg.defaultFees, "--yes", "--output", "json"}

	stdout, stderr, err := runCommand(ctx, cfg, cfg.swechaindPath, params...)
	if err != nil {
		// Pass cfg to parseAndEnhanceError
		enhancedErr := parseAndEnhanceError(cfg, toolName, err, stderr)
		log.Printf("ERROR: [%s] Enhanced error: %v", toolName, enhancedErr)
		return nil, enhancedErr
	}

	log.Printf("INFO: [%s] executed successfully.", toolName)
	return mcp.NewToolResultText(stdout), nil
}

// createBidHandler creates a bid using swechaind.
func createBidHandler(ctx context.Context, request mcp.CallToolRequest, cfg config) (*mcp.CallToolResult, error) {
	toolName := "create-bid"
	log.Printf("INFO: Handling '%s' tool request.", toolName)

	auctionID, err := getStringArg(request.Params.Arguments, "auctionId")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	bidder, err := getStringArg(request.Params.Arguments, "bidder")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	amount, err := getStringArg(request.Params.Arguments, "amount")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	amountWithDenom := amount + "token"
	description, err := getStringArg(request.Params.Arguments, "description")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	from, err := getStringArg(request.Params.Arguments, "from")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}

	params := []string{"tx", "issuemarket", "create-bid", auctionID, bidder, amountWithDenom, description, "--from", from, "--keyring-backend", cfg.keyringBackend, "--chain-id", cfg.chainID, "--fees", cfg.defaultFees, "--yes", "--output", "json"}

	stdout, stderr, err := runCommand(ctx, cfg, cfg.swechaindPath, params...)
	if err != nil {
		// Pass cfg to parseAndEnhanceError
		enhancedErr := parseAndEnhanceError(cfg, toolName, err, stderr)
		log.Printf("ERROR: [%s] Enhanced error: %v", toolName, enhancedErr)
		return nil, enhancedErr
	}

	log.Printf("INFO: [%s] executed successfully.", toolName)
	return mcp.NewToolResultText(stdout), nil
}

// closeAuctionHandler updates an auction to closed using swechaind.
func closeAuctionHandler(ctx context.Context, request mcp.CallToolRequest, cfg config) (*mcp.CallToolResult, error) {
	toolName := "close-auction"
	log.Printf("INFO: Handling '%s' tool request.", toolName)

	auctionID, err := getStringArg(request.Params.Arguments, "auctionId")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	issue, err := getStringArg(request.Params.Arguments, "issue")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	description, err := getStringArg(request.Params.Arguments, "description")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	status, err := getStringArg(request.Params.Arguments, "status")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	if status != "closed" {
		log.Printf("WARN: [%s] Status argument is '%s', expected 'closed'.", toolName, status)
	}
	winner, err := getStringArg(request.Params.Arguments, "winner")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	from, err := getStringArg(request.Params.Arguments, "from")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}

	params := []string{"tx", "issuemarket", "update-auction", auctionID, issue, description, status, winner, "--from", from, "--keyring-backend", cfg.keyringBackend, "--chain-id", cfg.chainID, "--fees", cfg.defaultFees, "--yes", "--output", "json"}

	stdout, stderr, err := runCommand(ctx, cfg, cfg.swechaindPath, params...)
	if err != nil {
		// Pass cfg to parseAndEnhanceError
		enhancedErr := parseAndEnhanceError(cfg, toolName, err, stderr)
		log.Printf("ERROR: [%s] Enhanced error: %v", toolName, enhancedErr)
		return nil, enhancedErr
	}

	log.Printf("INFO: [%s] executed successfully.", toolName)
	return mcp.NewToolResultText(stdout), nil
}

// payHandler sends tokens using swechaind tx bank send.
func payHandler(ctx context.Context, request mcp.CallToolRequest, cfg config) (*mcp.CallToolResult, error) {
	toolName := "pay"
	log.Printf("INFO: Handling '%s' tool request.", toolName)

	from, err := getStringArg(request.Params.Arguments, "from")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	to, err := getStringArg(request.Params.Arguments, "to")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	amount, err := getStringArg(request.Params.Arguments, "amount")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	amountWithDenom := amount + "token"
	if from == to {
		return nil, fmt.Errorf("Error executing tool '%s': 'from' and 'to' addresses cannot be the same. Suggestion: Provide distinct sender and recipient addresses.", toolName)
	}

	params := []string{"tx", "bank", "send", from, to, amountWithDenom, "--from", from, "--keyring-backend", cfg.keyringBackend, "--chain-id", cfg.chainID, "--fees", cfg.defaultFees, "--yes", "--output", "json"}

	stdout, stderr, err := runCommand(ctx, cfg, cfg.swechaindPath, params...)
	if err != nil {
		// Pass cfg to parseAndEnhanceError
		enhancedErr := parseAndEnhanceError(cfg, toolName, err, stderr)
		log.Printf("ERROR: [%s] Enhanced error: %v", toolName, enhancedErr)
		return nil, enhancedErr
	}

	log.Printf("INFO: [%s] executed successfully.", toolName)
	return mcp.NewToolResultText(stdout), nil
}

// Structure to parse the output of `swechaind keys add/show --output json`
type KeyInfo struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Address string `json:"address"`
	PubKey  string `json:"pubkey"`
}

// createAndFundAddressHandler creates a new key and sends funds to its address.
func createAndFundAddressHandler(ctx context.Context, request mcp.CallToolRequest, cfg config) (*mcp.CallToolResult, error) {
	toolName := "create-and-fund-address"
	log.Printf("INFO: Handling '%s' tool request.", toolName)

	username, err := getStringArg(request.Params.Arguments, "username")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	amountStr, err := getStringArg(request.Params.Arguments, "amount")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}
	funderAddress, err := getStringArg(request.Params.Arguments, "funder_address")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}

	log.Printf("INFO: [%s] Step 1: Ensuring key exists for username '%s'...", toolName, username)
	keysAddParams := []string{"keys", "add", username, "--keyring-backend", cfg.keyringBackend, "--output", "json"}
	keyCmdOutput, keyCmdStderr, err := runCommand(ctx, cfg, cfg.swechaindPath, keysAddParams...)
	keyAlreadyExists := false
	if err != nil {
		// Pass cfg to parseAndEnhanceError
		enhancedKeyAddErr := parseAndEnhanceError(cfg, toolName+"-key-add", err, keyCmdStderr)
		if strings.Contains(enhancedKeyAddErr.Error(), "already exists") {
			log.Printf("WARN: [%s] Key for username '%s' already exists. Attempting to retrieve address.", toolName, username)
			keyAlreadyExists = true
			keysShowParams := []string{"keys", "show", username, "--keyring-backend", cfg.keyringBackend, "--output", "json"}
			keyShowOutput, keyShowStderr, showErr := runCommand(ctx, cfg, cfg.swechaindPath, keysShowParams...)
			if showErr != nil {
				// Pass cfg to parseAndEnhanceError
				enhancedShowErr := parseAndEnhanceError(cfg, toolName+"-key-show", showErr, keyShowStderr)
				log.Printf("ERROR: [%s] Failed to retrieve existing key info for '%s': %v", toolName, username, enhancedShowErr)
				return nil, enhancedShowErr
			}
			keyCmdOutput = keyShowOutput
			log.Printf("INFO: [%s] Retrieved existing key info for '%s'.", toolName, username)
			err = nil // Clear original error
		} else {
			log.Printf("ERROR: [%s] Enhanced key creation error: %v", toolName, enhancedKeyAddErr)
			return nil, enhancedKeyAddErr
		}
	} else {
		log.Printf("INFO: [%s] Key creation command executed successfully.", toolName)
	}
	log.Printf("DEBUG: [%s] Key add/show output:\n%s", toolName, keyCmdOutput)

	var newKeyInfo KeyInfo
	jsonStart := strings.Index(keyCmdOutput, "{")
	jsonEnd := strings.LastIndex(keyCmdOutput, "}")
	var jsonToParse string
	if jsonStart != -1 && jsonEnd != -1 && jsonEnd > jsonStart {
		jsonToParse = keyCmdOutput[jsonStart : jsonEnd+1]
	} else {
		jsonToParse = keyCmdOutput
	}
	if err := json.Unmarshal([]byte(jsonToParse), &newKeyInfo); err != nil {
		errMsg := fmt.Sprintf("Error executing tool '%s': Failed to parse address from key command output. Output might be malformed. Suggestion: The key might exist but its details couldn't be read.", toolName)
		log.Printf("ERROR: [%s] Failed to parse address: %v. JSON attempted: %s", toolName, err, jsonToParse)
		return nil, fmt.Errorf(errMsg)
	}
	if newKeyInfo.Address == "" {
		errMsg := fmt.Sprintf("Error executing tool '%s': Parsed key info for '%s' but address field is empty. Suggestion: Key details might be incomplete or corrupted.", toolName, username)
		log.Printf("ERROR: [%s] Parsed empty address for '%s'. Data: %+v", toolName, username, newKeyInfo)
		return nil, fmt.Errorf(errMsg)
	}
	newAddress := newKeyInfo.Address
	log.Printf("INFO: [%s] Using address for '%s': %s", toolName, username, newAddress)

	log.Printf("INFO: [%s] Step 2: Funding address '%s' from funder '%s'...", toolName, newAddress, funderAddress)
	amountWithDenom := amountStr + "token"
	bankSendParams := []string{"tx", "bank", "send", funderAddress, newAddress, amountWithDenom, "--from", funderAddress, "--keyring-backend", cfg.keyringBackend, "--chain-id", cfg.chainID, "--fees", cfg.defaultFees, "--yes", "--output", "json"}
	fundStdout, fundStderr, fundErr := runCommand(ctx, cfg, cfg.swechaindPath, bankSendParams...)
	if fundErr != nil {
		// Pass cfg to parseAndEnhanceError
		enhancedFundErr := parseAndEnhanceError(cfg, toolName+"-funding", fundErr, fundStderr)
		log.Printf("ERROR: [%s] Enhanced funding error: %v", toolName, enhancedFundErr)
		return nil, enhancedFundErr
	}
	log.Printf("INFO: [%s] Funding command executed successfully.", toolName)
	log.Printf("DEBUG: [%s] Funding command output:\n%s", toolName, fundStdout)

	var successMsg string
	if keyAlreadyExists {
		successMsg = fmt.Sprintf("Key '%s' already existed with address '%s'. Successfully funded it with %s from '%s'.", username, newAddress, amountWithDenom, funderAddress)
	} else {
		successMsg = fmt.Sprintf("Successfully created key '%s' with address '%s' and funded it with %s from '%s'.", username, newAddress, amountWithDenom, funderAddress)
	}
	log.Printf("INFO: [%s] Task completed successfully.", toolName)
	finalResult := fmt.Sprintf("%s\nFunding Transaction Details:\n%s", successMsg, fundStdout)
	return mcp.NewToolResultText(finalResult), nil
}

// --- Error Parsing Helper ---

// parseAndEnhanceError analyzes command errors and stderr to provide LLM guidance.
// Added cfg config parameter
func parseAndEnhanceError(cfg config, toolName string, originalErr error, stderr string) error {
	log.Printf("DEBUG: [%s] Parsing error. Original: %v\nStderr: %s", toolName, originalErr, strings.TrimSpace(stderr))

	if errors.Is(originalErr, context.DeadlineExceeded) {
		return fmt.Errorf("Error executing tool '%s': The command timed out. The network might be busy or the operation took too long. Suggestion: Try the operation again later, perhaps with a longer timeout if configurable, or check network status.", toolName)
	}

	lowerStderr := strings.ToLower(stderr)

	if strings.Contains(lowerStderr, "key not found") {
		identifier := "" // Can be username or address
		re := regexp.MustCompile(`key\s+not\s+found\s+with\s+name\s+([^\s]+)\s+or\s+address\s+([^\s]+)`)
		matches := re.FindStringSubmatch(stderr) // Use original case stderr for regex if needed
		if len(matches) >= 3 {
			if strings.HasPrefix(matches[2], "cosmos1") {
				identifier = matches[2]
			} else {
				identifier = matches[1]
			}
		} else {
			parts := strings.SplitN(stderr, "key not found", 2)
			if len(parts) > 1 {
				fields := strings.Fields(strings.TrimSpace(parts[1]))
				if len(fields) > 0 {
					identifier = fields[0]
				}
			}
		}
		if identifier != "" {
			return fmt.Errorf("Error executing tool '%s': Key not found for identifier '%s'. Suggestion: Ensure the address or username used in '--from' (or 'bidder', 'funder_address') exists in the agent's keyring and is spelled correctly. Use 'create-and-fund-address' if the key needs to be created.", toolName, identifier)
		} else {
			return fmt.Errorf("Error executing tool '%s': A required key/address was not found in the keyring. Suggestion: Ensure the '--from' address (and any other address parameters like 'bidder' or 'funder_address') exists in the agent's keyring and is spelled correctly. Use 'create-and-fund-address' if the key needs to be created.", toolName)
		}
	}
	if strings.Contains(lowerStderr, "already exists") && strings.Contains(toolName, "key-add") {
		username := ""
		re := regexp.MustCompile(`key\s+with\s+name\s+([^\s]+)\s+already\s+exists`)
		matches := re.FindStringSubmatch(stderr)
		if len(matches) > 1 {
			username = matches[1]
		}
		if username != "" {
			return fmt.Errorf("Error executing tool '%s': Key with name '%s' already exists. Suggestion: Choose a different username or proceed assuming the key exists (the tool might handle this).", toolName, username)
		} else {
			return fmt.Errorf("Error executing tool '%s': The key/resource already exists. Suggestion: Choose a different identifier or check if the existing resource can be used.", toolName)
		}
	}

	if strings.Contains(lowerStderr, "insufficient funds") {
		addr := ""
		re := regexp.MustCompile(`account\s+(cosmos1[a-z0-9]+)\s+has\s+insufficient\s+funds`)
		matches := re.FindStringSubmatch(lowerStderr)
		if len(matches) > 1 {
			addr = matches[1]
		}
		if addr != "" {
			// Use cfg.defaultFees here
			return fmt.Errorf("Error executing tool '%s': Address '%s' has insufficient funds to complete the transaction (including fees). Suggestion: Check the address balance or obtain more funds before retrying. Ensure the specified fee ('%s') is appropriate.", toolName, addr, cfg.defaultFees)
		} else {
			// Use cfg.defaultFees here
			return fmt.Errorf("Error executing tool '%s': The specified 'from' address has insufficient funds to complete the transaction (including fees). Suggestion: Check the address balance or obtain more funds before retrying. Ensure the specified fee ('%s') is appropriate.", toolName, cfg.defaultFees)
		}
	}

	if strings.Contains(lowerStderr, "account sequence mismatch") {
		expected, got := "", ""
		re := regexp.MustCompile(`expected\s+(\d+),\s+got\s+(\d+)`)
		matches := re.FindStringSubmatch(lowerStderr)
		if len(matches) > 2 {
			expected, got = matches[1], matches[2]
		}
		if expected != "" && got != "" {
			return fmt.Errorf("Error executing tool '%s': Account sequence mismatch (expected %s, got %s). This often happens with concurrent transactions. Suggestion: Wait a moment and try the transaction again. If it persists, query the account's current sequence number.", toolName, expected, got)
		} else {
			return fmt.Errorf("Error executing tool '%s': Account sequence mismatch. This might be due to concurrent transactions or network issues. Suggestion: Wait a moment and try the transaction again.", toolName)
		}
	}

	if strings.Contains(lowerStderr, "invalid coins") || strings.Contains(lowerStderr, "invalid amount") {
		amount := ""
		re := regexp.MustCompile(`invalid\s+coins:\s+([^\s]+)`)
		matches := re.FindStringSubmatch(lowerStderr)
		if len(matches) > 1 {
			amount = matches[1]
		}
		if amount != "" {
			return fmt.Errorf("Error executing tool '%s': Invalid amount or coin denomination specified (e.g., '%s'). Suggestion: Ensure amounts are positive integers and the denomination (like 'token') is correct and appended where necessary by the tool.", toolName, amount)
		} else {
			return fmt.Errorf("Error executing tool '%s': Invalid amount or coin denomination specified. Suggestion: Ensure amounts are positive integers and the denomination (like 'token') is correct.", toolName)
		}
	}

	if strings.Contains(lowerStderr, "auction not found") || strings.Contains(lowerStderr, "auction does not exist") {
		id := ""
		re := regexp.MustCompile(`auction\s+([^\s]+)\s+not\s+found`)
		matches := re.FindStringSubmatch(lowerStderr)
		if len(matches) > 1 {
			id = matches[1]
		}
		if id != "" {
			return fmt.Errorf("Error executing tool '%s': Auction with ID '%s' was not found. Suggestion: Verify the 'auctionId' is correct. Use 'feedbackQA' to query existing auctions.", toolName, id)
		} else {
			return fmt.Errorf("Error executing tool '%s': The specified auction ID was not found. Suggestion: Verify the 'auctionId' is correct. Use 'feedbackQA' to query existing auctions.", toolName)
		}
	}
	if strings.Contains(lowerStderr, "bid does not exist") {
		return fmt.Errorf("Error executing tool '%s': The specified bid was not found. Suggestion: Verify the bid details or auction ID.", toolName)
	}
	if strings.Contains(lowerStderr, "auction is not open") || strings.Contains(lowerStderr, "auction is closed") {
		state := "not open or closed"
		if strings.Contains(lowerStderr, "not open") {
			state = "not open"
		}
		if strings.Contains(lowerStderr, "closed") {
			state = "closed"
		}
		return fmt.Errorf("Error executing tool '%s': The operation failed because the auction is currently '%s'. Suggestion: Check the auction status using 'feedbackQA' before attempting operations like bidding or closing.", toolName, state)
	}
	if strings.Contains(lowerStderr, "unauthorized") {
		return fmt.Errorf("Error executing tool '%s': Unauthorized operation. Suggestion: Ensure the 'from' address has the necessary permissions (e.g., only the auction creator can close an auction).", toolName)
	}

	errMsg := fmt.Sprintf("Error executing tool '%s': The command failed with an unexpected error.", toolName)
	if stderr != "" {
		conciseStderr := strings.TrimSpace(stderr)
		firstLine := strings.SplitN(conciseStderr, "\n", 2)[0]
		if len(firstLine) > 150 {
			firstLine = firstLine[:150] + "..."
		}
		errMsg += fmt.Sprintf(" Details from command output: %s", firstLine)
	}
	errMsg += " Original error: %w"
	return fmt.Errorf(errMsg, originalErr)
}

// --- Command Execution ---

// runCommand executes a command with timeout. Returns: stdout string, stderr string, error
func runCommand(ctx context.Context, cfg config, name string, arg ...string) (string, string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, cfg.commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, name, arg...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	fullCmdStr := fmt.Sprintf("%s %s", name, strings.Join(arg, " "))
	log.Printf("DEBUG: Executing command: %s", fullCmdStr)

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime)
	stdoutStr, stderrStr := stdout.String(), stderr.String()

	if err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) { // Use errors.Is for checking wrapped timeout
			log.Printf("ERROR: Command timed out after %v: %s", duration, fullCmdStr)
			return stdoutStr, stderrStr, fmt.Errorf("command '%s' timed out after %v: %w", fullCmdStr, duration, context.DeadlineExceeded)
		}
		log.Printf("ERROR: Command failed after %v with error: %v. Command: %s", duration, err, fullCmdStr)
		log.Printf("DEBUG: Command STDOUT:\n%s", stdoutStr)
		log.Printf("DEBUG: Command STDERR:\n%s", stderrStr)
		return stdoutStr, stderrStr, fmt.Errorf("command '%s' failed: %w", fullCmdStr, err) // Wrap original error
	}

	log.Printf("INFO: Command executed successfully in %v: %s", duration, fullCmdStr)
	log.Printf("DEBUG: Command STDOUT:\n%s", stdoutStr)
	if stderrStr != "" {
		log.Printf("DEBUG: Command STDERR (non-empty on success):\n%s", stderrStr)
	}
	return stdoutStr, stderrStr, nil
}
