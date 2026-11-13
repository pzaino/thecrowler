package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	agt "github.com/pzaino/thecrowler/pkg/agent"
	"github.com/pzaino/thecrowler/pkg/auth"
	cmn "github.com/pzaino/thecrowler/pkg/common"
)

const maxAgentRunRequestBytes = 64 << 10

type agentRunRequest struct {
	AgentID    string         `json:"agent_id,omitempty"`
	AgentName  string         `json:"agent_name,omitempty"`
	Input      any            `json:"input,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

type agentRunResponse struct {
	Status            string `json:"status"`
	AgentID           string `json:"agent_id"`
	AgentName         string `json:"agent_name"`
	PrincipalUserID   uint64 `json:"principal_user_id"`
	PrincipalUsername string `json:"principal_username"`
	PrincipalIssuer   string `json:"principal_issuer"`
	Result            any    `json:"result,omitempty"`
}

// agentRunHandler is the authenticated user-triggered manual agent entrypoint.
// It derives the principal only from the middleware identity plus a current
// account lookup. Event payloads, manifests, model output and request fields
// can never supply a principal.
func agentRunHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		agentRunError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}
	identity, ok := auth.FromContext(r.Context())
	if !ok {
		agentRunError(w, http.StatusUnauthorized, "authentication required", nil)
		return
	}
	resolved, err := auth.ResolveActiveIdentity(r.Context(), dbHandler, identity)
	if err != nil {
		agentRunError(w, http.StatusUnauthorized, "authentication required", err)
		return
	}
	if !hasEventRunScope(resolved.Scopes) {
		agentRunError(w, http.StatusForbidden, "events operator scope required", nil)
		return
	}
	principal, err := agt.MCPMutationPrincipalFromAuthIdentity(resolved)
	if err != nil {
		agentRunError(w, http.StatusForbidden, "authenticated principal is unavailable", err)
		return
	}

	var request agentRunRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAgentRunRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		agentRunError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if err := requireJSONEOF(decoder); err != nil {
		agentRunError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	request.AgentID = strings.TrimSpace(request.AgentID)
	request.AgentName = strings.TrimSpace(request.AgentName)
	if (request.AgentID == "") == (request.AgentName == "") {
		agentRunError(w, http.StatusBadRequest, "exactly one of agent_id or agent_name is required", nil)
		return
	}

	configMutex.Lock()
	runtimeFlags := config.Agent
	configMutex.Unlock()
	if !runtimeFlags.IdentityEnforcement {
		agentRunError(w, http.StatusForbidden, "agent identity enforcement is required for manual runs", nil)
		return
	}
	if agt.AgentsEngine == nil || agt.AgentsRegistry == nil {
		cmn.DebugMsg(cmn.DbgLvlError, "manual agent run unavailable: engine_nil=%t registry_nil=%t", agt.AgentsEngine == nil, agt.AgentsRegistry == nil)
		agentRunError(w, http.StatusServiceUnavailable, "agent runtime unavailable", nil)
		return
	}
	agentRef := request.AgentID
	if agentRef == "" {
		agentRef = request.AgentName
	}
	def := agentDefinitionForRun(agentRef)
	if def == nil {
		agentRunError(w, http.StatusNotFound, "agent not found", nil)
		return
	}
	if _, ok := def.ManualJobConfig(); !ok {
		agentRunError(w, http.StatusBadRequest, "agent has no manual trigger", nil)
		return
	}

	input := map[string]any{
		"db_handler":       dbHandler,
		"plugins_register": PluginRegister,
		"agent_runtime":    runtimeFlags,
		"source":           "events:agent/run",
	}
	if request.Input != nil {
		input["input"] = request.Input
	}
	if request.Parameters != nil {
		input["parameters"] = request.Parameters
	}
	input = agt.WithMCPMutationPrincipal(input, principal)

	if err := r.Context().Err(); err != nil {
		agentRunError(w, http.StatusRequestTimeout, "request cancelled", err)
		return
	}
	result, err := agt.AgentsEngine.ExecuteManualAgentResult(agentRef, input)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "manual agent execution failed: %v", err)
		agentRunError(w, http.StatusBadGateway, "agent execution failed", err)
		return
	}
	handleErrorAndRespond(w, nil, agentRunResponse{
		Status:            "success",
		AgentID:           def.Identity.AgentID,
		AgentName:         def.Identity.Name,
		PrincipalUserID:   principal.UserID,
		PrincipalUsername: principal.Username,
		PrincipalIssuer:   principal.Issuer,
		Result:            result,
	}, "", 0, http.StatusOK)
}

func agentDefinitionForRun(ref string) *agt.AgentDefinition {
	if agt.AgentsRegistry == nil {
		return nil
	}
	AgentsRegistry := agt.AgentsRegistry.Registry()
	if AgentsRegistry == nil {
		return nil
	}
	if def, ok := AgentsRegistry.GetByID(ref); ok {
		return def
	}
	if def, ok := AgentsRegistry.GetByName(ref); ok {
		return def
	}
	return nil
}

func hasEventRunScope(scopes []string) bool {
	for _, scope := range scopes {
		switch strings.TrimSpace(scope) {
		case "events:*", "events:agent:run":
			return true
		}
	}
	return false
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("request body must contain one JSON object")
		}
		return err
	}
	return nil
}

func agentRunError(w http.ResponseWriter, status int, message string, err error) {
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "manual agent run: %s: %v", message, err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(cmn.StdAPIError{
		ErrCode: status,
		Err:     message,
		Message: message,
	})
}
