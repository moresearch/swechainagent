package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
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
	// Potential future config: Rate limiting settings, feedbackQA script path
}

// loadConfig loads configuration from environment variables or defaults.
// Best Practice: Centralized configuration loading.
func loadConfig() config {
	cfg := config{
		swechaindPath:  getEnv("SWECHAIND_PATH", "swechaind"),
		keyringBackend: getEnv("KEYRING_BACKEND", "test"), // 'test' backend is convenient but not for production keys. Use 'os' or 'file' with appropriate security.
		chainID:        getEnv("CHAIN_ID", "swechain"),
		defaultFees:    getEnv("DEFAULT_FEES", "200token"), // Ensure this fee is sufficient for the target network.
		commandTimeout: defaultCommandTimeout,
	}
	if timeoutStr := os.Getenv("COMMAND_TIMEOUT_SECONDS"); timeoutStr != "" {
		if timeoutSec, err := time.ParseDuration(timeoutStr + "s"); err == nil && timeoutSec > 0 {
			cfg.commandTimeout = timeoutSec
		} else {
			log.Printf("WARN: Invalid COMMAND_TIMEOUT_SECONDS value '%s', using default %v", timeoutStr, defaultCommandTimeout)
		}
	}
	// Best Practice: Logging loaded configuration for monitoring/debugging.
	log.Printf("INFO: Configuration loaded: swechaindPath=%s, keyringBackend=%s, chainID=%s, defaultFees=%s, commandTimeout=%v",
		cfg.swechaindPath, cfg.keyringBackend, cfg.chainID, cfg.defaultFees, cfg.commandTimeout)
	return cfg
}

// getEnv retrieves an environment variable or returns a fallback string.
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// --- Main Setup ---

// init sets up global logging configuration.
// Best Practice: Consistent logging setup.
func init() {
	log.SetOutput(os.Stdout)                                // Configure output (can be file, syslog etc.)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds) // Add microseconds for finer granularity
}

// main initializes configuration, server, tools, and starts the server loop.
func main() {
	cfg := loadConfig()

	// Best Practice: Clear server identification (name, version).
	s := server.NewMCPServer(
		"swechain-mcp-server",
		"1.4.0", // Version bump reflecting best practice review
	)

	registerTools(s, cfg)

	log.Println("INFO: Server started, listening on stdio...")
	// Best Practice: Proper handling of server run errors.
	if err := server.ServeStdio(s); err != nil {
		log.Printf("CRITICAL: Server error: %v", err)
		os.Exit(1) // Exit on critical server failure
	}
	log.Println("INFO: Server stopped gracefully.")
}

// --- Tool Registration ---

