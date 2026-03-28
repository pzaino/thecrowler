// Copyright 2023 Paolo Fabio Zaino, all rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package agent provides the agent functionality for the CROWler.
package agent

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Knetic/govaluate"
)

// DecisionAction makes decisions based on conditions
type DecisionAction struct{}

// Name returns the name of the action
func (d *DecisionAction) Name() string {
	return "Decision"
}

// Execute evaluates conditions and executes steps
func (d *DecisionAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	rval := make(map[string]interface{})
	rval[StrResponse] = nil
	rval[StrConfig] = nil

	// Check if params has a field called config
	config, err := getConfig(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	rval[StrConfig] = config

	// Extract previous step response
	inputRaw, _ := getInput(params)

	condition, ok := params["condition"].(map[string]interface{})
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'condition' parameter"
		return rval, fmt.Errorf("missing 'condition' parameter")
	}

	result, err := evaluateCondition(condition, params, inputRaw)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("failed to evaluate condition: %v", err)
		return rval, err
	}
	var nextStep map[string]interface{}
	// Check if result is a boolean
	resultBool, ok := result.(bool)
	if !ok {
		// result is not a boolean, so it's a set of steps
		nextStep, ok = result.(map[string]interface{})
		if !ok {
			rval[StrStatus] = StatusError
			rval[StrMessage] = "invalid result from condition evaluation"
			return rval, fmt.Errorf("invalid result from condition evaluation")
		}
	} else {
		if resultBool {
			nextStep, ok = condition["on_true"].(map[string]interface{})
			if !ok {
				fmt.Printf("condition: %v\n", condition)
				rval[StrStatus] = StatusError
				rval[StrMessage] = "missing 'on_true' step"
				return rval, fmt.Errorf("missing 'on_true' step")
			}
		} else {
			nextStep, ok = condition["on_false"].(map[string]interface{})
			if !ok {
				fmt.Printf("condition: %v\n", condition)
				rval[StrStatus] = StatusError
				rval[StrMessage] = "missing 'on_false' step"
				return rval, fmt.Errorf("missing 'on_false' step")
			}
		}
	}

	var results map[string]interface{}
	if nextStep != nil {
		target, resolveErr := resolveDelegationTarget(nextStep, inputRaw)
		if resolveErr != nil {
			emitDelegationAudit(params, "", auditOutcomeDenied, resolveErr.Error())
			rval[StrStatus] = StatusError
			rval[StrMessage] = resolveErr.Error()
			return rval, resolveErr
		}

		callee, _, resolveAgentErr := AgentsEngine.resolveAgentDefinitionByTarget(target)
		if resolveAgentErr != nil {
			targetRef := target.AgentID
			if strings.TrimSpace(targetRef) == "" {
				targetRef = target.AgentName
			}
			emitDelegationAudit(params, targetRef, auditOutcomeDenied, resolveAgentErr.Error())
			rval[StrStatus] = StatusError
			rval[StrMessage] = resolveAgentErr.Error()
			return rval, resolveAgentErr
		}

		configMap, _ := params[StrConfig].(map[string]interface{})
		flags := runtimeFlagsFromConfig(configMap)
		if flags.IdentityEnforcement {
			caller, hasCaller := parseRuntimeIdentity(params)
			if !hasCaller {
				err := fmt.Errorf("delegation denied: missing caller identity")
				emitDelegationAudit(params, callee.Identity.AgentID, auditOutcomeDenied, err.Error())
				rval[StrStatus] = StatusError
				rval[StrMessage] = err.Error()
				return rval, err
			}
			if policyErr := delegationPolicyCheck(caller, callee.Identity); policyErr != nil {
				emitDelegationAudit(params, callee.Identity.AgentID, auditOutcomeDenied, policyErr.Error())
				rval[StrStatus] = StatusError
				rval[StrMessage] = policyErr.Error()
				return rval, policyErr
			}
			graph := getDelegationGraph(params)
			callerNode := delegationNodeKey(caller)
			calleeNode := delegationNodeKey(callee.Identity)
			if len(graph.Path) == 0 {
				graph.Path = append(graph.Path, callerNode)
			}
			if cycleErr := detectDelegationCycle(graph, calleeNode); cycleErr != nil {
				emitDelegationAudit(params, calleeNode, auditOutcomeDenied, cycleErr.Error())
				rval[StrStatus] = StatusError
				rval[StrMessage] = cycleErr.Error()
				return rval, cycleErr
			}
			graph.Edges = append(graph.Edges, callerNode+"->"+calleeNode)
			previousPath := append([]string(nil), graph.Path...)
			graph.Path = append(graph.Path, calleeNode)
			setDelegationGraph(params, graph)
			defer func() {
				graph.Path = previousPath
				setDelegationGraph(params, graph)
			}()
		}

		agentRef := target.AgentID
		if strings.TrimSpace(agentRef) == "" {
			agentRef = target.AgentName
		}

		delegationCtx := map[string]any{}
		if configMap != nil {
			for k, v := range configMap {
				delegationCtx[k] = v
			}
		}
		if inputVal, ok := inputRaw[StrRequest]; ok {
			delegationCtx[StrRequest] = inputVal
		}
		err = AgentsEngine.ExecuteAgent(agentRef, delegationCtx)
		if err != nil {
			emitDelegationAudit(params, agentRef, auditOutcomeError, err.Error())
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("delegation failed: %v", err)
			return rval, err
		}
		emitDelegationAudit(params, agentRef, auditOutcomeAllowed, "delegation_completed")
	}

	rval[StrResponse] = results
	rval[StrStatus] = StatusSuccess
	rval[StrMessage] = "decision executed successfully"

	return rval, nil
}

