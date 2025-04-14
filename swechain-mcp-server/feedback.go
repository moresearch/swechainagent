package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

const (
	pageLimit    = 50  // Number of items per page
	maxPages     = 100 // Maximum number of pages to fetch
	requestDelay = 500 * time.Millisecond
	swechaindCmd = "/home/maf/go/bin/swechaind"
)

// Result represents the combined output of all commands
type Result struct {
	Keys        []Key        `json:"keys"`
	Auctions    []Auction    `json:"auctions"`
	Bids        []Bid        `json:"bids"`
	DenomOwners []DenomOwner `json:"denom_owners"`
}

// Key represents a key from the `keys list` command
type Key struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// Auction represents an auction from the `list-auction` command
type Auction struct {
	ID          int    `json:"id,string"` // Handle ID as a string in JSON
	Issue       string `json:"issue"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Winner      string `json:"winner"`
	Creator     string `json:"creator"`
}

// Bid represents a bid from the `list-bid` command
type Bid struct {
	AuctionID   int    `json:"auctionId,string"` // Handle AuctionID as a string in JSON
	Amount      string `json:"amount"`
	Description string `json:"description"`
	Creator     string `json:"creator"`
}

// DenomOwner represents a denom owner from the `query bank denom-owners token` command
type DenomOwner struct {
	Address string `json:"address"`
	Balance struct {
		Amount string `json:"amount"`
		Denom  string `json:"denom"`
	} `json:"balance"`
}

func main() {
	// Fetch keys (no pagination)
	keys := fetchKeys()

	// Fetch auctions with pagination
	auctions := fetchPaginatedData("issuemarket", "list-auction", "Auction")

	// Fetch bids with pagination
	bids := fetchPaginatedData("issuemarket", "list-bid", "Bid")

	// Fetch denom owners
	denomOwners := fetchDenomOwners()

	// Combine results into a single structure
	result := Result{
		Keys:        keys,
		Auctions:    parseAuctions(auctions),
		Bids:        parseBids(bids),
		DenomOwners: denomOwners,
	}

	// Convert result to JSON
	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Printf("Error marshaling result: %v\n", err)
		return
	}

	// Print the final JSON output
	fmt.Println(string(resultJSON))
}

// fetchKeys executes the `keys list` command and parses the output
func fetchKeys() []Key {
	// swechaind keys list --keyring-backend test
	cmd := exec.Command(swechaindCmd, "keys", "list", "--keyring-backend", "test", "--output", "json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Error fetching keys: %v\n%s\n", err, string(output))
		return nil
	}

	var keys []Key
	if err := json.Unmarshal(output, &keys); err != nil {
		fmt.Printf("Error parsing keys: %v\n", err)
		return nil
	}
	return keys
}

// fetchPaginatedData fetches paginated data for a given query
func fetchPaginatedData(module, query, dataKey string) []map[string]interface{} {
	var allResults []map[string]interface{}
	offset := 0

	for {
		// Execute the command with pagination parameters
		cmd := exec.Command(
			swechaindCmd,
			"query", module, query,
			"--keyring-backend", "test",
			"--output", "json",
			"--page-offset", strconv.Itoa(offset),
			"--page-limit", strconv.Itoa(pageLimit),
		)
		output, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Printf("Error fetching %s/%s offset %d: %v\n%s\n", module, query, offset, err, string(output))
			break
		}

		// Parse the JSON response
		var responseData map[string]interface{}
		if err := json.Unmarshal(output, &responseData); err != nil {
			fmt.Printf("Error parsing %s/%s offset %d: %v\n", module, query, offset, err)
			break
		}

		// Extract the array of results using the correct key
		results, ok := responseData[dataKey].([]interface{})
		if !ok || len(results) == 0 {
			break // Exit if no more results
		}

		// Convert results to a slice of maps
		for _, result := range results {
			resultMap, ok := result.(map[string]interface{})
			if ok {
				allResults = append(allResults, resultMap)
			}
		}

		// Check if we've reached the maximum number of pages
		if offset >= maxPages*pageLimit {
			fmt.Printf("Reached maximum page limit (%d). Stopping.\n", maxPages)
			break
		}

		// Increment the offset
		offset += pageLimit
		time.Sleep(requestDelay) // Add a delay to avoid overwhelming the API
	}

	return allResults
}

// fetchDenomOwners fetches denom owners using the `query bank denom-owners token` command
func fetchDenomOwners() []DenomOwner {
	cmd := exec.Command(
		swechaindCmd,
		"query", "bank", "denom-owners", "token",
		"--keyring-backend", "test",
		"--output", "json",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Error fetching denom owners: %v\n%s\n", err, string(output))
		return nil
	}

	var responseData map[string]interface{}
	if err := json.Unmarshal(output, &responseData); err != nil {
		fmt.Printf("Error parsing denom owners: %v\n", err)
		return nil
	}

	// Extract the "denom_owners" field
	rawDenomOwners, ok := responseData["denom_owners"].([]interface{})
	if !ok {
		fmt.Printf("Unexpected format for denom owners\n")
		return nil
	}

	// Convert raw data into a slice of DenomOwner structs
	var denomOwners []DenomOwner
	for _, raw := range rawDenomOwners {
		rawMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		address := fmt.Sprintf("%v", rawMap["address"])
		balanceRaw, ok := rawMap["balance"].(map[string]interface{})
		if !ok {
			continue
		}
		amount := fmt.Sprintf("%v", balanceRaw["amount"])
		denom := fmt.Sprintf("%v", balanceRaw["denom"])
		denomOwners = append(denomOwners, DenomOwner{
			Address: address,
			Balance: struct {
				Amount string `json:"amount"`
				Denom  string `json:"denom"`
			}{
				Amount: amount,
				Denom:  denom,
			},
		})
	}

	return denomOwners
}

// parseAuctions converts raw auction data into a slice of Auction structs
func parseAuctions(rawData []map[string]interface{}) []Auction {
	var auctions []Auction
	for _, raw := range rawData {
		idStr, _ := raw["id"].(string)
		id, _ := strconv.Atoi(idStr)
		auction := Auction{
			ID:          id,
			Issue:       fmt.Sprintf("%v", raw["issue"]),
			Description: fmt.Sprintf("%v", raw["description"]),
			Status:      fmt.Sprintf("%v", raw["status"]),
			Winner:      fmt.Sprintf("%v", raw["winner"]),
			Creator:     fmt.Sprintf("%v", raw["creator"]),
		}
		auctions = append(auctions, auction)
	}
	return auctions
}

// parseBids converts raw bid data into a slice of Bid structs
func parseBids(rawData []map[string]interface{}) []Bid {
	var bids []Bid
	for _, raw := range rawData {
		auctionIDStr, _ := raw["auctionId"].(string)
		auctionID, _ := strconv.Atoi(auctionIDStr)
		bid := Bid{
			AuctionID:   auctionID,
			Amount:      fmt.Sprintf("%v", raw["amount"]),
			Description: fmt.Sprintf("%v", raw["description"]),
			Creator:     fmt.Sprintf("%v", raw["creator"]),
		}
		bids = append(bids, bid)
	}
	return bids
}
