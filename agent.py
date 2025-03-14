#!/usr/bin/env python3
import subprocess, json, time, sys, signal, datetime, os
import dspy

# Initialize LLM
try:
    lm = dspy.LM('ollama_chat/llama3.2:3b', api_base='http://localhost:11434', api_key='')
    dspy.configure(lm=lm)
except Exception as e:
    print(f"LLM initialization error: {e}")

# Constants and Globals
AGENT_NAME = sys.argv[2] if len(sys.argv) >= 3 else "agent" 
AGENT_ADDR = sys.argv[3] if len(sys.argv) >= 4 else "cosmos1default"
DATA_FILE = sys.argv[1] if len(sys.argv) >= 2 else f"{AGENT_NAME}.jsonl"
TRAJ_FILE = f"{AGENT_NAME}.traj"
DATA_LAST_MODIFIED = 0

def log_trajectory(event_type, data):
    """Log agent action to trajectory file"""
    try:
        timestamp = datetime.datetime.utcnow().strftime("%Y-%m-%d %H:%M:%S")
        entry = {
            "timestamp": timestamp,
            "agent": AGENT_NAME,
            "event": event_type,
            "data": data
        }
        with open(TRAJ_FILE, "a") as f:
            f.write(json.dumps(entry) + "\n")
    except Exception as e:
        print(f"Error logging to trajectory: {e}")

def shell(cmd, log=True):
    """Execute shell command and return output"""
    print(f"$ {cmd}")
    try:
        result = subprocess.run(cmd, shell=True, text=True, capture_output=True)
        output = result.stdout.strip() or result.stderr.strip()
        print(f"Output: {output[:100]}..." if len(output) > 100 else f"Output: {output}")
        if log:
            log_trajectory("command", {"cmd": cmd, "output": output})
        return output
    except Exception as e:
        error_msg = str(e)
        print(f"Error: {error_msg}")
        if log:
            log_trajectory("error", {"cmd": cmd, "error": error_msg})
        return error_msg

def get_balance():
    """Get agent's balance"""
    try:
        output = shell(f"swechaind query bank balances {AGENT_NAME} --output json", log=False)
        data = json.loads(output)
        if "balances" in data and data["balances"]:
            for balance in data["balances"]:
                if balance.get("denom") == "stake":
                    return int(balance["amount"])
        return 0
    except:
        return 0

def get_market_snapshot():
    """Get a snapshot of the market state"""
    try:
        auctions_output = shell("swechaind query issuemarket list-auction --output json", log=False)
        bids_output = shell("swechaind query issuemarket list-bid --output json", log=False)
        
        auctions_data = json.loads(auctions_output)
        auctions = []
        if isinstance(auctions_data, dict) and "auctions" in auctions_data:
            auctions = auctions_data["auctions"]
        elif isinstance(auctions_data, list):
            auctions = auctions_data
            
        # Find auctions created by this agent
        own_auctions = []
        for auction in auctions:
            if auction.get("creator") == AGENT_NAME or auction.get("creatorAddr") == AGENT_ADDR:
                own_auctions.append(auction)
                
        # Find active auctions by others
        other_auctions = []
        for auction in auctions:
            if (auction.get("creator") != AGENT_NAME and auction.get("creatorAddr") != AGENT_ADDR 
                and auction.get("status", "").lower() == "open"):
                other_auctions.append(auction)
                
        return {
            "balance": get_balance(),
            "own_auctions": own_auctions,
            "other_auctions": other_auctions[:3],  # Limit to 3 for prompt size
            "auctions_count": len(auctions),
            "own_auctions_count": len(own_auctions)
        }
    except Exception as e:
        print(f"Error getting market snapshot: {e}")
        return {"balance": 0, "own_auctions": [], "other_auctions": [], "auctions_count": 0, "own_auctions_count": 0}

def run_sweagent(issue_url=None):
    """Run SWE agent on specified issue"""
    if not issue_url:
        issue_url = "https://github.com/SWE-agent/test-repo/issues/1"
    
    cmd = f"sweagent run --agent.model.name=gpt-4o --agent.model.per_instance_cost_limit=2.00 " \
          f"--env.repo.github_url=https://github.com/SWE-agent/test-repo " \
          f"--problem_statement.github_url={issue_url}"
    return shell(cmd)