func evaluateCondition(condition, params, rawInput map[string]interface{}) (interface{}, error) {
	// Check which condition to evaluate (agents usually support `if` and `switch` type conditions)
	conditionType, _ := condition["condition_type"].(string)
	conditionType = strings.ToLower(strings.TrimSpace(conditionType))

	// Check if the condition is a simple `if` condition
	if conditionType == "if" {
		// Extract the condition to evaluate
		// This should be a string expression like "$response.success == true && ($response.status == 'active' || $response.value > 10)"
		expr, ok := condition["expression"].(string)
		if !ok {
			return false, fmt.Errorf("missing 'expression' in condition")
		}

		// Evaluate the condition
		return evaluateIfCondition(expr, rawInput)
	}

	// Check if the condition is a `switch` condition
	if conditionType == "switch" {
		// Extract the switch condition
		expr, ok := params["expression"].(string)
		if !ok {
			return false, fmt.Errorf("missing 'expression' in condition")
		}

		// Also, check if the switch many cases needs to be resolved
		// This should be an array of cases like {"1": "case1", "2": "case2", "default": "default"}
		rawCases, ok := condition["cases"].(map[string]interface{})
		if !ok {
			return false, fmt.Errorf("missing 'cases' in condition")
		}

		// Evaluate the switch condition
		return evaluateSwitchCondition(expr, rawCases, rawInput)
	}

	return false, fmt.Errorf("unsupported condition type: %s", conditionType)
}

// evaluateIfCondition evaluates a boolean condition based on the given expression and parameters.
func evaluateIfCondition(expression string, rawInput map[string]interface{}) (bool, error) {
	// Check if expr needs to be resolved
	expression = resolveResponseString(rawInput, expression)

	// Wrap string values in single quotes
	expression = wrapStrings(expression)

	parsedExpr, err := govaluate.NewEvaluableExpression(expression)
	if err != nil {
		return false, fmt.Errorf("invalid expression: %s", expression)
	}

	// Step 4: Evaluate the expression
	result, err := parsedExpr.Evaluate(nil) // No need for additional parameters
	if err != nil {
		return false, fmt.Errorf("error evaluating expression: %v", err)
	}

	// Step 5: Ensure the result is a boolean
	booleanResult, ok := result.(bool)
	if !ok {
		return false, fmt.Errorf("expression did not return a boolean: %v", result)
	}

	return booleanResult, nil
}

// wrapStrings ensures string values in the expression are enclosed in single quotes.
func wrapStrings(expression string) string {
	// Regex to match words that are not part of an operator, number, or boolean.
	re := regexp.MustCompile(`(\b[a-zA-Z_][a-zA-Z0-9_]*\b)`)
	return re.ReplaceAllStringFunc(expression, func(match string) string {
		// Avoid modifying operators or boolean values
		lower := strings.ToLower(match)
		if lower == "true" || lower == "false" || lower == "and" || lower == "or" || lower == "not" {
			return match
		}
		// Wrap other words in quotes
		return fmt.Sprintf("'%s'", match)
	})
}

// evaluateSwitchCondition evaluates a switch-like condition based on the given expression and cases.
func evaluateSwitchCondition(expression string, rawCases, rawInput map[string]interface{}) (interface{}, error) {

	// Parse the expression (basic implementation)
	// example expression: "test == test", or just "test"
	parts := strings.Fields(expression)
	var expr interface{}
	var err error
	if len(parts) > 1 {
		// use evaluateIfCondition for comparison
		expr, err = evaluateIfCondition(expression, rawInput)
		if err != nil {
			return false, fmt.Errorf("invalid switch condition: %s", expression)
		}
	} else {
		// Check if expression needs to be resolved
		expr = resolveResponseString(rawInput, expression)
	}

	// Check if cases needs to be resolved
	cases := make(map[string]interface{})
	// Convert rawCases to a map
	for k, v := range rawCases {
		// Check if k needs to be resolved
		k = resolveResponseString(rawInput, k)
		// Check if v needs to be resolved
		// Is V a map?
		if _, ok := v.(map[string]interface{}); ok {
			v = resolveValue(rawInput, v)
		} else {
			// Check if v is a string
			// If it is, resolve it
			v = resolveResponseString(rawInput, v.(string))
		}
		cases[k] = v
	}

	// Look for matching cases in params
	if expr != nil {
		if caseValue, exists := cases[fmt.Sprintf("%v", expr)]; exists {
			// Execute the case
			fmt.Printf("Case %v\n", caseValue)
			return caseValue, nil
		}
	}

	// Fallback to 'default' case if defined
	if _, defaultExists := cases["default"]; defaultExists {
		return cases["default"], nil
	}

	return nil, fmt.Errorf("no matching case found")
}

// compareNumeric performs numeric comparison with a custom comparator function.
func compareNumeric(left interface{}, right string, comparator func(a, b float64) bool) bool {
	leftFloat, ok1 := toFloat(left)
	rightFloat, ok2 := toFloat(right)
	if ok1 && ok2 {
		return comparator(leftFloat, rightFloat)
	}
	return false
}

// toFloat attempts to convert a value to a float64.
func toFloat(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case float64:
		return v, true
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}
