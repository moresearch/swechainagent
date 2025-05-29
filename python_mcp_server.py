import fastapi
import uvicorn
import subprocess
import json
import logging
import asyncio
import os
import re # Imported re module
from fastapi.responses import StreamingResponse
from fastapi import Body, Path, HTTPException
from pydantic import BaseModel, Field

# Environment Variable Configuration Comments
# MCP_SERVER_HOST: Server host address (default: 0.0.0.0)
# MCP_SERVER_PORT: Server port (default: 8000)
# SWECHAIND_PATH: Full path to `swechaind` executable (default: 'swechaind', assumes it's in PATH)
# FEEDBACK_BIN_PATH: Full path to `feedback` executable (default: './bin/feedback')
# FEEDBACK_QA_BIN_PATH: Full path to `feedbackQA` executable (default: './bin/feedbackQA')
# KEYRING_BACKEND: Keyring backend to use (default: 'test')
# CHAIN_ID: Chain ID for transactions (default: 'swechain')
# DEFAULT_FEES: Default fees for transactions (default: '200token')
# COMMAND_TIMEOUT_SECONDS: Timeout for subprocess commands (default: 120)

# Basic logging configuration
# Ensure os and logging are imported (already done at the top of the file)
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')

# Application Configuration Dictionary
CONFIG = {}
CONFIG['SWECHAIND_PATH'] = os.environ.get('SWECHAIND_PATH', 'swechaind')
CONFIG['FEEDBACK_BIN_PATH'] = os.environ.get('FEEDBACK_BIN_PATH', './bin/feedback')
CONFIG['FEEDBACK_QA_BIN_PATH'] = os.environ.get('FEEDBACK_QA_BIN_PATH', './bin/feedbackQA')
CONFIG['KEYRING_BACKEND'] = os.environ.get('KEYRING_BACKEND', 'test')
CONFIG['CHAIN_ID'] = os.environ.get('CHAIN_ID', 'swechain')
CONFIG['DEFAULT_FEES'] = os.environ.get('DEFAULT_FEES', '200token')

COMMAND_TIMEOUT_STR = os.environ.get('COMMAND_TIMEOUT_SECONDS', '120')
try:
    CONFIG['COMMAND_TIMEOUT_SECONDS'] = int(COMMAND_TIMEOUT_STR)
except ValueError:
    logging.warning(f"Invalid COMMAND_TIMEOUT_SECONDS value '{COMMAND_TIMEOUT_STR}', using default 120 seconds.")
    CONFIG['COMMAND_TIMEOUT_SECONDS'] = 120

# Utility function to parse and enhance swechaind errors
def parse_swechaind_error(stderr_output: str, tool_name: str, default_fees: str, original_exception: Exception = None) -> str:
    """
    Parses stderr output from swechaind commands to provide more user-friendly error messages
    and actionable suggestions for an LLM.
    """
    lower_stderr = stderr_output.lower() if stderr_output else ""

    if isinstance(original_exception, subprocess.TimeoutExpired):
        return f"Error executing tool '{tool_name}': The command timed out after {CONFIG.get('COMMAND_TIMEOUT_SECONDS', 120)} seconds. Suggestion: Try again later, check network status, or consider if the operation is too complex for the current timeout."

    if "key not found" in lower_stderr:
        match_name_addr = re.search(r"key not found with name ([^\s]+) or address ([^\s]+)", lower_stderr)
        if match_name_addr:
            identifier = f"name {match_name_addr.group(1)} or address {match_name_addr.group(2)}"
            return f"Error executing tool '{tool_name}': Key not found for identifier '{identifier}'. Suggestion: Ensure the address/username exists in the keyring and is spelled correctly. Use 'create-and-fund-address' if needed."
        match_name = re.search(r"key not found with name ([^\s]+)", lower_stderr)
        if match_name:
            identifier = f"name {match_name.group(1)}"
            return f"Error executing tool '{tool_name}': Key not found for identifier '{identifier}'. Suggestion: Ensure the username exists in the keyring and is spelled correctly. Use 'create-and-fund-address' if needed."
        match_addr = re.search(r"key not found for address ([^\s]+)", lower_stderr) # Example pattern
        if match_addr:
            identifier = f"address {match_addr.group(1)}"
            return f"Error executing tool '{tool_name}': Key not found for identifier '{identifier}'. Suggestion: Ensure the address exists in the keyring and is spelled correctly. Use 'create-and-fund-address' if needed."
        return f"Error executing tool '{tool_name}': A required key/address was not found. Suggestion: Ensure relevant addresses (e.g., 'from', 'bidder', 'funder_address') exist in the keyring. Use 'create-and-fund-address' if needed."

    # Contextual check for "already exists", especially for key creation
    if "already exists" in lower_stderr:
        if "create-and-fund-address" in tool_name or "key" in tool_name: # More specific context
            match_key_name = re.search(r"key with name ([^\s]+) already exists", lower_stderr)
            if match_key_name:
                username = match_key_name.group(1)
                # This is often not a true "error" for idempotent operations.
                return f"Informational for tool '{tool_name}': Key with name '{username}' already exists. Suggestion: The tool may proceed using this existing key. No action needed if this is the intended user."
            return f"Informational for tool '{tool_name}': The resource (e.g., key) already exists. Suggestion: The tool may proceed by using the existing resource."
        # Generic "already exists" for other contexts if needed in future
        # return f"Error executing tool '{tool_name}': The resource already exists."


    if "insufficient funds" in lower_stderr:
        match_addr_funds = re.search(r"account\s+([a-zA-Z0-9_.-]+)\s+has\s+insufficient\s+funds", lower_stderr)
        if match_addr_funds:
            address = match_addr_funds.group(1)
            return f"Error executing tool '{tool_name}': Address '{address}' has insufficient funds (needs fees + amount). Suggestion: Check balance or get funds. Default fee: '{default_fees}'."
        return f"Error executing tool '{tool_name}': Insufficient funds for the 'from' address. Suggestion: Check balance or get funds. Default fee: '{default_fees}'."

    if "account sequence mismatch" in lower_stderr:
        match_seq = re.search(r"expected\s+(\d+),\s+got\s+(\d+)", lower_stderr)
        if match_seq:
            expected, got = match_seq.group(1), match_seq.group(2)
            return f"Error executing tool '{tool_name}': Account sequence mismatch (expected {expected}, got {got}). Suggestion: Wait a moment and retry. If persistent, query the current account sequence and use that in the 'sequence_number' parameter if available for the tool."
        return f"Error executing tool '{tool_name}': Account sequence mismatch. Suggestion: Wait a moment and retry. If persistent, query the current account sequence."

    if "invalid coins" in lower_stderr or "invalid amount" in lower_stderr:
        match_coins = re.search(r"invalid coins:\s*([^\s\n]+)", lower_stderr)
        amount_str = match_coins.group(1) if match_coins else "provided"
        return f"Error executing tool '{tool_name}': Invalid amount/coins ('{amount_str}'). Suggestion: Ensure amounts are positive integers with correct denomination (e.g., '100token', not '100 token' or '100'). Denomination should be one word."
    
    if "auction not found" in lower_stderr or "auction does not exist" in lower_stderr:
        match_id = re.search(r"auction\s+([^\s]+)\s+(not found|does not exist)", lower_stderr)
        auction_id_str = f"'{match_id.group(1)}' " if match_id else ""
        return f"Error executing tool '{tool_name}': Auction ID {auction_id_str}not found. Suggestion: Verify the auction ID. Use 'feedbackQA --mode list-auctions' to list available auctions."

    if "auction is not open" in lower_stderr or "auction is closed" in lower_stderr:
        state = "not open" if "not open" in lower_stderr else "closed"
        return f"Error executing tool '{tool_name}': Auction is currently '{state}'. Suggestion: Check auction status with 'feedbackQA --mode list-auctions --auction-id <id>' before this operation."

    if "unauthorized" in lower_stderr:
        return f"Error executing tool '{tool_name}': Unauthorized. Suggestion: Ensure the 'from' address (signer) has the necessary permissions for this operation (e.g., auction creator to close auction, correct bidder to reveal bid)."

    if "bid does not exist" in lower_stderr: # Added from Go's parseAndEnhanceError
        match_bidder = re.search(r"bidder\s+([^\s]+)", lower_stderr)
        bidder_str = f"for bidder '{match_bidder.group(1)}' " if match_bidder else ""
        return f"Error executing tool '{tool_name}': Bid {bidder_str}does not exist for the specified auction. Suggestion: Verify bidder address and auction ID. Ensure a bid was placed."

    # Default fallback message
    first_line_stderr = stderr_output.splitlines()[0] if stderr_output else "No stderr output provided to parser."
    if len(first_line_stderr) > 150:  # Truncate for brevity
        first_line_stderr = first_line_stderr[:150] + "..."
    
    base_error_message = str(original_exception) if original_exception else "Unknown error during command execution."

    return f"Error executing tool '{tool_name}': Unexpected command failure. Details: {first_line_stderr}. Original error hint: {base_error_message}"