// registerTools defines all available tools and registers them with the server.
// Best Practice: Centralized tool registration.
func registerTools(s *server.MCPServer, cfg config) {

	// --- Tool: feedbackQA ---
	// Best Practice: Clear name, description, parameters, examples, and return value documentation.
	feedbackQATool := mcp.NewTool("feedbackQA",
		mcp.WithDescription(`(Read-Only Hint) Ask specific questions about the swechain blockchain environment state (e.g., open auctions, specific auction details, querying balances) to inform your next action. Uses an external script/tool.
Example Usage (Illustrative): {"function": {"name": "feedbackQA", "arguments": {"question": "What is the balance of cosmos1aqq8kv2m3zmnzr7k7z34j0zrsvl9p7un2xn4kf?"}}}
Expected Return: Text output from the underlying feedbackQA script/tool.`),
		mcp.WithString("question", mcp.Required(), mcp.Description("A specific question about the blockchain environment state (e.g., 'list open auctions', 'get balance cosmos1...')")),
	)
	s.AddTool(feedbackQATool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return feedbackQAHandler(ctx, request, cfg)
	})

	// --- Tool: create-and-fund-address ---
	// Best Practice: Clear name, description, parameters, examples, workflow guidance, idempotency handling, and return value documentation.
	createAndFundTool := mcp.NewTool("create-and-fund-address",
		mcp.WithDescription(`(Idempotent Hint, Open World Hint) Ensures a blockchain address exists FOR THE AGENT under a given username in the keyring and funds it. **Use this tool ONCE if you don't have an assigned address.** If the key already exists, it retrieves the address. Then, it attempts to fund the address. Parse the response to find the line starting with 'AGENT_ADDRESS:' and STORE this address for all future actions. IMPORTANT: The 'funder_address' MUST be a valid address with funds.
Example Usage (Illustrative): {"function": {"name": "create-and-fund-address", "arguments": {"username": "my-agent-identity", "amount": "10000", "funder_address": "cosmos1ztfcwxc9kadjzur9mvxayjycdtmpldjaujgt2f"}}}
Expected Return: A confirmation message including the 'AGENT_ADDRESS: <address>' line and the JSON details of the funding transaction upon success.`),
		mcp.WithString("username", mcp.Required(), mcp.Description("A unique name/label for the new key to be created for the agent (e.g., 'my-agent-01').")),
		mcp.WithString("amount", mcp.Required(), mcp.Description("Amount of tokens to send to the new address (e.g., '10000'). Denomination 'token' will be added automatically.")),
		mcp.WithString("funder_address", mcp.Required(), mcp.Description("The existing blockchain address that will send the funds (must exist in the keyring and have sufficient balance).")),
	)
	s.AddTool(createAndFundTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return createAndFundAddressHandler(ctx, request, cfg)
	})

	// --- Tool: open-auction ---
	// Best Practice: Clear name, description, parameters, examples, address usage guidance, and return value documentation.
	openAuctionTool := mcp.NewTool("open-auction",
		mcp.WithDescription(`(Open World Hint) Creates a new auction *on the blockchain* for a specific issue. IMPORTANT: Use the AGENT_ADDRESS you previously obtained from 'create-and-fund-address' for the 'from' parameter.
Example Usage (Illustrative): {"function": {"name": "open-auction", "arguments": {"issue": "your-org/your-repo/issues/15", "description": "Refactor auth module", "status": "open", "winner": "TBD", "from": "cosmos1aqq8kv2m3zmnzr7k7z34j0zrsvl9p7un2xn4kf"}}}
Expected Return: JSON details of the 'create-auction' transaction upon success.`),
		mcp.WithString("issue", mcp.Required(), mcp.Description("Issue identifier (e.g., 'owner/repo/issues/123').")),
		mcp.WithString("description", mcp.Required(), mcp.Description("Detailed description of the task/auction.")),
		mcp.WithString("status", mcp.Required(), mcp.Description("Initial status, should typically be 'open'.")),
		mcp.WithString("winner", mcp.Description("Leave empty or set to 'TBD' for a new auction.")),
		mcp.WithString("from", mcp.Required(), mcp.Description("Your AGENT_ADDRESS obtained previously.")),
	)
	s.AddTool(openAuctionTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return openAuctionHandler(ctx, request, cfg)
	})

	// --- Tool: create-bid ---
	// Best Practice: Clear name, description, parameters, examples, address usage guidance, and return value documentation.
	createBidTool := mcp.NewTool("create-bid",
		mcp.WithDescription(`(Open World Hint) Places a bid *on the blockchain* on an existing open auction. IMPORTANT: Use the AGENT_ADDRESS you previously obtained from 'create-and-fund-address' for the 'bidder' and 'from' parameters.
Example Usage (Illustrative): {"function": {"name": "create-bid", "arguments": {"auctionId": "auction-gamma-42", "bidder": "cosmos1r3j0jnkpzgn5kwkkrw98lh2v753r2egg95p0k3", "amount": "750", "description": "Ready to start.", "from": "cosmos1r3j0jnkpzgn5kwkkrw98lh2v753r2egg95p0k3"}}}
Expected Return: JSON details of the 'create-bid' transaction upon success.`),
		mcp.WithString("auctionId", mcp.Required(), mcp.Description("Identifier of the auction to bid on.")),
		mcp.WithString("bidder", mcp.Required(), mcp.Description("Your AGENT_ADDRESS obtained previously.")),
		mcp.WithString("amount", mcp.Required(), mcp.Description("Bid amount (e.g., '500'). Denomination 'token' will be added automatically.")),
		mcp.WithString("description", mcp.Required(), mcp.Description("Optional description for your bid.")),
		mcp.WithString("from", mcp.Required(), mcp.Description("Your AGENT_ADDRESS obtained previously.")),
	)
	s.AddTool(createBidTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return createBidHandler(ctx, request, cfg)
	})

	// --- Tool: close-auction ---
	// Best Practice: Clear name, description, parameters, examples, address usage guidance, and return value documentation.
	closeAuctionTool := mcp.NewTool("close-auction",
		mcp.WithDescription(`(Open World Hint) Updates an auction status to 'closed' *on the blockchain*, specifying the winner. IMPORTANT: Use the AGENT_ADDRESS you previously obtained for the 'from' parameter (must be the auction creator). The 'winner' address MUST be the actual winner's address. Payment is separate via 'pay'.
Example Usage (Illustrative): {"function": {"name": "close-auction", "arguments": {"auctionId": "auction-gamma-42", "issue": "your-org/your-repo/issues/15", "description": "Refactor auth module", "status": "closed", "winner": "cosmos1r3j0jnkpzgn5kwkkrw98lh2v753r2egg95p0k3", "from": "cosmos1aqq8kv2m3zmnzr7k7z34j0zrsvl9p7un2xn4kf"}}}
Expected Return: JSON details of the 'update-auction' transaction upon success.`),
		mcp.WithString("auctionId", mcp.Required(), mcp.Description("Identifier of the auction you are closing.")),
		mcp.WithString("issue", mcp.Required(), mcp.Description("The original issue identifier of the auction.")),
		mcp.WithString("description", mcp.Required(), mcp.Description("The original description of the auction.")),
		mcp.WithString("status", mcp.Required(), mcp.Description("Set status to 'closed'.")),
		mcp.WithString("winner", mcp.Required(), mcp.Description("The blockchain address of the winning bidder.")),
		mcp.WithString("from", mcp.Required(), mcp.Description("Your AGENT_ADDRESS (must be auction creator).")),
	)
	s.AddTool(closeAuctionTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return closeAuctionHandler(ctx, request, cfg)
	})

	// --- Tool: pay ---
	// Best Practice: Clear name, description, parameters, examples, address usage guidance, validation, and return value documentation.
	payTool := mcp.NewTool("pay",
		mcp.WithDescription(`(Open World Hint) Sends tokens from one address to another *on the blockchain*. IMPORTANT: Use the AGENT_ADDRESS you previously obtained for the 'from' parameter. Ensure it has sufficient funds. The 'to' address MUST be the intended recipient.
Example Usage (Illustrative): {"function": {"name": "pay", "arguments": {"from": "cosmos1aqq8kv2m3zmnzr7k7z34j0zrsvl9p7un2xn4kf", "to": "cosmos1r3j0jnkpzgn5kwkkrw98lh2v753r2egg95p0k3", "amount": "750"}}}
Expected Return: JSON details of the 'bank send' transaction upon success.`),
		mcp.WithString("from", mcp.Required(), mcp.Description("Your AGENT_ADDRESS obtained previously.")),
		mcp.WithString("to", mcp.Required(), mcp.Description("The recipient's blockchain address.")),
		mcp.WithString("amount", mcp.Required(), mcp.Description("Amount of tokens to send (e.g., '100'). Denomination 'token' will be added automatically.")),
	)
	s.AddTool(payTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return payHandler(ctx, request, cfg)
	})

	// Best Practice: Logging tool registration completion.
	log.Printf("INFO: Tool registration complete.")
	// Note: Progress reporting for long operations is not implemented in this basic stdio server.
	// Note: Rate limiting is not implemented; consider external mechanisms for production.
}

