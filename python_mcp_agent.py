#!/usr/bin/env python3

import sys
import json
import subprocess
import logging

# Basic logging configuration
logging.basicConfig(stream=sys.stderr, level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')

# Placeholder for MCP request handling
# TODO: Implement MCP request handling

# Placeholder for tool definitions
MCP_TOOLS = [
    {
        "name": "memory",
        "description": "Memory enables adaptive decision-making, allowing you to adjust its strategies based on accumulated knowledge",
        "parameters": [
            {
                "name": "query",
                "type": "string",
                "description": "query the memory for real-time information",
                "required": True,
            },
            {
                "name": "agent_name",
                "type": "string",
                "description": "agent_name is your given name, and it is used to RAG over your memories",
                "required": True,
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
                "description": "account to query the balance",
                "required": True,
            }
        ],
    },
    {
        "name": "send",
        "description": "sends an amount from one account's address to another, use this tool when trying to send tokens to another address from your address",
        "parameters": [
            {
                "name": "from",
                "type": "string",
                "description": "your account address",
                "required": True,
            },
            {
                "name": "to",
                "type": "string",
                "description": "account address you want to send to",
                "required": True,
            },
            {
                "name": "amount",
                "type": "string",
                "description": "amount to send, example: it just numbers like 100 or 200 or 300, etc.",
                "required": True,
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
                "description": "the name of the person you want to get its address",
                "required": True,
            }
        ],
    },
]
# TODO: Define tools

# Placeholder for tool handler functions

# Helper function to find tool definition
def _get_tool_definition(tool_name):
    for tool in MCP_TOOLS:
        if tool["name"] == tool_name:
            return tool
    return None

# Handler for the 'memory' tool
def handle_memory(params):
    logging.info(f"Handling 'memory' tool with params: {params}")
    definition = _get_tool_definition("memory")
    if not definition:
        return {"error": "Tool definition not found for 'memory'"}

    for param_def in definition.get("parameters", []):
        if param_def.get("required") and param_def["name"] not in params:
            return {"error": "Missing required parameter", "parameter_name": param_def["name"]}

    try:
        agent_name = params["agent_name"]
        query = params["query"]
        # Ensure ./bin/feedback is executable: os.chmod('./bin/feedback', 0o755) might be needed once if not set
        command = ['./bin/feedback', '--env', agent_name.lower(), '--mode', 'infer', '--query', query]

        logging.info(f"Executing command: {' '.join(command)}")
        result = subprocess.run(command, capture_output=True, text=True, check=False)

        if result.returncode == 0:
            return {"text_response": result.stdout.strip()}
        else:
            logging.error(f"Memory tool execution failed. Return code: {result.returncode}, Stderr: {result.stderr.strip()}")
            return {"error": "Memory tool execution failed", "details": result.stderr.strip(), "returncode": result.returncode}
    except KeyError as e:
        logging.error(f"Missing parameter for memory tool: {e}")
        return {"error": "Missing required parameter for memory tool", "parameter_name": str(e)}
    except Exception as e:
        logging.error(f"Exception in handle_memory: {e}")
        return {"error": "Exception during memory tool execution", "details": str(e)}

# Handler for the 'balance' tool
def handle_balance(params):
    logging.info(f"Handling 'balance' tool with params: {params}")
    definition = _get_tool_definition("balance")
    if not definition:
        return {"error": "Tool definition not found for 'balance'"}

    for param_def in definition.get("parameters", []):
        if param_def.get("required") and param_def["name"] not in params:
            return {"error": "Missing required parameter", "parameter_name": param_def["name"]}

    try:
        account = params["account"]
        command = ['swechaind', 'query', 'bank', 'balances', account, '--output', 'json']

        logging.info(f"Executing command: {' '.join(command)}")
        result = subprocess.run(command, capture_output=True, text=True, check=False)

        if result.returncode == 0:
            try:
                json_response = json.loads(result.stdout)
                return {"json_response": json_response}
            except json.JSONDecodeError as e:
                logging.error(f"Failed to parse JSON from balance tool output: {e}. Output: {result.stdout.strip()}")
                return {"error": "Failed to parse JSON output from balance tool", "details": str(e), "raw_output": result.stdout.strip()}
        else:
            logging.error(f"Balance tool execution failed. Return code: {result.returncode}, Stderr: {result.stderr.strip()}")
            return {"error": "Balance tool execution failed", "details": result.stderr.strip(), "returncode": result.returncode}
    except KeyError as e:
        logging.error(f"Missing parameter for balance tool: {e}")
        return {"error": "Missing required parameter for balance tool", "parameter_name": str(e)}
    except Exception as e:
        logging.error(f"Exception in handle_balance: {e}")
        return {"error": "Exception during balance tool execution", "details": str(e)}

# Handler for the 'send' tool
def handle_send(params):
    logging.info(f"Handling 'send' tool with params: {params}")
    definition = _get_tool_definition("send")
    if not definition:
        return {"error": "Tool definition not found for 'send'"}

    for param_def in definition.get("parameters", []):
        if param_def.get("required") and param_def["name"] not in params:
            return {"error": "Missing required parameter", "parameter_name": param_def["name"]}

    try:
        from_addr = params["from"]
        to_addr = params["to"]
        amount = params["amount"]

        command = ['swechaind', 'tx', 'bank', 'send', from_addr.lower(), to_addr.lower(), amount, '--from', from_addr.lower(), '--output', 'json', '--yes']

        logging.info(f"Executing command: {' '.join(command)}")
        result = subprocess.run(command, capture_output=True, text=True, check=False)

        if result.returncode == 0:
            try:
                json_response = json.loads(result.stdout)
                return {"json_response": json_response}
            except json.JSONDecodeError as e:
                logging.error(f"Failed to parse JSON from send tool output: {e}. Output: {result.stdout.strip()}")
                if result.stdout.strip():
                    return {"error": "Send tool output was not valid JSON", "details": str(e), "raw_output": result.stdout.strip()}
                else:
                     return {"error": "Send tool execution might have failed or produced no JSON output", "details": result.stderr.strip() or "No stderr output", "returncode": result.returncode}
        else:
            logging.error(f"Send tool execution failed. Return code: {result.returncode}, Stdout: {result.stdout.strip()}, Stderr: {result.stderr.strip()}")
            return {"error": "Send tool execution failed", "details": result.stderr.strip() or result.stdout.strip() or "No output", "returncode": result.returncode}
    except KeyError as e:
        logging.error(f"Missing parameter for send tool: {e}")
        return {"error": "Missing required parameter for send tool", "parameter_name": str(e)}
    except Exception as e:
        logging.error(f"Exception in handle_send: {e}")
        return {"error": "Exception during send tool execution", "details": str(e)}

# Handler for the 'addr' tool
def handle_addr(params):
    logging.info(f"Handling 'addr' tool with params: {params}")
    definition = _get_tool_definition("addr")
    if not definition:
        return {"error": "Tool definition not found for 'addr'"}

    for param_def in definition.get("parameters", []):
        if param_def.get("required") and param_def["name"] not in params:
            return {"error": "Missing required parameter", "parameter_name": param_def["name"]}

    try:
        account_name = params["account_name"]
        command = ['swechaind', 'keys', 'show', account_name.lower(), '-a']

        logging.info(f"Executing command: {' '.join(command)}")
        result = subprocess.run(command, capture_output=True, text=True, check=False)

        if result.returncode == 0:
            return {"text_response": result.stdout.strip()}
        else:
            logging.error(f"Addr tool execution failed. Return code: {result.returncode}, Stderr: {result.stderr.strip()}")
            return {"error": "Addr tool execution failed", "details": result.stderr.strip(), "returncode": result.returncode}
    except KeyError as e:
        logging.error(f"Missing parameter for addr tool: {e}")
        return {"error": "Missing required parameter for addr tool", "parameter_name": str(e)}
    except Exception as e:
        logging.error(f"Exception in handle_addr: {e}")
        return {"error": "Exception during addr tool execution", "details": str(e)}

# TODO: Implement tool handler functions (This comment can be removed after full integration)

def send_mcp_response(response_dict):
    """Serializes a dictionary to JSON and prints it to stdout for MCP communication."""
    try:
        response_json = json.dumps(response_dict)
        print(response_json)
        sys.stdout.flush()  # Ensure the message is sent immediately
        logging.info(f"Sent MCP response: {response_json}")
    except TypeError as e:
        logging.error(f"Error serializing response to JSON: {e}. Response: {response_dict}")
        # Fallback: try to send a minimal error response if serialization fails
        error_response = json.dumps({"error": "Internal server error", "details": "Failed to serialize original response."})
        print(error_response)
        sys.stdout.flush()
        logging.info(f"Sent fallback MCP error response: {error_response}")


def main():
    logging.info("MCP Agent started. Waiting for requests on stdin.")
    while True:
        line = sys.stdin.readline()
        if not line:  # End of input
            logging.info("EOF received, exiting.")
            break

        line = line.strip() # Remove leading/trailing whitespace, especially newline
        if not line: # Skip empty lines
            continue

        try:
            request_data = json.loads(line)
            logging.info(f"Received MCP request: {request_data}")

            tool_name = request_data.get("tool_name")
            tool_params = request_data.get("parameters", {}) # Default to empty dict if 'parameters' is missing

            response_payload = {}
            status = "error" # Default to error

            if not tool_name:
                logging.error("Invalid MCP request: 'tool_name' missing.")
                response_payload = {"error": "Invalid MCP request format", "details": "Missing tool_name"}
            else:
                logging.info(f"Tool '{tool_name}' requested with params: {tool_params}")
                handler_result = None
                if tool_name == "memory":
                    handler_result = handle_memory(tool_params)
                elif tool_name == "balance":
                    handler_result = handle_balance(tool_params)
                elif tool_name == "send":
                    handler_result = handle_send(tool_params)
                elif tool_name == "addr":
                    handler_result = handle_addr(tool_params)
                else:
                    logging.warning(f"Unknown tool requested: {tool_name}")
                    handler_result = {"error": "Unknown tool", "tool_name": tool_name}

                if handler_result:
                    if "error" in handler_result:
                        response_payload = handler_result # Error already formatted by handler
                        status = "error"
                    else:
                        response_payload = handler_result # Success, result is the payload
                        status = "success"
                else:
                    # This case should ideally not be reached if handlers are robust
                    logging.error(f"Tool handler for '{tool_name}' returned None or an empty result.")
                    response_payload = {"error": "Tool handler returned no result", "tool_name": tool_name}
                    status = "error"

            send_mcp_response({
                "status": status,
                # Include tool_name in the response, even if it was missing in the request (will be None)
                # or if the tool_name was unknown.
                "tool_name": tool_name if tool_name else "N/A",
                "result": response_payload
            })

        except json.JSONDecodeError as e:
            logging.error(f"Failed to decode JSON from stdin: {e}. Line: '{line}'")
            send_mcp_response({
                "error": "Invalid JSON format",
                "details": str(e)
            })
        except Exception as e:
            logging.error(f"An unexpected error occurred while processing request: {e}. Line: '{line}'")
            send_mcp_response({
                "error": "Unexpected server error",
                "details": str(e)
            })


if __name__ == "__main__":
    main()