# Global SSE Headers
SSE_HEADERS = {
    "Cache-Control": "no-cache",
    "X-Accel-Buffering": "no",
    "Connection": "keep-alive",
}

# Pydantic Models for Tool Parameters
class MemoryParams(BaseModel):
    """Parameters for invoking the memory tool via SSE."""
    query: str = Field(..., description="The natural language query for the memory or feedback system.")
    agent_name: str = Field(..., description="Identifier for the agent whose memory is being queried.")

class BalanceParams(BaseModel):
    """Parameters for invoking the balance tool via SSE."""
    account: str = Field(..., description="The blockchain account address to query for balances.")

class PayParams(BaseModel): # Renamed from SendParams
    """Parameters for invoking the pay tool via SSE to transfer tokens."""
    from_address: str = Field(..., alias='from', description="The sender's blockchain address.")
    to: str = Field(..., description="The recipient's blockchain address.")
    amount: str = Field(..., description="The amount of tokens to send (e.g., '1000000', the denomination 'token' is appended by the tool).")

class AddrParams(BaseModel):
    """Parameters for invoking the address tool via SSE to retrieve an account's address."""
    account_name: str = Field(..., description="The name of the account for which to retrieve the address.")

class FeedbackQAParams(BaseModel):
    """Parameters for invoking the feedbackQA tool."""
    question: str = Field(..., description="A specific question about the blockchain environment state (e.g., 'list open auctions', 'get balance cosmos1...')")

class CreateAndFundParams(BaseModel):
    """Parameters for creating/ensuring a key and funding its address."""
    username: str = Field(..., description="A unique name/label for the new key to be created for the agent (e.g., 'my-agent-01').")
    amount: str = Field(..., description="Amount of tokens to send to the new address (e.g., '10000'). Denomination 'token' will be added automatically.")
    funder_address: str = Field(..., description="The existing blockchain address that will send the funds (must exist in the keyring and have sufficient balance).")

class OpenAuctionParams(BaseModel):
    """Parameters for creating a new auction on the blockchain."""
    issue: str = Field(..., description="Issue identifier (e.g., 'owner/repo/issues/123').")
    description: str = Field(..., description="Detailed description of the task/auction.")
    status: str = Field(..., description="Initial status, should typically be 'open'.")
    winner: str = Field(default="TBD", description="Leave empty or set to 'TBD' for a new auction.")
    from_address: str = Field(..., alias='from', description="Your AGENT_ADDRESS obtained previously.")

class CreateBidParams(BaseModel):
    """Parameters for placing a bid on an existing auction."""
    auctionId: str = Field(..., description="Identifier of the auction to bid on.")
    bidder: str = Field(..., description="Your AGENT_ADDRESS obtained previously.")
    amount: str = Field(..., description="Bid amount (e.g., '500'). Denomination 'token' will be added automatically.")
    description: str = Field(..., description="Optional description for your bid.")
    from_address: str = Field(..., alias='from', description="Your AGENT_ADDRESS obtained previously (should be same as bidder).")

class CloseAuctionParams(BaseModel):
    """Parameters for closing an auction and declaring the winner."""
    auctionId: str = Field(..., description="Identifier of the auction you are closing.")
    issue: str = Field(..., description="The original issue identifier of the auction.")
    description: str = Field(..., description="The original description of the auction.")
    status: str = Field(..., description="Set status to 'closed'.") # Consider validator if strictness needed: validator('status')(ensure_closed_status)
    winner: str = Field(..., description="The blockchain address of the winning bidder.")
    from_address: str = Field(..., alias='from', description="Your AGENT_ADDRESS (must be auction creator).")