// --- Tool Handlers ---

// Helper function for safe argument extraction with improved error messages.
// Best Practice: Clear error messages for argument issues.
func getStringArg(args map[string]interface{}, key string) (string, error) {
	val, ok := args[key]
	if !ok {
		// Provide actionable error for missing argument
		return "", fmt.Errorf("Error preparing tool arguments: Missing required argument '%s'. Suggestion: Provide a value for the '%s' argument.", key, key)
	}
	strVal, ok := val.(string)
	if !ok {
		// Provide actionable error for incorrect type
		return "", fmt.Errorf("Error preparing tool arguments: Argument '%s' has incorrect type. Expected string, got %T. Suggestion: Ensure the value for '%s' is provided as a JSON string.", key, val, key)
	}
	// Add validation for empty strings if necessary for specific parameters
	// if strVal == "" {
	//     return "", fmt.Errorf("Error preparing tool arguments: Argument '%s' cannot be empty. Suggestion: Provide a non-empty string value.", key)
	// }
	return strVal, nil
}

// feedbackQAHandler executes the feedbackQA script/binary.
// Best Practice: Clear logging, calls runCommand, uses enhanced error parsing.
func feedbackQAHandler(ctx context.Context, request mcp.CallToolRequest, cfg config) (*mcp.CallToolResult, error) {
	toolName := "feedbackQA"
	log.Printf("INFO: [%s] Handling request.", toolName) // Consistent log prefix

	question, err := getStringArg(request.Params.Arguments, "question")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err // Return clear argument error directly
	}

	// Consider making the script path configurable via `cfg`
	scriptPath := "./bin/feedbackQA"
	log.Printf("DEBUG: [%s] Executing script: %s with question: %s", toolName, scriptPath, question)
	stdout, stderr, err := runCommand(ctx, cfg, scriptPath, question)
	if err != nil {
		enhancedErr := parseAndEnhanceError(cfg, toolName, err, stderr)
		log.Printf("ERROR: [%s] Execution failed: %v", toolName, enhancedErr) // Log enhanced error
		return nil, enhancedErr                                               // Return enhanced error
	}

	log.Printf("INFO: [%s] Execution successful.", toolName)
	// Return raw stdout as per documented return value
	return mcp.NewToolResultText(stdout), nil
}

