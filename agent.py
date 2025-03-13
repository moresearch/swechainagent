#!/usr/bin/env python3
import sys
import json
import subprocess
import time
import shlex
import random
import re
import traceback

# Constants
SWECHAIN_PATH = "swechaind"
FIXED_WORKING_DIR = "/tmp"
AGENT_NAME = "swechainagent"
AGENT_ADDRESS = "starcraft_rule!"
LOOP_WAIT = 1

# Define initial objectives
objectives = [
    "explore the market for profitable opportunities",
    "create new auctions",
    "place bids on open auctions",
    "increase market activity",
    "maintain a healthy account balance",
    "maximize profits"
]

# Command learning storage
learned_commands = {
    "issuemarket": [],
    "bank": []
}

# Helper Functions
def execute_command(command):
    try:
        cmd_list = shlex.split(command)
        result = subprocess.run(cmd_list, capture_output=True, text=True, check=False, cwd=FIXED_WORKING_DIR)
        return result.stdout.strip() or result.stderr.strip()
    except Exception as e:
        return str(e)

def swechaind_query(subcommand):
    return execute_command(f"{SWECHAIN_PATH} query {subcommand} --output json")

def swechaind_action(subcommand):
    return execute_command(f"{SWECHAIN_PATH} tx {subcommand} --from {AGENT_NAME} --yes --output json")

def get_help_message(command_type, subcommand):
    return execute_command(f"{SWECHAIN_PATH} {command_type} {subcommand} --help")

def extract_available_commands(help_message):
    """Extract available commands from help message"""
    commands = []
    if "Available Commands:" in help_message:
        command_section = help_message.split("Available Commands:")[1].split("\n\n")[0]
        command_lines = command_section.strip().split("\n")
        for line in command_lines:
            if line.strip():
                parts = line.strip().split()
                if parts:
                    commands.append(parts[0])
    return commands

def learn_from_error(error_message, tool_area):
    """Learn from error messages by extracting available commands"""
    global learned_commands
    
    # Extract available commands from error message
    commands = extract_available_commands(error_message)
    
    if commands:
        print(f"Learned new commands for {tool_area}: {commands}")
        learned_commands[tool_area] = list(set(learned_commands[tool_area] + commands))

def parse_json_safely(text):
    """Safely parse JSON or return None"""
    try:
        return json.loads(text)
    except:
        return None

# Improved agent functions with learning capabilities
def select_query(agent_name, agent_address, objective):
    """Query selector with learning capability"""
    print(f"Selecting query for objective: {objective}")
    
    # Initial query guesses based on objectives
    query_map = {
        "explore": {"tool_area": "issuemarket", "query": "list-auction"},
        "create": {"tool_area": "issuemarket", "query": "list-auction"},
        "bid": {"tool_area": "issuemarket", "query": "list-auction"},
        "increase": {"tool_area": "issuemarket", "query": "list-auction"},
        "balance": {"tool_area": "bank", "query": "balances"},
        "profits": {"tool_area": "bank", "query": "balances"},
    }
    
    # Find matching query
    for key, value in query_map.items():
        if key in objective.lower():
            # Check if we've learned better commands for this tool area
            tool_area = value["tool_area"]
            if tool_area in learned_commands and learned_commands[tool_area]:
                # Use learned commands that might be relevant to the objective
                relevant_commands = []
                for cmd in learned_commands[tool_area]:
                    if any(term in cmd for term in ["list", "get", "show"]):
                        relevant_commands.append(cmd)
                
                if relevant_commands:
                    return {"tool_area": tool_area, "query": random.choice(relevant_commands)}
            
            return value
    
    # Default query - use learned commands if available
    if learned_commands["issuemarket"]:
        list_commands = [cmd for cmd in learned_commands["issuemarket"] if "list" in cmd]
        if list_commands:
            return {"tool_area": "issuemarket", "query": random.choice(list_commands)}
    
    return {"tool_area": "issuemarket", "query": "list-auction"}

def reflect_on_feedback(agent_name, environment_feedback, help_message):
    """Improved reflection with learning capabilities"""
    print("Reflecting on environment feedback...")
    
    # If feedback looks like help/error message, learn from it
    if "Available Commands:" in environment_feedback:
        learn_from_error(environment_feedback, "issuemarket")
        return f"The command I tried wasn't correct. I learned these alternative commands: {learned_commands['issuemarket']}. I'll try one of these next time."
    
    # Parse feedback
    data = parse_json_safely(environment_feedback)
    if data is not None:
        if isinstance(data, list):
            reflection = f"Analyzed feedback: Found {len(data)} items."
            
            # Add specific reflections based on data structure
            if len(data) > 0 and isinstance(data[0], dict):
                keys = data[0].keys()
                reflection += f" Data has these fields: {', '.join(keys)}."
                
                # Identify what type of data we're looking at
                if "auctionID" in keys or "auction" in keys:
                    reflection += " These appear to be auction listings."
                elif "issueID" in keys or "issue" in keys:
                    reflection += " These appear to be market issues."
                elif "bidID" in keys or "bid" in keys:
                    reflection += " These appear to be bid listings."
                
            return reflection
        elif isinstance(data, dict):
            keys = data.keys()
            return f"Received data with keys: {', '.join(keys)}. This appears to be a single object response."
    
    return "Unable to parse feedback as JSON. It appears to be either an error message or plain text. I'll try a different approach."

