#!/usr/bin/env python3
import subprocess, json, time, sys, random, datetime
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
ITERATIONS = int(sys.argv[4]) if len(sys.argv) >= 5 and sys.argv[4].isdigit() else 10

def shell(cmd):
    """Execute shell command and return output"""
    print(f"$ {cmd}")
    try:
        result = subprocess.run(cmd, shell=True, text=True, capture_output=True)
        output = result.stdout.strip() or result.stderr.strip()
        print(f"Output: {output[:100]}..." if len(output) > 100 else f"Output: {output}")
        return output
    except Exception as e:
        print(f"Error: {e}")
        return str(e)

def get_balance():
    """Get agent's balance"""
    try:
        output = shell(f"swechaind query bank balances {AGENT_NAME} --output json")
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
        auctions_output = shell("swechaind query issuemarket list-auction --output json")
        bids_output = shell("swechaind query issuemarket list-bid --output json")
        
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
            print("\n=== AGENT REASONING ===")
            print(result.reasoning)
            print("=== AGENT DECISION ===")
            print(result.decision)
            return result.decision
        else:
            return dspy.Predict(lambda x: x)(prompt=prompt).completion
    except Exception as e:
        print(f"DSPy error: {e}")
        print("\n=== AGENT REASONING FAILED ===")
        print("Agent couldn't make a decision due to reasoning error")
        return "DECISION_FAILED"

def decide_action(agent_data):
    """Decide what action to take based on market data and agent's issues"""
    # Get market snapshot
    market = get_market_snapshot()
    
    # Format agent data
    issues = agent_data.get("issues", [])
    if not issues:
        issues = [{"desc": "Generic issue", "cost": 5000}]
    
    # Current date and time for context
    now = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    
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

def main():
    if len(sys.argv) < 2:
        print("Usage: ./agent.py <data_file.json> [agent_name] [agent_address] [iterations]")
        sys.exit(1)
        
    # Load agent data
    try:
        agent_data = json.load(open(sys.argv[1]))
    except Exception as e:
        print(f"Error loading data file: {e}")
        agent_data = {"issues": []}
        
    print(f"Agent {AGENT_NAME} started with {len(agent_data.get('issues', []))} issues")
    
    for i in range(ITERATIONS):
        print(f"\n=== ITERATION {i+1}/{ITERATIONS} ===")
        
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
                action = f"swechaind query issuemarket list-auction --output json"
            else:
                print(f"✓ Balance sufficient for bid: {get_balance()} >= {bid_amount}")
        
        # Execute action
        if action.startswith("swechaind"):
            shell(action)
        else:
            print(f"Invalid action: {action}")
            print("Agent couldn't make a valid decision. Doing market research instead.")
            shell(f"swechaind query issuemarket list-auction --output json")
            
        time.sleep(0.5)

if __name__ == "__main__":
    main()