# MCP Tool Definitions
MCP_TOOLS_METADATA = [
    {
        "name": "memory",
        "description": "Memory enables adaptive decision-making, allowing you to adjust its strategies based on accumulated knowledge",
        "parameters": [
            {
                "name": "query",
                "type": "string",
                "required": True,
                "description": "query the memory for real-time information",
            },
            {
                "name": "agent_name",
                "type": "string",
                "required": True,
                "description": "agent_name is your given name, and it is used to RAG over your memories",
            },
        ],
    },
    {
        "name": "balance",
        "description": "gets a balance for an account",
        "parameters": [
            {
                "name": "account",
                "type": "string",
                "required": True,
                "description": "account to query the balance",
            }
        ],
    },
    {
        "name": "pay", # Renamed from send
        "description": "Sends a specified amount of tokens from one account to another.",
        "parameters": [
            {
                "name": "from", # Corresponds to from_address in PayParams due to alias
                "type": "string",
                "required": True,
                "description": "The sender's blockchain account address (key name or raw address).",
            },
            {
                "name": "to",
                "type": "string",
                "required": True,
                "description": "The recipient's blockchain account address.",
            },
            {
                "name": "amount",
                "type": "string",
                "required": True,
                "description": "The numerical amount of tokens to send (e.g., '100', '200000'). The denomination 'token' will be appended by the system.",
            },
        ],
    },
    {
        "name": "addr",
        "description": "give an account name to get the account address",
        "parameters": [
            {
                "name": "account_name",
                "type": "string",
                "required": True,
                "description": "the name of the person you want to get its address",
            }
        ],
    },
    {
        "name": "feedbackQA",
        "description": "(Read-Only Hint) Ask specific questions about the swechain blockchain environment state (e.g., open auctions, specific auction details, querying balances) to inform your next action. Uses an external script/tool.",
        "parameters": [
            {"name": "question", "type": "string", "required": True, "description": "A specific question about the blockchain environment state (e.g., 'list open auctions', 'get balance cosmos1...')"}
        ]
    },
    {
        "name": "create-and-fund-address",
        "description": "Creates a new key for an agent if it doesn't exist, retrieves its address, and funds it from a specified funder address. Idempotent for key creation.",
        "parameters": [
            {
                "name": "username",
                "type": "string",
                "required": True,
                "description": "A unique name/label for the new key to be created for the agent (e.g., 'my-agent-01')."
            },
            {
                "name": "amount",
                "type": "string",
                "required": True,
                "description": "Amount of tokens to send to the new address (e.g., '10000'). Denomination 'token' will be added automatically."
            },
            {
                "name": "funder_address",
                "type": "string",
                "required": True,
                "description": "The existing blockchain address that will send the funds (must exist in the keyring and have sufficient balance)."
            }
        ]
    },
    {
        "name": "open-auction",
        "description": "(Open World Hint) Creates a new auction *on the blockchain* for a specific issue. IMPORTANT: Use the AGENT_ADDRESS you previously obtained from 'create-and-fund-address' for the 'from' parameter.",
        "parameters": [
            {"name": "issue", "type": "string", "required": True, "description": "Issue identifier (e.g., 'owner/repo/issues/123')."},
            {"name": "description", "type": "string", "required": True, "description": "Detailed description of the task/auction."},
            {"name": "status", "type": "string", "required": True, "description": "Initial status, should typically be 'open'."},
            {"name": "winner", "type": "string", "required": False, "description": "Leave empty or set to 'TBD' for a new auction."},
            {"name": "from", "type": "string", "required": True, "description": "Your AGENT_ADDRESS obtained previously."}
        ]
    },
    {
        "name": "create-bid",
        "description": "(Open World Hint) Places a bid *on the blockchain* on an existing open auction. IMPORTANT: Use the AGENT_ADDRESS you previously obtained from 'create-and-fund-address' for the 'bidder' and 'from' parameters.",
        "parameters": [
            {"name": "auctionId", "type": "string", "required": True, "description": "Identifier of the auction to bid on."},
            {"name": "bidder", "type": "string", "required": True, "description": "Your AGENT_ADDRESS obtained previously."},
            {"name": "amount", "type": "string", "required": True, "description": "Bid amount (e.g., '500'). Denomination 'token' will be added automatically."},
            {"name": "description", "type": "string", "required": True, "description": "Optional description for your bid."},
            {"name": "from", "type": "string", "required": True, "description": "Your AGENT_ADDRESS obtained previously (should be same as bidder)."}
        ]
    },
    {
        "name": "close-auction",
        "description": "(Open World Hint) Updates an auction status to 'closed' *on the blockchain*, specifying the winner. IMPORTANT: Use the AGENT_ADDRESS you previously obtained for the 'from' parameter (must be the auction creator). The 'winner' address MUST be the actual winner's address. Payment is separate via 'pay'.",
        "parameters": [
            {"name": "auctionId", "type": "string", "required": True, "description": "Identifier of the auction you are closing."},
            {"name": "issue", "type": "string", "required": True, "description": "The original issue identifier of the auction."},
            {"name": "description", "type": "string", "required": True, "description": "The original description of the auction."},
            {"name": "status", "type": "string", "required": True, "description": "Set status to 'closed'."},
            {"name": "winner", "type": "string", "required": True, "description": "The blockchain address of the winning bidder."},
            {"name": "from", "type": "string", "required": True, "description": "Your AGENT_ADDRESS (must be auction creator)."}
        ]
    },
]

# Core Tool Execution Functions

def execute_memory_tool(params: dict, config: dict) -> dict: # Added config
    try:
        agent_name = params['agent_name']
        query = params['query']
        command = [config['FEEDBACK_BIN_PATH'], '--env', agent_name.lower(), '--mode', 'infer', '--query', query]
        
        logging.info(f"Executing command: {' '.join(command)}")
        # Intentionally not using parse_swechaind_error for feedback tool
        result = subprocess.run(command, capture_output=True, text=True, check=False, timeout=config['COMMAND_TIMEOUT_SECONDS'])

        if result.returncode == 0:
            logging.info(f"Memory tool executed successfully. Output: {result.stdout.strip()}")
            return {"status": "success", "data_type": "text", "data": result.stdout.strip()}
        else: # Non-zero return code from feedback tool
            logging.error(f"Memory tool execution failed. Return code: {result.returncode}, Stderr: {result.stderr.strip()}")
            # Simple error reporting for feedback tool, not using parse_swechaind_error
            return {"status": "error", "message": f"Memory tool execution failed. Details: {result.stderr.strip() if result.stderr else 'No error output'}"}
    except FileNotFoundError:
        logging.error(f"Command '{config['FEEDBACK_BIN_PATH']}' not found.")
        return {"status": "error", "message": f"Command '{config['FEEDBACK_BIN_PATH']}' not found."}
    except subprocess.TimeoutExpired:
        logging.error(f"Memory tool command timed out after {config['COMMAND_TIMEOUT_SECONDS']} seconds.")
        return {"status": "error", "message": f"Error executing tool 'memory': The command timed out after {config['COMMAND_TIMEOUT_SECONDS']} seconds."}
    except KeyError as e: # Should be caught by Pydantic, but as a safeguard
        logging.error(f"Missing parameter for memory tool: {e}")
        return {"status": "error", "message": f"Missing required parameter for memory tool: {str(e)}"}
    except Exception as e:
        logging.error(f"An unexpected error occurred in execute_memory_tool: {e}", exc_info=True)
        return {"status": "error", "message": f"An unexpected error occurred during memory tool execution: {str(e)}"}