// openAuctionHandler creates an auction using swechaind.
// Best Practice: Clear logging, atomic operation, calls runCommand, uses enhanced error parsing.
func openAuctionHandler(ctx context.Context, request mcp.CallToolRequest, cfg config) (*mcp.CallToolResult, error) {
	toolName := "open-auction"
	log.Printf("INFO: [%s] Handling request.", toolName)

	// Best Practice: Validate arguments early.
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

	// Best Practice: Keep tool operations focused.
	params := []string{"tx", "issuemarket", "create-auction", issue, description, status, winner, "--from", from, "--keyring-backend", cfg.keyringBackend, "--chain-id", cfg.chainID, "--fees", cfg.defaultFees, "--yes", "--output", "json"}
	stdout, stderr, err := runCommand(ctx, cfg, cfg.swechaindPath, params...)
	if err != nil {
		enhancedErr := parseAndEnhanceError(cfg, toolName, err, stderr)
		log.Printf("ERROR: [%s] Execution failed: %v", toolName, enhancedErr)
		return nil, enhancedErr
	}

	log.Printf("INFO: [%s] Execution successful.", toolName)
	// Return JSON tx details as per documented return value
	return mcp.NewToolResultText(stdout), nil
}

// createBidHandler creates a bid using swechaind.
// Best Practice: Clear logging, atomic operation, calls runCommand, uses enhanced error parsing.
func createBidHandler(ctx context.Context, request mcp.CallToolRequest, cfg config) (*mcp.CallToolResult, error) {
	toolName := "create-bid"
	log.Printf("INFO: [%s] Handling request.", toolName)

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
	amountWithDenom := amount + "token" // Denomination added internally
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
		enhancedErr := parseAndEnhanceError(cfg, toolName, err, stderr)
		log.Printf("ERROR: [%s] Execution failed: %v", toolName, enhancedErr)
		return nil, enhancedErr
	}

	log.Printf("INFO: [%s] Execution successful.", toolName)
	return mcp.NewToolResultText(stdout), nil
}

// closeAuctionHandler updates an auction to closed using swechaind.
// Best Practice: Clear logging, atomic operation, calls runCommand, uses enhanced error parsing.
func closeAuctionHandler(ctx context.Context, request mcp.CallToolRequest, cfg config) (*mcp.CallToolResult, error) {
	toolName := "close-auction"
	log.Printf("INFO: [%s] Handling request.", toolName)

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
	} // Warn but proceed
	winner, err := getStringArg(request.Params.Arguments, "winner")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	} // Winner required
	from, err := getStringArg(request.Params.Arguments, "from")
	if err != nil {
		log.Printf("ERROR: [%s] Argument error: %v", toolName, err)
		return nil, err
	}

	params := []string{"tx", "issuemarket", "update-auction", auctionID, issue, description, status, winner, "--from", from, "--keyring-backend", cfg.keyringBackend, "--chain-id", cfg.chainID, "--fees", cfg.defaultFees, "--yes", "--output", "json"}
	stdout, stderr, err := runCommand(ctx, cfg, cfg.swechaindPath, params...)
	if err != nil {
		enhancedErr := parseAndEnhanceError(cfg, toolName, err, stderr)
		log.Printf("ERROR: [%s] Execution failed: %v", toolName, enhancedErr)
		return nil, enhancedErr
	}

	log.Printf("INFO: [%s] Execution successful.", toolName)
	return mcp.NewToolResultText(stdout), nil
}

