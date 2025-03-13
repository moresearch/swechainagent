#!/usr/bin/env python3
"""Minimal Swechaind Market Agent - 2025-03-13"""
import subprocess, json, time, sys, random, dspy

# Setup
try:
    lm = dspy.LM('ollama/deepseek-r1:1.5b', api_base='http://localhost:11434')
    dspy.configure(lm=lm)
except Exception as e:
    print(f"LLM init error: {e}")

AGENT, ADDRESS, WAIT = sys.argv[2] if len(sys.argv) > 2 else "agent", sys.argv[3] if len(sys.argv) > 3 else "cosmos1default", 0.5

class Action(dspy.Signature):
    data = dspy.InputField()
    role = dspy.OutputField()
    cmd = dspy.OutputField()

def run(cmd):
    """Run command and return output"""
    print(f"$ {cmd}")
    try:
        r = subprocess.run(cmd.split(), capture_output=True, text=True)
        return r.stdout.strip() or r.stderr.strip()
    except Exception as e:
        return str(e)

def get_state():
    """Get current market state"""
    state = {"auctions": [], "bids": []}
    try:
        aucts = run(f"swechaind query issuemarket list-auction --output json")
        if aucts and aucts[0] == '{':
            parsed = json.loads(aucts)
            state["auctions"] = parsed.get("auctions", []) if isinstance(parsed, dict) else parsed
    except: pass
    try:
        bids = run(f"swechaind query issuemarket list-bid --output json")
        if bids and bids[0] == '{':
            parsed = json.loads(bids)
            state["bids"] = parsed.get("bid", []) if isinstance(parsed, dict) else parsed
    except: pass
    return state

def decide(issues, market):
    """Decide action based on market state and agent data"""
    prompt = f"""Based on:
1. Issues: {str(issues)[:100]}
2. Market: {str(market)[:100]}

Choose role (auctioneer/bidder) and command:
- Auctioneer: swechaind tx issuemarket create-auction "ISSUE-ID" "DESC" "open" "" --from {AGENT} --yes
- Bidder: swechaind tx issuemarket create-bid "AUCTION-ID" "0" {ADDRESS} "AMOUNT" "MSG" --from {AGENT} --yes"""
    
    try:
        result = dspy.Predict(Action)(data=prompt)
        return result.role, result.cmd
    except:
        # Fallback logic
        if random.random() > 0.5 and issues:
            issue = random.choice(issues)
            issue_id = f"BUG-{random.randint(100,999)}"
            return "auctioneer", f"swechaind tx issuemarket create-auction \"{issue_id}\" \"{issue['desc']}\" \"open\" \"\" --from {AGENT} --yes"
        auctions = market.get("auctions", [])
        if auctions and isinstance(auctions, list) and len(auctions) > 0:
            auction_id = auctions[0].get("id", "0") if isinstance(auctions[0], dict) else "0"
            return "bidder", f"swechaind tx issuemarket create-bid \"{auction_id}\" \"0\" {ADDRESS} \"500\" \"I can help\" --from {AGENT} --yes"
        return "bidder", f"swechaind query issuemarket list-auction --output json"

def main():
    if len(sys.argv) < 2:
        print("Usage: ./agent.py <data_file.json> [agent_name] [agent_address] [iterations]")
        sys.exit(1)
    
    # Load agent data
    try:
        with open(sys.argv[1], 'r') as f:
            issues = json.load(f).get("issues", [])
    except:
        issues = []
    
    max_iter = int(sys.argv[4]) if len(sys.argv) > 4 and sys.argv[4].isdigit() else 10
    print(f"Agent {AGENT} started with {len(issues)} issues")
    
    for i in range(max_iter):
        print(f"\n=== ITERATION {i+1}/{max_iter} ===")
        market = get_state()
        role, cmd = decide(issues, market)
        print(f"Acting as: {role}")
        output = run(cmd)
        print(f"Output: {output[:100]}..." if len(output) > 100 else f"Output: {output}")
        time.sleep(WAIT)

if __name__ == "__main__":
    main()