def ask_with_dspy(prompt, query_type="market_decision"):
    """Ask LLM using DSPy with improved error handling"""
    class MarketReasoner(dspy.Signature):
        situation = dspy.InputField()
        reasoning = dspy.OutputField(desc="Step-by-step reasoning about market situation")
        decision = dspy.OutputField(desc="Final decision as exact command")
    
    try:
        if query_type == "market_decision":
            result = dspy.Predict(MarketReasoner)(situation=prompt)
            reasoning_text = result.reasoning
            decision_text = result.decision
            
            print("\n=== AGENT REASONING ===")
            print(reasoning_text)
            print("=== AGENT DECISION ===")
            print(decision_text)
            
            log_trajectory("reasoning", {
                "prompt": prompt,
                "reasoning": reasoning_text,
                "decision": decision_text
            })
            
            return decision_text
        else:
            result = dspy.Predict(lambda x: x)(prompt=prompt).completion
            log_trajectory("query", {
                "prompt": prompt,
                "response": result
            })
            return result
    except Exception as e:
        error_msg = str(e)
        print(f"DSPy error: {error_msg}")
        print("\n=== AGENT REASONING FAILED ===")
        print("Agent couldn't make a decision due to reasoning error")
        
        log_trajectory("reasoning_error", {
            "prompt": prompt,
            "error": error_msg
        })
        
        return "DECISION_FAILED"

def load_agent_data():
    """Load agent data from JSONL file, monitoring for changes"""
    global DATA_LAST_MODIFIED
    
    try:
        # Check if file has been modified since last load
        current_mtime = os.path.getmtime(DATA_FILE)
        if current_mtime == DATA_LAST_MODIFIED:
            return None  # No changes
        
        # File has changed, load the new data
        DATA_LAST_MODIFIED = current_mtime
        issues = []
        
        with open(DATA_FILE, 'r') as f:
            for line in f:
                try:
                    issue = json.loads(line.strip())
                    issues.append(issue)
                except:
                    pass
        
        print(f"Loaded {len(issues)} issues from {DATA_FILE}")
        log_trajectory("data_reload", {"file": DATA_FILE, "issues_count": len(issues)})
        return {"issues": issues}
    
    except FileNotFoundError:
        # If file doesn't exist, create it with sample data
        print(f"Data file {DATA_FILE} not found, creating with sample data")
        create_sample_data()
        DATA_LAST_MODIFIED = os.path.getmtime(DATA_FILE)
        return load_agent_data()
    
    except Exception as e:
        print(f"Error loading agent data: {e}")
        log_trajectory("data_error", {"file": DATA_FILE, "error": str(e)})
        return {"issues": []}

def create_sample_data():
    """Create sample JSONL data file with issues"""
    sample_issues = [
        {"desc": "Fix memory leak in login module", "cost": 5000, "priority": "high"},
        {"desc": "Implement user authentication", "cost": 7500, "priority": "medium"},
        {"desc": "Fix security vulnerability in payment processing", "cost": 8000, "priority": "critical"},
        {"desc": "Add dark mode support", "cost": 3500, "priority": "low"},
        {"desc": "Fix database connection timeout", "cost": 4200, "priority": "medium"}
    ]
    
    with open(DATA_FILE, 'w') as f:
        for issue in sample_issues:
            f.write(json.dumps(issue) + "\n")
            
    log_trajectory("data_created", {"file": DATA_FILE, "issues_count": len(sample_issues)})