// payHandler sends tokens using swechaind tx bank send.
// Best Practice: Clear logging, atomic operation, validation, calls runCommand, uses enhanced error parsing.
func payHandler(ctx context.Context, request mcp.CallToolRequest, cfg config) (*mcp.CallToolResult, error) {
	toolName := "pay"
	log.Printf("INFO: [%s] Handling request.", toolName)

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

	// Best Practice: Input validation.
	if from == to {
		log.Printf("ERROR: [%s] Validation failed: 'from' and 'to' addresses are the same.", toolName)
		// Provide actionable error directly
		return nil, fmt.Errorf("Error executing tool '%s': 'from' and 'to' addresses cannot be the same. Suggestion: Provide distinct sender and recipient addresses.", toolName)
	}

	params := []string{"tx", "bank", "send", from, to, amountWithDenom, "--from", from, "--keyring-backend", cfg.keyringBackend, "--chain-id", cfg.chainID, "--fees", cfg.defaultFees, "--yes", "--output", "json"}
	stdout, stderr, err := runCommand(ctx, cfg, cfg.swechaindPath, params...)
	if err != nil {
		enhancedErr := parseAndEnhanceError(cfg, toolName, err, stderr)
		log.Printf("ERROR: [%s] Execution failed: %v", toolName, enhancedErr)
		return nil, enhancedErr
	}

	log.Printf("INFO: [%s] Execution successful.", toolName)
	return mcp.NewToolResultText(stdout), nil
}

// Structure to parse the output of `swechaind keys add/show --output json`.
// Best Practice: Using structs for JSON parsing.
type KeyInfo struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Address string `json:"address"`
	PubKey  string `json:"pubkey"`
	// Mnemonic string `json:"mnemonic"` // Excluded for security unless explicitly needed
}