def execute_balance_tool(params: dict, config: dict) -> dict: # Added config
    stderr_output = ""
    original_exception = None
    try:
        account = params['account']
        command = [config['SWECHAIND_PATH'], 'query', 'bank', 'balances', account, '--output', 'json']

        logging.info(f"Executing command: {' '.join(command)}")
        result = subprocess.run(command, capture_output=True, text=True, check=False, timeout=config['COMMAND_TIMEOUT_SECONDS'])
        stderr_output = result.stderr if result else "" # Capture stderr even if result is None (though unlikely here)

        if result and result.returncode == 0:
            try:
                parsed_json = json.loads(result.stdout)
                logging.info("Balance tool executed successfully (JSON).")
                return {"status": "success", "data_type": "json", "data": parsed_json}
            except json.JSONDecodeError as je:
                logging.warning(f"Balance tool output was not valid JSON: {je}. Output: {result.stdout.strip()}")
                return {"status": "success", "data_type": "text", "data": result.stdout.strip(), "warning": "Command output was not valid JSON."}
        else: # Non-zero return code or result is None
            enhanced_error_message = parse_swechaind_error(stderr_output, "balance", config['DEFAULT_FEES'])
            logging.error(f"Balance tool execution failed. Parsed error: {enhanced_error_message}. Stderr: {stderr_output.strip() if stderr_output else 'N/A'}")
            return {"status": "error", "message": enhanced_error_message}
    except FileNotFoundError:
        logging.error(f"Command '{config['SWECHAIND_PATH']}' not found.")
        return {"status": "error", "message": f"Command '{config['SWECHAIND_PATH']}' not found."}
    except subprocess.TimeoutExpired as te:
        original_exception = te
        logging.error(f"Balance tool command timed out after {config['COMMAND_TIMEOUT_SECONDS']} seconds.")
        # Pass current stderr_output, might be empty if timeout happened before any output
        enhanced_error_message = parse_swechaind_error(stderr_output, "balance", config['DEFAULT_FEES'], original_exception)
        return {"status": "error", "message": enhanced_error_message}
    except KeyError as e: # Should be caught by Pydantic
        logging.error(f"Missing parameter for balance tool: {e}")
        return {"status": "error", "message": f"Missing required parameter for balance tool: {str(e)}"}
    except Exception as e:
        original_exception = e
        logging.error(f"An unexpected error occurred in execute_balance_tool: {e}", exc_info=True)
        # Pass current stderr_output
        enhanced_error_message = parse_swechaind_error(stderr_output, "balance", config['DEFAULT_FEES'], original_exception)
        return {"status": "error", "message": enhanced_error_message}

def execute_pay_tool(params: dict, config: dict) -> dict:
    stderr_output = ""
    original_exception = None
    try:
        from_addr = params['from_address'] # Changed from params['from'] to match PayParams
        to_addr = params['to']
        amount_val = params['amount']
        
        command = [
            config['SWECHAIND_PATH'], 'tx', 'bank', 'send', 
            from_addr.lower(), to_addr.lower(), amount_val + 'token', # Append 'token' denomination
            '--from', from_addr.lower(), 
            '--keyring-backend', config['KEYRING_BACKEND'],
            '--chain-id', config['CHAIN_ID'],
            '--fees', config['DEFAULT_FEES'],
            '--yes', '--output', 'json'
        ]

        logging.info(f"Executing command: {' '.join(command)}")
        result = subprocess.run(command, capture_output=True, text=True, check=False, timeout=config['COMMAND_TIMEOUT_SECONDS'])
        stderr_output = result.stderr

        if result.returncode == 0:
            try:
                parsed_json = json.loads(result.stdout)
                logging.info("Pay tool executed successfully (JSON).")
                return {"status": "success", "data_type": "json", "data": parsed_json}
            except json.JSONDecodeError as je:
                stdout_stripped = result.stdout.strip()
                if stdout_stripped: 
                    logging.warning(f"Pay tool output was not valid JSON: {je}. Output: {stdout_stripped}")
                    return {"status": "success", "data_type": "text", "data": stdout_stripped, "warning": "Command output was not valid JSON."}
                else: 
                    logging.info("Pay tool executed, but produced no JSON output (stdout was empty). This might be normal.")
                    return {"status": "success", "data_type": "text", "data": "", "warning": "Command executed but produced no JSON output."}
        else: # Non-zero return code
            enhanced_error_message = parse_swechaind_error(stderr_output, "pay", config['DEFAULT_FEES'])
            logging.error(f"Pay tool execution failed. Parsed error: {enhanced_error_message}. Stderr: {stderr_output.strip()}")
            return {"status": "error", "message": enhanced_error_message}
    except FileNotFoundError:
        logging.error(f"Command '{config['SWECHAIND_PATH']}' not found.")
        return {"status": "error", "message": f"Command '{config['SWECHAIND_PATH']}' not found."}
    except subprocess.TimeoutExpired as te:
        original_exception = te
        logging.error(f"Pay tool command timed out after {config['COMMAND_TIMEOUT_SECONDS']} seconds.")
        enhanced_error_message = parse_swechaind_error(stderr_output, "pay", config['DEFAULT_FEES'], original_exception)
        return {"status": "error", "message": enhanced_error_message}
    except KeyError as e: 
        logging.error(f"Missing parameter for pay tool: {e}")
        return {"status": "error", "message": f"Missing required parameter for pay tool: {e}"}
    except Exception as e:
        original_exception = e
        logging.error(f"An unexpected error occurred in execute_pay_tool: {e}", exc_info=True)
        enhanced_error_message = parse_swechaind_error(stderr_output, "pay", config['DEFAULT_FEES'], original_exception)
        return {"status": "error", "message": enhanced_error_message}

def execute_addr_tool(params: dict, config: dict) -> dict: # Added config
    try:
        account_name = params['account_name']
        command = [SWECHAIND_CMD, 'keys', 'show', account_name.lower(), '-a']

        logging.info(f"Executing command: {' '.join(command)}")
        result = subprocess.run(command, capture_output=True, text=True, check=False)

        if result.returncode == 0:
            logging.info(f"Address tool executed successfully. Output: {result.stdout.strip()}")
            return {"status": "success", "data_type": "text", "data": result.stdout.strip()}
        else:
            logging.error(f"Address tool execution failed. Return code: {result.returncode}, Stderr: {result.stderr.strip()}")
            return {"status": "error", "message": "Address tool execution failed", "details": result.stderr.strip(), "returncode": result.returncode}
    except FileNotFoundError:
        logging.error(f"Command '{SWECHAIND_CMD}' not found.")
        return {"status": "error", "message": f"Command '{SWECHAIND_CMD}' not found."}
    except KeyError as e:
        logging.error(f"Missing parameter for addr tool: {e}")
        return {"status": "error", "message": f"Missing required parameter: {e}"}
    except Exception as e:
        logging.error(f"An unexpected error occurred in execute_addr_tool: {e}")
        return {"status": "error", "message": "An unexpected error occurred during address tool execution", "details": str(e)}

