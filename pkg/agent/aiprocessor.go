// Package agent provides the agent functionality for the CROWler.
package agent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	cmn "github.com/pzaino/thecrowler/pkg/common"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// AIAgentProcessor handles AI-based decision-making for Agents.
type AIAgentProcessor struct {
	learning *LearningSystem
}

// NewAIAgentProcessor initializes the AI processor with learning memory.
func NewAIAgentProcessor(learning *LearningSystem) *AIAgentProcessor {
	return &AIAgentProcessor{learning: learning}
}

// ProcessAIRequest generates AI output based on input, memory, and parameters.
func (a *AIAgentProcessor) ProcessAIRequest(agentName, eventType, prompt string, params map[string]interface{}) (map[string]interface{}, error) {
	// Retrieve past memory before generating response
	previousMemory, _ := a.learning.Recall(agentName, eventType)

	// Modify prompt to include memory
	if previousMemory != nil {
		memSummary := formatMemorySummary(previousMemory)
		prompt = fmt.Sprintf("Previous context:\n%s\n\nNew request: %s", memSummary, prompt)
	}

	// Generate AI request
	requestBody := map[string]interface{}{"prompt": prompt}
	for _, key := range []string{"temperature", "max_tokens", "top_p", "presence_penalty", "frequency_penalty"} {
		if val, exists := params[key]; exists {
			requestBody[key] = val
		}
	}

	// Validate and resolve URL
	url, ok := params["url"].(string)
	if !ok || !cmn.IsURLValid(url) {
		return nil, errors.New("invalid or missing AI URL")
	}

	// Call AI
	response, err := cmn.GenericAPIRequest(map[string]string{
		"url":    url,
		"body":   string(cmn.ConvertMapToJSON(requestBody)),
		"method": "POST",
	})
	if err != nil {
		return nil, fmt.Errorf("AI request failed: %v", err)
	}

	// Parse AI response
	responseMap, err := cmn.JSONStrToMap(response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AI response: %v", err)
	}

	// Store AI response in memory
	_ = a.learning.Learn(agentName, eventType, responseMap)

	return responseMap, nil
}

// formatMemorySummary converts previous memory into structured text.
func formatMemorySummary(memory map[string]interface{}) string {
	// Convert map to sorted JSON for consistency
	jsonData, _ := json.MarshalIndent(memory, "", "  ")
	return string(jsonData)
}

// retrieveAIModelDetails returns AI model details for the known AI models.
func retrieveAIModelDetails(AIModelName string) map[string]interface{} {
	// Define AI model details
	aiModels := map[string]map[string]interface{}{
		"gpt3": {
			"temperature":       "0.5",
			"max_tokens":        "100",
			"top_p":             "1.0",
			"presence_penalty":  "0.0",
			"frequency_penalty": "0.0",
		},
	}

	// Retrieve AI model details
	if modelDetails, exists := aiModels[AIModelName]; exists {
		return modelDetails
	}

	return nil
}

// CallAIModel calls the AI model with the specified parameters.
func (a *AIAgentProcessor) CallAIModel(agentName, eventType, prompt string, modelDetails map[string]interface{}) (map[string]interface{}, error) {
	// Validate model details
	if modelDetails == nil {
		// use default model
		modelDetails = retrieveAIModelDetails("gpt3")
		if modelDetails == nil {
			return nil, errors.New("unknown AI model")
		}
	}
	// Call AI
	response, err := a.ProcessAIRequest(agentName, eventType, prompt, modelDetails)
	if err != nil {
		return nil, fmt.Errorf("AI request failed: %v", err)
	}
	// Return AI response
	return response, nil
}

// LearningSystem manages agent memory
type LearningSystem struct {
	db *cdb.Handler
}

// NewLearningSystem creates a new learning system
func NewLearningSystem(db *cdb.Handler) *LearningSystem {
	return &LearningSystem{db: db}
}

// Learn stores an action result into agent memory
func (ls *LearningSystem) Learn(agentName, eventType string, memoryData map[string]interface{}) error {
	memoryJSON, err := json.Marshal(memoryData)
	if err != nil {
		return fmt.Errorf("failed to serialize memory: %v", err)
	}

	query := `
		INSERT INTO AgentMemory (id, agent_name, event_type, details, created_at, last_updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (agent_name, event_type) DO UPDATE
		SET details = EXCLUDED.details, last_updated_at = NOW();
	`

	_, err = (*ls.db).ExecuteQuery(query, uuid.New().String(), agentName, eventType, string(memoryJSON))
	if err != nil {
		return fmt.Errorf("failed to store agent memory: %v", err)
	}
	return nil
}

// Recall retrieves agent memory by name and event type
func (ls *LearningSystem) Recall(agentName, eventType string) (map[string]interface{}, error) {
	query := `
		SELECT details FROM AgentMemory
		WHERE agent_name=$1 AND event_type=$2 AND deleted_at IS NULL
		ORDER BY last_updated_at DESC LIMIT 1;
	`

	var memoryJSON string
	err := (*ls.db).QueryRow(query, agentName, eventType).Scan(&memoryJSON)
	if err == sql.ErrNoRows {
		return nil, nil // Return empty memory instead of error
	} else if err != nil {
		return nil, fmt.Errorf("failed to retrieve memory: %v", err)
	}

	var memory map[string]interface{}
	err = json.Unmarshal([]byte(memoryJSON), &memory)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize memory: %v", err)
	}

	return memory, nil
}

// Forget removes agent memory (soft delete)
func (ls *LearningSystem) Forget(agentName, eventType string) error {
	query := `
		UPDATE AgentMemory
		SET deleted_at = NOW()
		WHERE agent_name=$1 AND event_type=$2;
	`

	_, err := (*ls.db).Exec(query, agentName, eventType)
	if err != nil {
		return fmt.Errorf("failed to soft delete memory: %v", err)
	}
	return nil
}

// ListMemories retrieves all memories for an agent
func (ls *LearningSystem) ListMemories(agentName string, limit int) ([]map[string]interface{}, error) {
	query := `
		SELECT details FROM AgentMemory
		WHERE agent_name=$1 AND deleted_at IS NULL
		ORDER BY created_at DESC LIMIT $2;
	`

	rows, err := (*ls.db).ExecuteQuery(query, agentName, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query memories: %v", err)
	}
	defer rows.Close() // nolint: errcheck // we can ignore this error

	var memories []map[string]interface{}
	for rows.Next() {
		var memoryJSON string
		if err := rows.Scan(&memoryJSON); err != nil {
			return nil, fmt.Errorf("failed to scan memory row: %v", err)
		}

		var memory map[string]interface{}
		if err := json.Unmarshal([]byte(memoryJSON), &memory); err != nil {
			return nil, fmt.Errorf("failed to deserialize memory: %v", err)
		}

		memories = append(memories, memory)
	}

	return memories, nil
}
