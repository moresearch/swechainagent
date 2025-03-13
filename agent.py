#!/usr/bin/env python3
"""Minimal Swechaind Agent - 2025-03-13"""
import subprocess, json, time, sys, random, os
import dspy

# Setup
lm = dspy.LM('ollama/deepseek-r1:1.5b', api_base='http://localhost:11434')
dspy.configure(lm=lm)
AGENT_NAME = sys.argv[2] if len(sys.argv) > 2 else "agent"
AGENT_ADDRESS = sys.argv[3] if len(sys.argv) > 3 else "cosmos1default"

def cmd(command):
    """Execute shell command"""
    print(f"$ {command}")
    result = subprocess.run(command, shell=True, text=True, capture_output=True)
    output = result.stdout.strip() or result.stderr.strip()
    print(f"Output: {output[:150]}{'...' if len(output) > 150 else ''}")
    return output

def get_balance():
    """Get agent balance"""
    try:
        output = cmd(f"swechaind query bank balances {AGENT_NAME} --output json")
        data = json.loads(output)
        if "balances" in data and data["balances"]:
            for coin in data["balances"]:
                if coin.get("denom") == "stake":
                    return int(coin.get("amount", "0"))
        return 0
    except:
        return 0

def get_data():
    """Collect blockchain data"""
    data = {"auctions": [], "bids": [], "my_auctions": []}
    
    # Get auctions
    try:
        output = cmd("swechaind query issuemarket list-auction --output json")
        auctions = json.loads(output)
        
        # Handle different response formats
        if isinstance(auctions, dict) and "auctions" in auctions:
            data["auctions"] = auctions["auctions"]
        elif isinstance(auctions, list):
            data["auctions"] = auctions
            
        # Find my auctions
        for auction in data["auctions"]:
            if AGENT_NAME in str(auction):
                data["my_auctions"].append(auction)
    except:
        pass
    
    return data

def decide_action(agent_issues, market_data):
    """Decide next action using LLM"""
    balance = get_balance()
    
    prompt = f"""
You are a blockchain agent making decisions. Current state:
- My balance: {balance} stake
- My issues: {agent_issues[:2] if agent_issues else []}
- Active auctions: {len(market_data['auctions'])}
- My auctions: {len(market_data['my_auctions'])}

Choose ONE action:
1. CREATE: Make an auction from my issues
   swechaind tx issuemarket create-auction ISSUE-ID "DESCRIPTION" open "" --from {AGENT_NAME} --yes
   
2. BID: Bid on an auction (only if I have enough balance)
   swechaind tx issuemarket create-bid AUCTION-ID 0 {AGENT_ADDRESS} AMOUNT "MESSAGE" --from {AGENT_NAME} --yes
   
3. CLOSE: Close my auction if I created one
   swechaind tx issuemarket update-auction AUCTION-ID ISSUE-ID "DESCRIPTION" closed "" --from {AGENT_NAME} --yes
   
4. CHECK: View auctions or balances
   swechaind query issuemarket list-auction --output json

Return only the complete command to execute.
"""
    try:
        prediction = dspy.Predict(lambda x: x)(prompt)
        return prediction.strip()
    except:
        # Fallback commands
        if balance < 500:
            return f"swechaind query bank balances {AGENT_NAME}"
        elif market_data["my_auctions"]:
            auction = market_data["my_auctions"][0]
            auction_id = auction.get("id", "0")
            desc = auction.get("description", "Issue").replace('"', '\\"')
            issue_id = auction.get("issueId", "ISSUE-1") 
            return f'swechaind tx issuemarket update-auction {auction_id} {issue_id} "{desc}" closed "" --from {AGENT_NAME} --yes'
        elif agent_issues and random.random() > 0.5:
            issue = random.choice(agent_issues)
            issue_id = f"ISSUE-{random.randint(100, 999)}"
            desc = issue["desc"].replace('"', '\\"')
            return f'swechaind tx issuemarket create-auction {issue_id} "{desc}" open "" --from {AGENT_NAME} --yes'
        else:
            return "swechaind query issuemarket list-auction --output json"

def main():
    if len(sys.argv) < 2:
        print("Usage: ./agent.py <data_file.json> [agent_name] [agent_address] [iterations]")
        sys.exit(1)
    
    # Load agent data
    agent_data = json.load(open(sys.argv[1]))
    issues = agent_data.get("issues", [])
    iterations = int(sys.argv[4]) if len(sys.argv) > 4 else 10
    
    print(f"Agent {AGENT_NAME} started with {len(issues)} issues")
    
    for i in range(iterations):
        print(f"\n=== ITERATION {i+1}/{iterations} ===")
        market_data = get_data()
        command = decide_action(issues, market_data)
        cmd(command)
        time.sleep(0.5)

if __name__ == "__main__":
    main()