// createAndFundAddressHandler ensures a key exists and funds it.
// Best Practice: Handles idempotency, clear logging, calls runCommand, uses enhanced error parsing, clear return value format.
func createAndFundAddressHandler(ctx context.Context, request mcp.CallToolRequest, cfg config) (*mcp.CallToolResult, error) {
	toolName := "create-and-fund-address"
	log.Printf("INFO: [%s] Handling request.", toolName)

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

	// Step 1: Ensure Key Exists (Idempotency Handling)
	log.Printf("INFO: [%s] Ensuring key exists for username '%s'.", toolName, username)
	keysAddParams := []string{"keys", "add", username, "--keyring-backend", cfg.keyringBackend, "--output", "json"}
	keyCmdOutput, keyCmdStderr, err := runCommand(ctx, cfg, cfg.swechaindPath, keysAddParams...)
	keyAlreadyExists := false
	if err != nil {
		enhancedKeyAddErr := parseAndEnhanceError(cfg, toolName+"-key-add", err, keyCmdStderr)
		// Best Practice: Check specific error condition for idempotency.
		if strings.Contains(enhancedKeyAddErr.Error(), "already exists") {
			log.Printf("WARN: [%s] Key '%s' already exists. Retrieving address.", toolName, username)
			keyAlreadyExists = true
			keysShowParams := []string{"keys", "show", username, "--keyring-backend", cfg.keyringBackend, "--output", "json"}
			keyShowOutput, keyShowStderr, showErr := runCommand(ctx, cfg, cfg.swechaindPath, keysShowParams...)
			if showErr != nil {
				enhancedShowErr := parseAndEnhanceError(cfg, toolName+"-key-show", showErr, keyShowStderr)
				log.Printf("ERROR: [%s] Failed to retrieve existing key info for '%s': %v", toolName, username, enhancedShowErr)
				return nil, enhancedShowErr // Return specific error from 'show'
			}
			keyCmdOutput = keyShowOutput // Use output from 'show'
			log.Printf("INFO: [%s] Retrieved existing key info.", toolName)
			err = nil // Handled the "already exists" case
		} else {
			// Unhandled error during 'add'
			log.Printf("ERROR: [%s] Key creation/check failed: %v", toolName, enhancedKeyAddErr)
			return nil, enhancedKeyAddErr
		}
	} else {
		log.Printf("INFO: [%s] Key created successfully.", toolName)
	}
	log.Printf("DEBUG: [%s] Key add/show output: %s", toolName, keyCmdOutput)

	// Step 2: Parse Address
	var newKeyInfo KeyInfo
	jsonStart := strings.Index(keyCmdOutput, "{")
	jsonEnd := strings.LastIndex(keyCmdOutput, "}")
	jsonToParse := keyCmdOutput
	if jsonStart != -1 && jsonEnd != -1 && jsonEnd > jsonStart {
		jsonToParse = keyCmdOutput[jsonStart : jsonEnd+1]
	}
	if err := json.Unmarshal([]byte(jsonToParse), &newKeyInfo); err != nil {
		errMsg := fmt.Sprintf("Error executing tool '%s': Failed to parse address from key command output. Suggestion: Check raw output if available.", toolName)
		log.Printf("ERROR: [%s] Failed to parse address: %v. JSON attempted: %s", toolName, err, jsonToParse)
		return nil, fmt.Errorf(errMsg)
	}
	if newKeyInfo.Address == "" {
		errMsg := fmt.Sprintf("Error executing tool '%s': Parsed key info for '%s' but address is empty. Suggestion: Key details might be corrupted.", toolName, username)
		log.Printf("ERROR: [%s] Parsed empty address for '%s'. Data: %+v", toolName, username, newKeyInfo)
		return nil, fmt.Errorf(errMsg)
	}
	newAddress := newKeyInfo.Address
	log.Printf("INFO: [%s] Using address '%s' for username '%s'.", toolName, newAddress, username)

	// Step 3: Fund Address
	log.Printf("INFO: [%s] Funding address '%s' from '%s'.", toolName, newAddress, funderAddress)
	amountWithDenom := amountStr + "token"
	bankSendParams := []string{"tx", "bank", "send", funderAddress, newAddress, amountWithDenom, "--from", funderAddress, "--keyring-backend", cfg.keyringBackend, "--chain-id", cfg.chainID, "--fees", cfg.defaultFees, "--yes", "--output", "json"}
	fundStdout, fundStderr, fundErr := runCommand(ctx, cfg, cfg.swechaindPath, bankSendParams...)
	if fundErr != nil {
		enhancedFundErr := parseAndEnhanceError(cfg, toolName+"-funding", fundErr, fundStderr)
		log.Printf("ERROR: [%s] Funding failed: %v", toolName, enhancedFundErr)
		return nil, enhancedFundErr
	}
	log.Printf("INFO: [%s] Funding successful.", toolName)
	log.Printf("DEBUG: [%s] Funding output: %s", toolName, fundStdout)

	// Step 4: Format Success Response
	// Best Practice: Clear and documented return value structure.
	statusMsg := fmt.Sprintf("Key '%s' ensured.", username)
	if !keyAlreadyExists {
		statusMsg = fmt.Sprintf("Key '%s' created.", username)
	}
	// Standardized line for easy parsing by LLM
	finalResult := fmt.Sprintf("%s\nAGENT_ADDRESS: %s\nFunded with %s from %s.\nFunding Tx Details:\n%s",
		statusMsg, newAddress, amountWithDenom, funderAddress, fundStdout)

	log.Printf("INFO: [%s] Task completed successfully.", toolName)
	return mcp.NewToolResultText(finalResult), nil
}

// --- Error Parsing Helper ---