def execute_feedbackqa_tool(params: dict, config: dict) -> dict:
    try:
        question = params['question']
        # The question string is passed as a single argument to the script.
        # Shell safety is less of a concern here if the script is trusted and handles its own args,
        # but if the question could contain shell metacharacters and was interpolated directly into a shell string,
        # it would be an issue. Here, it's an argument in a list, which is safer.
        command = [config['FEEDBACK_QA_BIN_PATH'], question] 
        
        logging.info(f"Executing command: {' '.join(command)}")
        result = subprocess.run(
            command, 
            capture_output=True, 
            text=True, 
            check=False, 
            timeout=config['COMMAND_TIMEOUT_SECONDS']
        )

        if result.returncode == 0:
            logging.info(f"FeedbackQA tool executed successfully. Output: {result.stdout.strip()}")
            return {"status": "success", "data_type": "text", "data": result.stdout.strip()}
        else:
            # Not using parse_swechaind_error as this is a different binary
            logging.error(f"FeedbackQA tool execution failed. Return code: {result.returncode}, Stderr: {result.stderr.strip()}")
            return {"status": "error", "message": f"FeedbackQA tool execution failed. Details: {result.stderr.strip() if result.stderr else 'No error output'}", "returncode": result.returncode}
    except FileNotFoundError:
        logging.error(f"Command '{config['FEEDBACK_QA_BIN_PATH']}' not found.")
        return {"status": "error", "message": f"Command '{config['FEEDBACK_QA_BIN_PATH']}' not found."}
    except subprocess.TimeoutExpired:
        logging.error(f"FeedbackQA tool command timed out after {config['COMMAND_TIMEOUT_SECONDS']} seconds.")
        return {"status": "error", "message": f"Error executing tool 'feedbackQA': The command timed out after {config['COMMAND_TIMEOUT_SECONDS']} seconds."}
    except KeyError as e: # Should be caught by Pydantic
        logging.error(f"Missing parameter for feedbackQA tool: {e}")
        return {"status": "error", "message": f"Missing required parameter for feedbackQA tool: {str(e)}"}
    except Exception as e:
        logging.error(f"An unexpected error occurred in execute_feedbackqa_tool: {e}", exc_info=True)
        return {"status": "error", "message": f"An unexpected error occurred during feedbackQA tool execution: {str(e)}"}

def execute_create_and_fund_tool(params: dict, config: dict) -> dict:
    try:
        username = params['username']
        amount = params['amount']
        funder_address = params['funder_address']
    except KeyError as e: # Should be caught by Pydantic, but good to have a safeguard
        logging.error(f"Missing parameter for create-and-fund-address tool: {e}")
        return {"status": "error", "message": f"Missing required parameter: {str(e)}"}

    key_cmd_output = ""
    key_already_exists = False
    add_exception = None
    add_result = None
    
    # Step 1: Ensure Key Exists (Idempotency Logic)
    try:
        keys_add_cmd = [
            config['SWECHAIND_PATH'], 'keys', 'add', username,
            '--keyring-backend', config['KEYRING_BACKEND'],
            '--output', 'json'
        ]
        logging.info(f"Executing key add command: {' '.join(keys_add_cmd)}")
        add_result = subprocess.run(
            keys_add_cmd, capture_output=True, text=True, check=False, 
            timeout=config['COMMAND_TIMEOUT_SECONDS']
        )
    except FileNotFoundError:
        logging.error(f"Command '{config['SWECHAIND_PATH']}' not found for key add.")
        return {"status": "error", "message": f"Command '{config['SWECHAIND_PATH']}' not found."}
    except subprocess.TimeoutExpired as e:
        add_exception = e
        logging.error(f"Key add command timed out for '{username}': {e}")
    except Exception as e: # Other unexpected errors during subprocess run
        add_exception = e
        logging.error(f"Unexpected error during key add command for '{username}': {e}", exc_info=True)

    add_stderr = add_result.stderr if add_result else ""
    if (add_result and add_result.returncode != 0) or add_exception:
        enhanced_key_add_err_msg = parse_swechaind_error(add_stderr, "create-and-fund-address-key-add", config['DEFAULT_FEES'], add_exception)
        
        if "already exists" in enhanced_key_add_err_msg.lower() or "already exists" in add_stderr.lower():
            key_already_exists = True
            logging.info(f"Key '{username}' already exists. Retrieving address.")
            show_exception = None
            show_result = None
            try:
                keys_show_cmd = [
                    config['SWECHAIND_PATH'], 'keys', 'show', username,
                    '--keyring-backend', config['KEYRING_BACKEND'],
                    '--output', 'json'
                ]
                logging.info(f"Executing key show command: {' '.join(keys_show_cmd)}")
                show_result = subprocess.run(
                    keys_show_cmd, capture_output=True, text=True, check=False,
                    timeout=config['COMMAND_TIMEOUT_SECONDS']
                )
            except FileNotFoundError: # Should not happen if key add didn't, but for safety
                logging.error(f"Command '{config['SWECHAIND_PATH']}' not found for key show.")
                return {"status": "error", "message": f"Command '{config['SWECHAIND_PATH']}' not found (during key show)."}
            except subprocess.TimeoutExpired as e:
                show_exception = e
                logging.error(f"Key show command timed out for '{username}': {e}")
            except Exception as e:
                show_exception = e
                logging.error(f"Unexpected error during key show command for '{username}': {e}", exc_info=True)

            show_stderr = show_result.stderr if show_result else ""
            if (show_result and show_result.returncode != 0) or show_exception:
                err_msg = parse_swechaind_error(show_stderr, "create-and-fund-address-key-show", config['DEFAULT_FEES'], show_exception)
                return {"status": "error", "message": f"Failed to retrieve existing key info for '{username}': {err_msg}"}
            key_cmd_output = show_result.stdout if show_result else ""
        else: # Other error during 'keys add'
            return {"status": "error", "message": f"Key creation/check failed: {enhanced_key_add_err_msg}"}
    else: # keys add was successful
        logging.info(f"Key '{username}' created successfully.")
        key_cmd_output = add_result.stdout if add_result else ""

    # Step 2: Parse Address
    new_address = None
    try:
        parsed_key_info = json.loads(key_cmd_output)
        new_address = parsed_key_info.get('address')
        if not new_address: # Check if address is empty or None
            raise ValueError("Address not found in key command output.")
    except (json.JSONDecodeError, ValueError) as e:
        logging.error(f"Failed to parse address from key command output for '{username}'. Error: {e}. JSON attempted: {key_cmd_output}")
        return {"status": "error", "message": f"Failed to parse address from key command output for '{username}'. Check server logs for details."}

    # Step 3: Fund Address
    amount_with_denom = amount + "token"
    fund_exception = None
    fund_result = None
    try:
        fund_cmd = [
            config['SWECHAIND_PATH'], 'tx', 'bank', 'send',
            funder_address, new_address, amount_with_denom,
            '--from', funder_address,
            '--keyring-backend', config['KEYRING_BACKEND'],
            '--chain-id', config['CHAIN_ID'],
            '--fees', config['DEFAULT_FEES'],
            '--yes', '--output', 'json'
        ]
        logging.info(f"Executing funding command: {' '.join(fund_cmd)}")
        fund_result = subprocess.run(
            fund_cmd, capture_output=True, text=True, check=False,
            timeout=config['COMMAND_TIMEOUT_SECONDS']
        )
    except FileNotFoundError: # Should not happen if previous steps didn't, but for safety
        logging.error(f"Command '{config['SWECHAIND_PATH']}' not found for funding.")
        return {"status": "error", "message": f"Command '{config['SWECHAIND_PATH']}' not found (during funding)."}
    except subprocess.TimeoutExpired as e:
        fund_exception = e
        logging.error(f"Funding command timed out for new address '{new_address}': {e}")
    except Exception as e:
        fund_exception = e
        logging.error(f"Unexpected error during funding command for new address '{new_address}': {e}", exc_info=True)
        
    fund_stderr = fund_result.stderr if fund_result else ""
    if (fund_result and fund_result.returncode != 0) or fund_exception:
        err_msg = parse_swechaind_error(fund_stderr, "create-and-fund-address-funding", config['DEFAULT_FEES'], fund_exception)
        return {"status": "error", "message": f"Funding failed for new address '{new_address}': {err_msg}"}
    
    funding_tx_details = fund_result.stdout.strip() if fund_result else "No output from funding command."

    # Step 4: Format Success Response
    status_msg = f"Key '{username}' ensured (already existed)." if key_already_exists else f"Key '{username}' created."
    final_result_text = (
        f"{status_msg}\n"
        f"AGENT_ADDRESS: {new_address}\n"
        f"Funded with {amount_with_denom} from {funder_address}.\n"
        f"Funding Tx Details:\n{funding_tx_details}"
    )
    return {"status": "success", "data_type": "text", "data": final_result_text}