def decide_action(agent_data):
    """Decide what action to take based on market data and agent's issues"""
    # Get market snapshot
    market = get_market_snapshot()
    
    # Format agent data
    issues = agent_data.get("issues", [])
    if not issues:
        issues = [{"desc": "Generic issue", "cost": 5000}]
    
    # Current date and time for context
    now = datetime.datetime.utcnow().strftime("%Y-%m-%d %H:%M:%S")
    
    prompt = f"""
Date: {now}
You are {AGENT_NAME}, a profit-maximizing blockchain agent with address {AGENT_ADDR}.

YOUR MARKET STATUS:
- Current balance: {market['balance']} tokens
- You have {market['own_auctions_count']} active auctions
- There are {market['auctions_count']} total auctions in the market
- Your issues to work on: {json.dumps(issues[:2])}

MARKET OPPORTUNITIES:
- Open auctions to bid on: {json.dumps(market['other_auctions'])}
- Your own auctions: {json.dumps(market['own_auctions'])}

ECONOMIC INCENTIVES:
1. CREATE AUCTIONS to outsource your issues and save development costs
   - You pay winning bidders but save your time for more valuable work
   - Command: swechaind tx issuemarket create-auction "BUG-123" "Fix memory leak" "open" "" --from {AGENT_NAME} --yes

2. PLACE BIDS to earn tokens by completing others' issues
   - Lower bids increase chances of winning but reduce profit
   - Higher bids increase profit but reduce chances of winning
   - Command: swechaind tx issuemarket create-bid "0" "0" {AGENT_ADDR} "500" "Will fix fast" --from {AGENT_NAME} --yes

3. CLOSE YOUR AUCTIONS when ready to award to lowest bidder
   - Command: swechaind tx issuemarket update-auction 0 "BUG-123" "Fix memory leak" "closed" "" --from {AGENT_NAME} --yes

4. MARKET RESEARCH actions:
   - Check market: swechaind query issuemarket list-auction --output json
   - Check balance: swechaind query bank balances {AGENT_NAME} --output json

Think economically! Balance creating auctions and bidding to maximize profit.
Return ONLY the exact command to execute.
"""
    response = ask_with_dspy(prompt)
    
    # Check if decision failed
    if response == "DECISION_FAILED":
        print("Using fallback command due to reasoning failure")
        return f"swechaind query issuemarket list-auction --output json"
    
    # Extract command if present
    if "swechaind" in response:
        for line in response.split("\n"):
            if "swechaind" in line:
                return line.strip()
    
    print("Agent returned response without valid command: fallback to market research")
    return f"swechaind query issuemarket list-auction --output json"

def signal_handler(sig, frame):
    """Handle Ctrl+C gracefully"""
    print("\nAgent shutting down gracefully...")
    log_trajectory("shutdown", {"reason": "user_interrupt"})
    sys.exit(0)

def main():
    global AGENT_NAME, AGENT_ADDR
    
    if len(sys.argv) < 2:
        print("Usage: ./agent.py <data_file.jsonl> [agent_name] [agent_address]")
        sys.exit(1)
    
    # Initialize trajectory log
    log_trajectory("startup", {
        "agent": AGENT_NAME,
        "address": AGENT_ADDR,
        "data_file": DATA_FILE
    })
    
    # Register signal handler for graceful exit
    signal.signal(signal.SIGINT, signal_handler)
    
    # First data load
    agent_data = load_agent_data()
    
    if agent_data:
        print(f"Agent {AGENT_NAME} started with {len(agent_data.get('issues', []))} issues")
    else:
        print(f"Agent {AGENT_NAME} started but couldn't load data")
        agent_data = {"issues": []}
    
    iteration = 0
    
    # Infinite loop
    while True:
        iteration += 1
        print(f"\n=== ITERATION {iteration} ({datetime.datetime.utcnow().strftime('%Y-%m-%d %H:%M:%S')}) ===")
        
        # Check for data updates
        new_data = load_agent_data()
        if new_data:  # Only update if file changed
            agent_data = new_data
        
        # Get action from LLM
        action = decide_action(agent_data)
        
        # If bidding, check balance first
        if "create-bid" in action:
            bid_amount = 500  # Default
            try:
                # Extract bid amount from command
                parts = action.split('"')
                for j, part in enumerate(parts):
                    if part.isdigit() and j > 3:  # Should be after auction id and bidder address
                        bid_amount = int(part)
                        break
            except:
                pass
                
            if get_balance() < bid_amount:
                print(f"⚠️ Insufficient balance ({get_balance()}) for bidding {bid_amount}! Checking market instead.")
                log_trajectory("balance_check_failed", {
                    "balance": get_balance(),
                    "required": bid_amount,
                    "action": action
                })
                action = f"swechaind query issuemarket list-auction --output json"
            else:
                print(f"✓ Balance sufficient for bid: {get_balance()} >= {bid_amount}")
                log_trajectory("balance_check_passed", {
                    "balance": get_balance(),
                    "required": bid_amount
                })
        
        # Execute action
        if action.startswith("swechaind"):
            shell(action)
        else:
            print(f"Invalid action: {action}")
            print("Agent couldn't make a valid decision. Doing market research instead.")
            log_trajectory("invalid_action", {"action": action})
            shell(f"swechaind query issuemarket list-auction --output json")
            
        time.sleep(0.5)

if __name__ == "__main__":
    main()