// parseAndEnhanceError analyzes command errors and stderr to provide LLM guidance.
// Best Practice: Centralized, detailed error parsing providing actionable suggestions.
func parseAndEnhanceError(cfg config, toolName string, originalErr error, stderr string) error {
	// Log raw details for server admin
	log.Printf("DEBUG: [%s] Parsing error. Original: %v\nStderr: %s", toolName, originalErr, strings.TrimSpace(stderr))

	// Best Practice: Handle specific error types first (e.g., timeout).
	if errors.Is(originalErr, context.DeadlineExceeded) {
		return fmt.Errorf("Error executing tool '%s': The command timed out. Suggestion: Try again later or check network status.", toolName)
	}

	lowerStderr := strings.ToLower(stderr)

	// Best Practice: Check for common, actionable errors.
	if strings.Contains(lowerStderr, "key not found") {
		identifier := ""
		re := regexp.MustCompile(`key\s+not\s+found\s+with\s+name\s+([^\s]+)\s+or\s+address\s+([^\s]+)`)
		matches := re.FindStringSubmatch(stderr)
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
			return fmt.Errorf("Error executing tool '%s': Key not found for identifier '%s'. Suggestion: Ensure the address/username exists in the keyring and is spelled correctly. Use 'create-and-fund-address' if needed.", toolName, identifier)
		}
		return fmt.Errorf("Error executing tool '%s': A required key/address was not found. Suggestion: Ensure relevant addresses ('from', 'bidder', 'funder_address') exist in the keyring. Use 'create-and-fund-address' if needed.", toolName)
	}
	if strings.Contains(lowerStderr, "already exists") && strings.Contains(toolName, "key-add") { // Context specific check
		username := ""
		re := regexp.MustCompile(`key\s+with\s+name\s+([^\s]+)\s+already\s+exists`)
		matches := re.FindStringSubmatch(stderr)
		if len(matches) > 1 {
			username = matches[1]
		}
		if username != "" {
			return fmt.Errorf("Error executing tool '%s': Key with name '%s' already exists. Suggestion: Choose a different username or proceed (tool might handle this).", toolName, username)
		} // Specific message for idempotency handling
		return fmt.Errorf("Error executing tool '%s': The resource already exists. Suggestion: Choose a different identifier or check if the existing resource can be used.", toolName) // Generic fallback
	}
	if strings.Contains(lowerStderr, "insufficient funds") {
		addr := ""
		re := regexp.MustCompile(`account\s+(cosmos1[a-z0-9]+)\s+has\s+insufficient\s+funds`)
		matches := re.FindStringSubmatch(lowerStderr)
		if len(matches) > 1 {
			addr = matches[1]
		}
		// Include configured fee in suggestion
		if addr != "" {
			return fmt.Errorf("Error executing tool '%s': Address '%s' has insufficient funds (needs fees + amount). Suggestion: Check balance or get funds. Fee: '%s'.", toolName, addr, cfg.defaultFees)
		}
		return fmt.Errorf("Error executing tool '%s': Insufficient funds for the 'from' address. Suggestion: Check balance or get funds. Fee: '%s'.", toolName, cfg.defaultFees)
	}
	if strings.Contains(lowerStderr, "account sequence mismatch") {
		expected, got := "", ""
		re := regexp.MustCompile(`expected\s+(\d+),\s+got\s+(\d+)`)
		matches := re.FindStringSubmatch(lowerStderr)
		if len(matches) > 2 {
			expected, got = matches[1], matches[2]
		}
		if expected != "" && got != "" {
			return fmt.Errorf("Error executing tool '%s': Account sequence mismatch (expected %s, got %s). Suggestion: Wait and retry. If persistent, query current sequence.", toolName, expected, got)
		}
		return fmt.Errorf("Error executing tool '%s': Account sequence mismatch. Suggestion: Wait and retry.", toolName)
	}
	if strings.Contains(lowerStderr, "invalid coins") || strings.Contains(lowerStderr, "invalid amount") {
		amount := ""
		re := regexp.MustCompile(`invalid\s+coins:\s+([^\s]+)`)
		matches := re.FindStringSubmatch(lowerStderr)
		if len(matches) > 1 {
			amount = matches[1]
		}
		if amount != "" {
			return fmt.Errorf("Error executing tool '%s': Invalid amount/coins ('%s'). Suggestion: Ensure amounts are positive integers with correct denomination (e.g., '100token').", toolName, amount)
		}
		return fmt.Errorf("Error executing tool '%s': Invalid amount/coins. Suggestion: Ensure amounts are positive integers with correct denomination.", toolName)
	}
	// Module specific errors
	if strings.Contains(lowerStderr, "auction not found") || strings.Contains(lowerStderr, "auction does not exist") {
		id := ""
		re := regexp.MustCompile(`auction\s+([^\s]+)\s+not\s+found`)
		matches := re.FindStringSubmatch(lowerStderr)
		if len(matches) > 1 {
			id = matches[1]
		}
		if id != "" {
			return fmt.Errorf("Error executing tool '%s': Auction ID '%s' not found. Suggestion: Verify ID. Use 'feedbackQA' to list auctions.", toolName, id)
		}
		return fmt.Errorf("Error executing tool '%s': Auction ID not found. Suggestion: Verify ID. Use 'feedbackQA' to list auctions.", toolName)
	}
	if strings.Contains(lowerStderr, "bid does not exist") {
		return fmt.Errorf("Error executing tool '%s': Bid not found. Suggestion: Verify bid/auction details.", toolName)
	}
	if strings.Contains(lowerStderr, "auction is not open") || strings.Contains(lowerStderr, "auction is closed") {
		state := "in wrong state"
		if strings.Contains(lowerStderr, "not open") {
			state = "not open"
		}
		if strings.Contains(lowerStderr, "closed") {
			state = "closed"
		}
		return fmt.Errorf("Error executing tool '%s': Auction is '%s'. Suggestion: Check status with 'feedbackQA' before this operation.", toolName, state)
	}
	if strings.Contains(lowerStderr, "unauthorized") {
		return fmt.Errorf("Error executing tool '%s': Unauthorized. Suggestion: Ensure 'from' address has permissions (e.g., auction creator to close).", toolName)
	}

	// Best Practice: Fallback error for unparsed issues, including some context.
	errMsg := fmt.Sprintf("Error executing tool '%s': Unexpected command failure.", toolName)
	if stderr != "" {
		conciseStderr := strings.TrimSpace(stderr)
		firstLine := strings.SplitN(conciseStderr, "\n", 2)[0]
		if len(firstLine) > 150 {
			firstLine = firstLine[:150] + "..."
		} // Truncate for brevity
		errMsg += fmt.Sprintf(" Details: %s", firstLine)
	}
	errMsg += " Original error: %w" // Wrap original error
	return fmt.Errorf(errMsg, originalErr)
}