def execute_open_auction_tool(params: dict, config: dict) -> dict:
    stderr_output = ""
    original_exception = None
    try:
        # Parameters are aliased in Pydantic model, so access them by their Python names
        issue = params['issue']
        description = params['description']
        status = params['status']
        winner = params.get('winner', 'TBD') # .get() handles default if not provided
        from_address = params['from_address'] # from Pydantic alias 'from'

        command = [
            config['SWECHAIND_PATH'], 'tx', 'issuemarket', 'create-auction',
            issue, description, status, winner,
            '--from', from_address,
            '--keyring-backend', config['KEYRING_BACKEND'],
            '--chain-id', config['CHAIN_ID'],
            '--fees', config['DEFAULT_FEES'],
            '--yes', '--output', 'json'
        ]
        
        logging.info(f"Executing command: {' '.join(command)}")
        result = subprocess.run(
            command, capture_output=True, text=True, check=False, 
            timeout=config['COMMAND_TIMEOUT_SECONDS']
        )
        stderr_output = result.stderr if result else ""

        if result and result.returncode == 0:
            try:
                parsed_json = json.loads(result.stdout)
                logging.info("Open Auction tool executed successfully (JSON).")
                return {"status": "success", "data_type": "json", "data": parsed_json}
            except json.JSONDecodeError as je:
                logging.warning(f"Open Auction tool output was not valid JSON despite success: {je}. Output: {result.stdout.strip()}")
                return {"status": "success", "data_type": "text", "data": result.stdout.strip(), "warning": "Output was not valid JSON despite command success."}
        else: # Non-zero return code or result is None
            enhanced_error_message = parse_swechaind_error(stderr_output, "open-auction", config['DEFAULT_FEES'])
            logging.error(f"Open Auction tool execution failed. Parsed error: {enhanced_error_message}. Stderr: {stderr_output.strip() if stderr_output else 'N/A'}")
            return {"status": "error", "message": enhanced_error_message}

    except FileNotFoundError:
        logging.error(f"Command '{config['SWECHAIND_PATH']}' not found.")
        return {"status": "error", "message": f"Command '{config['SWECHAIND_PATH']}' not found."}
    except subprocess.TimeoutExpired as te:
        original_exception = te
        logging.error(f"Open Auction tool command timed out after {config['COMMAND_TIMEOUT_SECONDS']} seconds.")
        enhanced_error_message = parse_swechaind_error(stderr_output, "open-auction", config['DEFAULT_FEES'], original_exception)
        return {"status": "error", "message": enhanced_error_message}
    except KeyError as e: # Should be caught by Pydantic, but as a safeguard
        logging.error(f"Missing parameter for open-auction tool: {e}")
        return {"status": "error", "message": f"Missing required parameter for open-auction tool: {str(e)}"}
    except Exception as e:
        original_exception = e
        logging.error(f"An unexpected error occurred in execute_open_auction_tool: {e}", exc_info=True)
        enhanced_error_message = parse_swechaind_error(stderr_output, "open-auction", config['DEFAULT_FEES'], original_exception)
        return {"status": "error", "message": enhanced_error_message}

def execute_create_bid_tool(params: dict, config: dict) -> dict:
    stderr_output = ""
    original_exception = None
    try:
        auction_id = params['auctionId']
        bidder = params['bidder'] # This should be an address
        amount = params['amount']
        description = params['description']
        from_address = params['from_address'] # This should be the same as bidder

        if bidder.lower() != from_address.lower():
            logging.warning(f"Bidder '{bidder}' and from_address '{from_address}' are not the same for create-bid. Using from_address as the signer.")
            # The command uses from_address for the --from flag, so this is more of a logical check / warning.

        amount_with_denom = amount + "token"

        command = [
            config['SWECHAIND_PATH'], 'tx', 'issuemarket', 'create-bid',
            auction_id, bidder, amount_with_denom, description,
            '--from', from_address,
            '--keyring-backend', config['KEYRING_BACKEND'],
            '--chain-id', config['CHAIN_ID'],
            '--fees', config['DEFAULT_FEES'],
            '--yes', '--output', 'json'
        ]
        
        logging.info(f"Executing command: {' '.join(command)}")
        result = subprocess.run(
            command, capture_output=True, text=True, check=False, 
            timeout=config['COMMAND_TIMEOUT_SECONDS']
        )
        stderr_output = result.stderr if result else ""

        if result and result.returncode == 0:
            try:
                parsed_json = json.loads(result.stdout)
                logging.info("Create Bid tool executed successfully (JSON).")
                return {"status": "success", "data_type": "json", "data": parsed_json}
            except json.JSONDecodeError as je:
                logging.warning(f"Create Bid tool output was not valid JSON despite success: {je}. Output: {result.stdout.strip()}")
                return {"status": "success", "data_type": "text", "data": result.stdout.strip(), "warning": "Output was not valid JSON despite command success."}
        else: # Non-zero return code or result is None
            enhanced_error_message = parse_swechaind_error(stderr_output, "create-bid", config['DEFAULT_FEES'])
            logging.error(f"Create Bid tool execution failed. Parsed error: {enhanced_error_message}. Stderr: {stderr_output.strip() if stderr_output else 'N/A'}")
            return {"status": "error", "message": enhanced_error_message}

    except FileNotFoundError:
        logging.error(f"Command '{config['SWECHAIND_PATH']}' not found.")
        return {"status": "error", "message": f"Command '{config['SWECHAIND_PATH']}' not found."}
    except subprocess.TimeoutExpired as te:
        original_exception = te
        logging.error(f"Create Bid tool command timed out after {config['COMMAND_TIMEOUT_SECONDS']} seconds.")
        enhanced_error_message = parse_swechaind_error(stderr_output, "create-bid", config['DEFAULT_FEES'], original_exception)
        return {"status": "error", "message": enhanced_error_message}
    except KeyError as e: # Should be caught by Pydantic
        logging.error(f"Missing parameter for create-bid tool: {e}")
        return {"status": "error", "message": f"Missing required parameter for create-bid tool: {str(e)}"}
    except Exception as e:
        original_exception = e
        logging.error(f"An unexpected error occurred in execute_create_bid_tool: {e}", exc_info=True)
        enhanced_error_message = parse_swechaind_error(stderr_output, "create-bid", config['DEFAULT_FEES'], original_exception)
        return {"status": "error", "message": enhanced_error_message}

