package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// TrajectoryEvent represents a single event in an agent's trajectory
type TrajectoryEvent struct {
	Timestamp int64                  `json:"timestamp"`
	EventType string                 `json:"event_type"`
	Content   map[string]interface{} `json:"content"`
	Metadata  map[string]interface{} `json:"metadata"`
}

// TrajectoryLogger manages logging agent trajectories
type TrajectoryLogger struct {
	AgentName string
	BasePath  string
}

// NewTrajectoryLogger creates a new trajectory logger for the specified agent
func NewTrajectoryLogger(agentName string) (*TrajectoryLogger, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %v", err)
	}

	basePath := filepath.Join(homeDir, ".hackspree", agentName, "trajs")

	// Ensure trajectory directory exists
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create trajectory directory: %v", err)
	}

	return &TrajectoryLogger{
		AgentName: agentName,
		BasePath:  basePath,
	}, nil
}

// LogEvent logs an event to the trajectory file
func (tl *TrajectoryLogger) LogEvent(eventType string, content, metadata map[string]interface{}) error {
	event := TrajectoryEvent{
		Timestamp: time.Now().Unix(),
		EventType: eventType,
		Content:   content,
		Metadata:  metadata,
	}

	// Get day start in Unix epoch for filename
	dayEpoch := getDayEpoch()
	filename := fmt.Sprintf("%d.traj", dayEpoch)
	filepath := filepath.Join(tl.BasePath, filename)

	// Convert to JSON
	eventJSON, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal event: %v", err)
	}

	// Append to file
	file, err := os.OpenFile(filepath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open trajectory file: %v", err)
	}
	defer file.Close()

	// Add newline between entries
	if _, err := file.Write(append(eventJSON, '\n')); err != nil {
		return fmt.Errorf("failed to write to trajectory file: %v", err)
	}

	return nil
}

// LogToolCall is a convenience method for logging tool calls
func (tl *TrajectoryLogger) LogToolCall(toolName, arguments string, result string) error {
	content := map[string]interface{}{
		"tool_name": toolName,
		"arguments": arguments,
		"result":    result,
	}

	metadata := map[string]interface{}{
		"agent": tl.AgentName,
	}

	return tl.LogEvent("tool_call", content, metadata)
}

// LogCompletion is a convenience method for logging model completions
func (tl *TrajectoryLogger) LogCompletion(model string, prompt, completion string) error {
	content := map[string]interface{}{
		"model":      model,
		"prompt":     prompt,
		"completion": completion,
	}

	metadata := map[string]interface{}{
		"agent": tl.AgentName,
	}

	return tl.LogEvent("completion", content, metadata)
}

// getDayEpoch returns the Unix epoch timestamp for the start of the current day
func getDayEpoch() int64 {
	now := time.Now()
	year, month, day := now.Date()
	dayStart := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	return dayStart.Unix()
}