// --- Command Execution ---

// runCommand executes a command with timeout, capturing stdout, stderr, and errors.
// Best Practice: Proper timeout handling, capturing output streams, detailed logging, error wrapping.
func runCommand(ctx context.Context, cfg config, name string, arg ...string) (stdout string, stderr string, err error) {
	// Best Practice: Use context for cancellation and timeouts.
	cmdCtx, cancel := context.WithTimeout(ctx, cfg.commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, name, arg...)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	fullCmdStr := fmt.Sprintf("%s %s", name, strings.Join(arg, " "))
	// Best Practice: Debug logging for command execution.
	log.Printf("DEBUG: Executing command: %s", fullCmdStr)

	startTime := time.Now()
	runErr := cmd.Run() // Capture the error from Run()
	duration := time.Since(startTime)

	stdout = stdoutBuf.String()
	stderr = stderrBuf.String() // Capture stderr regardless of error

	if runErr != nil {
		// Best Practice: Check for specific context errors like deadline exceeded.
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			log.Printf("ERROR: Command timed out after %v: %s", duration, fullCmdStr)
			// Return specific, wrapped error for timeout
			err = fmt.Errorf("command '%s' timed out after %v: %w", fullCmdStr, duration, context.DeadlineExceeded)
		} else {
			// Best Practice: Log detailed error info, including wrapped original error.
			log.Printf("ERROR: Command failed after %v: %v. Cmd: %s", duration, runErr, fullCmdStr)
			// Wrap the original error from cmd.Run()
			err = fmt.Errorf("command '%s' failed: %w", fullCmdStr, runErr)
		}
		// Log output streams even on error for debugging
		log.Printf("DEBUG: STDOUT on error:\n%s", stdout)
		log.Printf("DEBUG: STDERR on error:\n%s", stderr)
		return stdout, stderr, err // Return streams and the processed error
	}

	// Best Practice: Log successful execution details.
	log.Printf("INFO: Command executed successfully in %v: %s", duration, fullCmdStr)
	log.Printf("DEBUG: STDOUT on success:\n%s", stdout)
	if stderr != "" {
		// Log stderr even on success if it's not empty (some tools output info/warnings to stderr)
		log.Printf("DEBUG: STDERR (non-empty on success):\n%s", stderr)
	}
	return stdout, stderr, nil // Return streams and nil error
}