def select_action(agent_name, agent_address, reflection):
    """Improved action selector with learning capabilities"""
    print("Selecting action based on reflection...")
    
    # Check if we detected an error in our previous command
    if "command I tried wasn't correct" in reflection:
        # Try to get help to learn more
        return "swechaind_query issuemarket --help"
    
    # Action logic based on reflection content
    if "auction listings" in reflection:
        return "swechaind_action issuemarket create-auction --issue-index 1 --starting-price 100 --duration 86400"
    elif "market issues" in reflection:
        return "swechaind_action issuemarket create-auction --issue-index 1 --starting-price 100 --duration 86400"
    elif "bid listings" in reflection:
        return "swechaind_action issuemarket place-bid --auction-index 1 --amount 150"
    elif "keys: " in reflection:
        # We have some structured data, let's query more information
        if learned_commands["issuemarket"]:
            # Use one of the learned list commands
            list_commands = [cmd for cmd in learned_commands["issuemarket"] if "list" in cmd]
            if list_commands:
                return f"swechaind_query issuemarket {random.choice(list_commands)}"
    
    # If we have no specific action, try to get more information using learned commands
    if learned_commands["issuemarket"]:
        return f"swechaind_query issuemarket {random.choice(learned_commands['issuemarket'])}"
    
    # Default fallback action
    return "swechaind_query issuemarket --help"

def adjust_objectives(environment_feedback):
    global objectives
    
    # Example logic to dynamically adjust objectives based on environment feedback
    data = parse_json_safely(environment_feedback)
    
    if data:
        # Adjust objectives based on data
        if isinstance(data, list):
            if len(data) > 5:
                objectives.append("focus on high value auctions")
            if len(data) < 2:
                objectives.append("create more auctions to stimulate the market")
                
            # Check if we have any auctions with bids
            has_bids = False
            for item in data:
                if isinstance(item, dict) and ('bids' in item or 'bidCount' in item or 'bidding' in str(item).lower()):
                    has_bids = True
                    break
            
            if has_bids:
                objectives.append("analyze bidding strategies")
            
        # Ensure objectives are unique and limit to reasonable number
        objectives = list(set(objectives))
        if len(objectives) > 10:
            objectives = random.sample(objectives, 10)

def main():
    global AGENT_NAME, AGENT_ADDRESS
    if len(sys.argv) >= 3:
        AGENT_NAME = sys.argv[1]
        AGENT_ADDRESS = sys.argv[2]

    print(f"Starting agent {AGENT_NAME} with address {AGENT_ADDRESS}")
    
    # Initialize by learning available commands
    print("Learning available commands...")
    issuemarket_help = get_help_message("query", "issuemarket")
    bank_help = get_help_message("query", "bank")
    
    learn_from_error(issuemarket_help, "issuemarket")
    learn_from_error(bank_help, "bank")
    
    print(f"Initially learned commands: {learned_commands}")

    while True:
        try:
            objective = random.choice(objectives)
            print(f"\n--- 1. Feedback ---")
            query = select_query(AGENT_NAME, AGENT_ADDRESS, objective)
            query_tool_area, query_subcommand = query["tool_area"], query["query"]
            print(f"Selected query: {query_tool_area} {query_subcommand}")
            
            environment_feedback = swechaind_query(f"{query_tool_area} {query_subcommand}")
            print(f"Received feedback: {environment_feedback[:100]}..." if len(environment_feedback) > 100 else f"Received feedback: {environment_feedback}")

            print(f"\n--- 2. Reflection ---")
            help_message = get_help_message("query", f"{query_tool_area} {query_subcommand}")
            reflection = reflect_on_feedback(AGENT_NAME, environment_feedback, help_message)
            print(f"Reflection: {reflection}")

            print(f"\n--- 3. Action ---")
            action = select_action(AGENT_NAME, AGENT_ADDRESS, reflection)
            print(f"Selected action: {action}")
            
            if action.strip():
                parts = action.split(None, 2)
                if len(parts) >= 3:
                    action_type, action_tool_area, action_subcommand = parts
                    if "swechaind_query" == action_type:
                        result = swechaind_query(f"{action_tool_area} {action_subcommand}")
                        print(f"Query Result: {result[:200]}..." if len(result) > 200 else f"Query Result: {result}")
                        
                        # Learn from query result if it contains help information
                        if "Available Commands:" in result:
                            learn_from_error(result, action_tool_area)
                            
                    elif "swechaind_action" == action_type:
                        result = swechaind_action(f"{action_tool_area} {action_subcommand}")
                        print(f"Action Result: {result[:200]}..." if len(result) > 200 else f"Action Result: {result}")
                    else:
                        print("Unknown action type")
                else:
                    print("Action format incorrect")
            else:
                print("No action")

            # Adjust objectives based on environment feedback
            adjust_objectives(environment_feedback)
            print(f"Current objectives: {objectives}")

        except Exception as e:
            print(f"Error: {e}")
            print(traceback.format_exc())
            
        time.sleep(LOOP_WAIT)

if __name__ == "__main__":
    main()