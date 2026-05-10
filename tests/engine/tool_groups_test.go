package engine_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func toolCall(id, name string) schema.ToolCall {
	return schema.ToolCall{ID: id, Name: name}
}

func groupedIDs(groups [][]schema.ToolCall) [][]string {
	out := make([][]string, 0, len(groups))
	for _, group := range groups {
		ids := make([]string, 0, len(group))
		for _, call := range group {
			ids = append(ids, call.ID)
		}
		out = append(out, ids)
	}
	return out
}

func policyMap(policies map[string]tools.ExecutionPolicy) func(schema.ToolCall) tools.ExecutionPolicy {
	return func(call schema.ToolCall) tools.ExecutionPolicy {
		return policies[call.Name]
	}
}

func TestPlanToolCallGroupsBatchesConsecutiveParallelCalls(t *testing.T) {
	calls := []schema.ToolCall{
		toolCall("1", "read_file"),
		toolCall("2", "read_file"),
		toolCall("3", "edit_file"),
		toolCall("4", "read_file"),
		toolCall("5", "bash"),
		toolCall("6", "read_file"),
		toolCall("7", "read_file"),
	}
	policyFor := policyMap(map[string]tools.ExecutionPolicy{
		"read_file": {ParallelSafe: true},
	})

	got := groupedIDs(engine.PlanToolCallGroups(calls, policyFor))
	want := [][]string{{"1", "2"}, {"3"}, {"4"}, {"5"}, {"6", "7"}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %#v, want %#v", got, want)
	}
}

func TestPlanToolCallGroupsTreatsNilPolicyLookupAsSerial(t *testing.T) {
	calls := []schema.ToolCall{
		toolCall("1", "read_file"),
		toolCall("2", "read_file"),
	}

	got := groupedIDs(engine.PlanToolCallGroups(calls, nil))
	want := [][]string{{"1"}, {"2"}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %#v, want %#v", got, want)
	}
}

func TestPlanToolCallGroupsDoesNotMutateInput(t *testing.T) {
	calls := []schema.ToolCall{
		toolCall("1", "read_file"),
		toolCall("2", "bash"),
	}
	original := append([]schema.ToolCall(nil), calls...)
	policyFor := policyMap(map[string]tools.ExecutionPolicy{
		"read_file": {ParallelSafe: true},
	})

	_ = engine.PlanToolCallGroups(calls, policyFor)

	if !reflect.DeepEqual(calls, original) {
		t.Fatalf("input mutated: got %#v, want %#v", calls, original)
	}
}

func TestPlanToolCallGroupsDoesNotShareArgumentsMemory(t *testing.T) {
	args := json.RawMessage(`{"path":"a.txt"}`)
	calls := []schema.ToolCall{{ID: "1", Name: "read_file", Arguments: args}}
	policyFor := policyMap(map[string]tools.ExecutionPolicy{
		"read_file": {ParallelSafe: true},
	})

	groups := engine.PlanToolCallGroups(calls, policyFor)
	groups[0][0].Arguments[0] = 'X' // mutate output

	if calls[0].Arguments[0] == 'X' {
		t.Fatal("output group shares Arguments memory with input")
	}
}