def execute_close_auction_tool(params: dict, config: dict) -> dict:
    stderr_output = ""
    original_exception = None
    try:
        auction_id = params['auctionId']
        issue = params['issue']
        description = params['description']
        status = params['status']
        winner = params['winner']
        from_address = params['from_address']

        if status.lower() != 'closed':
            logging.warning(f"Close-auction tool called with status '{status}', but it should typically be 'closed'. Proceeding as per Go implementation, which uses the provided status.")
            # The command `update-auction` will use the status string as provided.
            # If the intent is to *only* allow closing, this logic might need adjustment or a Pydantic validator.

        command = [
            config['SWECHAIND_PATH'], 'tx', 'issuemarket', 'update-auction',
            auction_id, issue, description, status, winner,
            '--from', from_address,
            '--keyring-backend', config['KEYRING_BACKEND'],
            '--chain-id', config['CHAIN_ID'],
            '--fees', config['DEFAULT_FEES'],
            '--yes', '--output', 'json'
        ]
        
        logging.info(f"Executing command: {' '.join(command)}")
        result = subprocess.run(
            command, capture_output=True, text=True, check=False, 
            timeout=config['COMMAND_TIMEOUT_SECONDS']
        )
        stderr_output = result.stderr if result else ""

        if result and result.returncode == 0:
            try:
                parsed_json = json.loads(result.stdout)
                logging.info("Close Auction tool executed successfully (JSON).")
                return {"status": "success", "data_type": "json", "data": parsed_json}
            except json.JSONDecodeError as je:
                logging.warning(f"Close Auction tool output was not valid JSON despite success: {je}. Output: {result.stdout.strip()}")
                return {"status": "success", "data_type": "text", "data": result.stdout.strip(), "warning": "Output was not valid JSON despite command success."}
        else: # Non-zero return code or result is None
            enhanced_error_message = parse_swechaind_error(stderr_output, "close-auction", config['DEFAULT_FEES'])
            logging.error(f"Close Auction tool execution failed. Parsed error: {enhanced_error_message}. Stderr: {stderr_output.strip() if stderr_output else 'N/A'}")
            return {"status": "error", "message": enhanced_error_message}

    except FileNotFoundError:
        logging.error(f"Command '{config['SWECHAIND_PATH']}' not found.")
        return {"status": "error", "message": f"Command '{config['SWECHAIND_PATH']}' not found."}
    except subprocess.TimeoutExpired as te:
        original_exception = te
        logging.error(f"Close Auction tool command timed out after {config['COMMAND_TIMEOUT_SECONDS']} seconds.")
        enhanced_error_message = parse_swechaind_error(stderr_output, "close-auction", config['DEFAULT_FEES'], original_exception)
        return {"status": "error", "message": enhanced_error_message}
    except KeyError as e: # Should be caught by Pydantic
        logging.error(f"Missing parameter for close-auction tool: {e}")
        return {"status": "error", "message": f"Missing required parameter for close-auction tool: {str(e)}"}
    except Exception as e:
        original_exception = e
        logging.error(f"An unexpected error occurred in execute_close_auction_tool: {e}", exc_info=True)
        enhanced_error_message = parse_swechaind_error(stderr_output, "close-auction", config['DEFAULT_FEES'], original_exception)
        return {"status": "error", "message": enhanced_error_message}

app = fastapi.FastAPI(
    title="Python MCP Server",
    version="1.0.0",
    description="Provides MCP-like tool execution over Server-Sent Events (SSE). Tools interact with `swechaind` and a feedback mechanism.",
)

@app.get("/")
async def read_root():
    return {"message": "Python MCP Server is running"}

# API documentation will be available at /docs (Swagger UI) and /redoc (ReDoc)
if __name__ == "__main__":
    server_host = os.environ.get('MCP_SERVER_HOST', '0.0.0.0')
    try:
        server_port = int(os.environ.get('MCP_SERVER_PORT', '8000'))
    except ValueError:
        logging.warning(f"Invalid MCP_SERVER_PORT value '{os.environ.get('MCP_SERVER_PORT')}', defaulting to 8000.")
        server_port = 8000
    
    logging.info(f"Starting MCP Server on {server_host}:{server_port}")
    
    # Log all configuration values
    logging.info("--- Application Configuration ---")
    for key, value in CONFIG.items():
        logging.info(f"CONFIG - {key}: {value}")
    logging.info(f"CONFIG - MCP_SERVER_HOST (for Uvicorn): {server_host}")
    logging.info(f"CONFIG - MCP_SERVER_PORT (for Uvicorn): {server_port}")
    logging.info("-------------------------------")
    
    uvicorn.run(app, host=server_host, port=server_port, reload=True)

# Tool executor mapping
TOOL_EXECUTORS = {
    "memory": execute_memory_tool,
    "balance": execute_balance_tool,
    "pay": execute_pay_tool, 
    "addr": execute_addr_tool,
    "feedbackQA": execute_feedbackqa_tool,
    "create-and-fund-address": execute_create_and_fund_tool,
    "open-auction": execute_open_auction_tool,
    "create-bid": execute_create_bid_tool,
    "close-auction": execute_close_auction_tool,
}

# Unified SSE Event Generator
async def generate_sse_events(tool_name: str, params_dict: dict, config: dict): # Added config
    try:
        logging.info(f"Starting SSE event generation for tool: {tool_name} with params: {params_dict}")
        yield f"data: {json.dumps({'event_type': 'tool_started', 'tool_name': tool_name})}\n\n"
        await asyncio.sleep(0.01)

        executor = TOOL_EXECUTORS.get(tool_name) 
        if not executor:
             logging.error(f"No executor found for tool: {tool_name} (this should not happen with specific endpoints)")
             error_payload = {'status': 'error', 'message': 'Internal server error: Executor not found for tool.', 'details': f"Tool name: {tool_name}"}
             yield f"data: {json.dumps({'event_type': 'runtime_error', 'tool_name': tool_name, 'error': error_payload})}\n\n"
             yield f"data: {json.dumps({'event_type': 'tool_finished', 'tool_name': tool_name})}\n\n"
             return

        logging.info(f"Executing tool '{tool_name}' in thread pool with params: {params_dict} and config.")
        loop = asyncio.get_event_loop()
        result = None
        try:
            result = await loop.run_in_executor(None, executor, params_dict, config) # Pass config
        except Exception as e:
            logging.error(f"Unexpected error during threaded execution of tool '{tool_name}': {e}", exc_info=True)
            error_payload = {'status': 'error', 'message': 'Tool execution crashed unexpectedly.', 'details': str(e), 'type': e.__class__.__name__}
            yield f"data: {json.dumps({'event_type': 'tool_error', 'tool_name': tool_name, 'error': error_payload})}\n\n"
            # result remains None

        if result: # If executor didn't crash and returned a result
            if result.get("status") == "success":
                logging.info(f"Tool '{tool_name}' executed successfully. Result: {result}")
                yield f"data: {json.dumps({'event_type': 'tool_result', 'tool_name': tool_name, 'result': result})}\n\n"
            else:
                logging.error(f"Tool '{tool_name}' execution failed. Error details: {result}")
                # Consistent error key 'error'
                yield f"data: {json.dumps({'event_type': 'tool_error', 'tool_name': tool_name, 'error': result})}\n\n"
        elif result is None and executor: # If result is None but we had an executor, it means it crashed (already logged)
            pass # Error already yielded in the except block

        logging.info(f"Finishing SSE event generation for tool: {tool_name}")
        yield f"data: {json.dumps({'event_type': 'tool_finished', 'tool_name': tool_name})}\n\n"

    except Exception as e:
        logging.error(f"Global error in SSE event generator for tool '{tool_name}': {e}", exc_info=True)
        try:
            # Refined error structure for global exceptions
            error_payload = {
                'status': 'error',
                'message': f'An unexpected server error occurred in SSE generator for tool {tool_name}',
                'details': str(e),
                'type': e.__class__.__name__
            }
            yield f"data: {json.dumps({'event_type': 'runtime_error', 'tool_name': tool_name, 'error': error_payload})}\n\n"
        except Exception: # If yielding the error itself fails
            logging.error("Failed to send final runtime_error message to SSE client.")
    finally:
        logging.info(f"SSE stream closed for tool: {tool_name}")

