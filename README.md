# SWEChainAgent [Draft] 

# About

A wrapper for SWE-Agent that augments it to operate in chain environments.

# Usage


```
./agent.py alice.json alice cosmos1w4aglj3z0ls0js6l7hlk9tw0hgrtpqg7alnuyy
```


```
./agent.py bob.json bob cosmos1fgs3u5hvkrh50y7nphrqyjur27jaahh4h3c86w
```


# Specification 

1. Python script for autonomous interaction with Swechaind blockchain.
2. Uses command-line interface "swechaind" to query and execute blockchain operations.
3. Maintains and dynamically updates objectives based on blockchain feedback.
4. Learns available commands through help messages and error responses.
5. Implements a three-phase cycle: feedback gathering, reflection, and action execution.
6. Adapts to incorrect commands by learning proper syntax from error messages.
7. Creates auctions and places bids based on market analysis.
8. Handles JSON parsing with error recovery to prevent crashes.
9. Executes in a continuous loop with configurable delay between iterations.
10. Operates without human intervention after initial startup.



# System Prompt

swechaind query bank balances {agent_name}
swechaind tx issuemarket create-auction "BUG-123" "Fix critical security vulnerability" "open" "" --from {agent_name} --yes --output json

swechaind tx issuemarket create-bid "0" "0" {agent_address} "5000" "Will fix in 7 days" --from {agent_name} --yes --output json 

swechaind query issuemarket list-bid --output json | jq '.bid | .[] | select(.auctionId == "1")'

swechaind tx issuemarket update-auction 0 "BUG-123" "Fix critical security vulnerability" "closed" "" --from {agent_name} --yes --output json

swechaind query issuemarket get-auction 0 --output json

swechaind tx bank send alice $BOB 4000stake --from alice --yes

swechaind query bank balances {agent_name}