# Specific SSE Endpoints
@app.post("/tools/memory/invoke-sse", 
          summary="Invoke Memory Tool (SSE)", 
          description="Streams events for memory tool execution. Takes query and agent_name, calls feedback binary.")
async def invoke_memory_sse(params: MemoryParams):
    logging.info(f"SSE request for 'memory' tool with validated params: {params.dict()}")
    return StreamingResponse(generate_sse_events("memory", params.dict(), CONFIG), media_type="text/event-stream", headers=SSE_HEADERS)

@app.post("/tools/balance/invoke-sse",
          summary="Invoke Balance Tool (SSE)",
          description="Streams events for balance tool execution. Takes an account address, calls swechaind to query balances.")
async def invoke_balance_sse(params: BalanceParams):
    logging.info(f"SSE request for 'balance' tool with validated params: {params.dict()}")
    return StreamingResponse(generate_sse_events("balance", params.dict(), CONFIG), media_type="text/event-stream", headers=SSE_HEADERS)

@app.post("/tools/pay/invoke-sse", # Renamed from /send/invoke-sse
          summary="Invoke Pay Tool (SSE)", # Updated summary
          description="Streams events for token payment execution. Takes from_address, to_address, and amount, calls swechaind to send tokens.") # Updated description
async def invoke_pay_sse(params: PayParams): # Renamed function and Pydantic model
    params_dict = params.dict(by_alias=True) 
    logging.info(f"SSE request for 'pay' tool with validated params (aliases used): {params_dict}")
    return StreamingResponse(generate_sse_events("pay", params_dict, CONFIG), media_type="text/event-stream", headers=SSE_HEADERS)

@app.post("/tools/addr/invoke-sse",
          summary="Invoke Address Tool (SSE)",
          description="Streams events for address tool execution. Takes an account_name, calls swechaind to retrieve the address.")
async def invoke_addr_sse(params: AddrParams):
    logging.info(f"SSE request for 'addr' tool with validated params: {params.dict()}")
    return StreamingResponse(generate_sse_events("addr", params.dict(), CONFIG), media_type="text/event-stream", headers=SSE_HEADERS)

@app.post("/tools/feedbackQA/invoke-sse",
          summary="Invoke FeedbackQA Tool (SSE)",
          description="Streams events for feedbackQA tool execution. Takes a question and calls the feedbackQA binary.")
async def invoke_feedbackqa_sse(params: FeedbackQAParams):
    logging.info(f"SSE request for 'feedbackQA' tool with validated params: {params.dict()}")
    return StreamingResponse(generate_sse_events("feedbackQA", params.dict(), CONFIG), media_type="text/event-stream", headers=SSE_HEADERS)

@app.post("/tools/create-and-fund-address/invoke-sse",
          summary="Invoke Create and Fund Address Tool (SSE)",
          description="Streams events for creating a new key/address and funding it. Idempotent for key creation.")
async def invoke_create_and_fund_sse(params: CreateAndFundParams):
    logging.info(f"SSE request for 'create-and-fund-address' tool with validated params: {params.dict()}")
    return StreamingResponse(generate_sse_events("create-and-fund-address", params.dict(), CONFIG), media_type="text/event-stream", headers=SSE_HEADERS)

@app.post("/tools/open-auction/invoke-sse",
          summary="Invoke Open Auction Tool (SSE)",
          description="(Open World Hint) Creates a new auction *on the blockchain* for a specific issue. IMPORTANT: Use the AGENT_ADDRESS you previously obtained from 'create-and-fund-address' for the 'from' parameter.")
async def invoke_open_auction_sse(params: OpenAuctionParams):
    # Use by_alias=True for the 'from' parameter to match 'from_address' in Pydantic model
    params_dict = params.dict(by_alias=True) 
    logging.info(f"SSE request for 'open-auction' tool with validated params (aliases used): {params_dict}")
    return StreamingResponse(generate_sse_events("open-auction", params_dict, CONFIG), media_type="text/event-stream", headers=SSE_HEADERS)

@app.post("/tools/create-bid/invoke-sse",
          summary="Invoke Create Bid Tool (SSE)",
          description="(Open World Hint) Places a bid *on the blockchain* on an existing open auction. IMPORTANT: Use the AGENT_ADDRESS you previously obtained from 'create-and-fund-address' for the 'bidder' and 'from' parameters.")
async def invoke_create_bid_sse(params: CreateBidParams):
    # Use by_alias=True for the 'from' parameter to match 'from_address' in Pydantic model
    params_dict = params.dict(by_alias=True)
    logging.info(f"SSE request for 'create-bid' tool with validated params (aliases used): {params_dict}")
    return StreamingResponse(generate_sse_events("create-bid", params_dict, CONFIG), media_type="text/event-stream", headers=SSE_HEADERS)

@app.post("/tools/close-auction/invoke-sse",
          summary="Invoke Close Auction Tool (SSE)",
          description="(Open World Hint) Updates an auction status to 'closed' *on the blockchain*, specifying the winner. IMPORTANT: Use the AGENT_ADDRESS you previously obtained for the 'from' parameter (must be the auction creator). The 'winner' address MUST be the actual winner's address. Payment is separate via 'pay'.")
async def invoke_close_auction_sse(params: CloseAuctionParams):
    # Use by_alias=True for the 'from' parameter to match 'from_address' in Pydantic model
    params_dict = params.dict(by_alias=True)
    logging.info(f"SSE request for 'close-auction' tool with validated params (aliases used): {params_dict}")
    return StreamingResponse(generate_sse_events("close-auction", params_dict, CONFIG), media_type="text/event-stream", headers=SSE_HEADERS)
