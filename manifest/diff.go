package manifest

import (
	"fmt"
	"slices"
	"strings"
)

// Result is what Diff found between 2 manifests. Both lists are sorted, so 2
// runs on the same manifests print the same lines in the same order.
type Result struct {
	// Breaking holds 1 line for each breaking change: a removed operation,
	// tool or event, or a changed permission, effect, selector or topic of an
	// existing one, or a changed app.
	Breaking []string
	// Additions holds 1 line for each addition. An addition is not a breaking
	// change; `hearth manifest diff` prints it with --verbose only.
	Additions []string
}

// Diff compares 2 manifests by key: an operation by its name, a tool by its
// name, and an event by its type. It reports every change of the old manifest
// that breaks a consumer of the old contract, and every addition.
func Diff(old, next *Manifest) Result {
	var r Result
	if old.App != next.App {
		// A different app keeps nothing of the old contract: every operation,
		// tool and event of the old app is gone.
		r.Breaking = append(r.Breaking, fmt.Sprintf("app changed from %q to %q", old.App, next.App))
		for _, op := range old.Operations {
			r.removedOperation(op)
		}
		for _, event := range old.Events {
			r.Breaking = append(r.Breaking, "event "+event.Type+" removed")
		}
		for _, op := range next.Operations {
			r.addedOperation(op)
		}
		for _, event := range next.Events {
			r.Additions = append(r.Additions, "event "+event.Type+" added")
		}
		r.sortLines()
		return r
	}

	oldOps := operationsByName(old.Operations)
	nextOps := operationsByName(next.Operations)
	for _, op := range old.Operations {
		n, ok := nextOps[op.Name]
		if !ok {
			r.removedOperation(op)
			continue
		}
		if op.Permission != n.Permission {
			r.Breaking = append(r.Breaking, fmt.Sprintf("operation %s: permission changed from %q to %q", op.Name, op.Permission, n.Permission))
		}
		if op.Effect != n.Effect {
			r.Breaking = append(r.Breaking, fmt.Sprintf("operation %s: effect changed from %q to %q", op.Name, op.Effect, n.Effect))
		}
		if before, after := selectorPairs(op.Selectors), selectorPairs(n.Selectors); !slices.Equal(before, after) {
			r.Breaking = append(r.Breaking, fmt.Sprintf("operation %s: selectors changed from [%s] to [%s]", op.Name, strings.Join(before, ", "), strings.Join(after, ", ")))
		}
		r.compareTools(op, n)
	}
	for _, op := range next.Operations {
		if _, ok := oldOps[op.Name]; !ok {
			r.addedOperation(op)
		}
	}

	oldEvents := eventsByType(old.Events)
	nextEvents := eventsByType(next.Events)
	for _, event := range old.Events {
		n, ok := nextEvents[event.Type]
		if !ok {
			r.Breaking = append(r.Breaking, "event "+event.Type+" removed")
			continue
		}
		if event.ReadPermission != n.ReadPermission {
			r.Breaking = append(r.Breaking, fmt.Sprintf("event %s: read_permission changed from %q to %q", event.Type, event.ReadPermission, n.ReadPermission))
		}
		if event.Selector != n.Selector {
			r.Breaking = append(r.Breaking, fmt.Sprintf("event %s: selector changed from %q to %q", event.Type, event.Selector, n.Selector))
		}
		if event.Topic != n.Topic {
			r.Breaking = append(r.Breaking, fmt.Sprintf("event %s: topic changed from %q to %q", event.Type, event.Topic, n.Topic))
		}
	}
	for _, event := range next.Events {
		if _, ok := oldEvents[event.Type]; !ok {
			r.Additions = append(r.Additions, "event "+event.Type+" added")
		}
	}

	r.sortLines()
	return r
}

// compareTools records the tool changes of 1 operation that exists in both
// manifests: a tool that the RPC no longer exposes is removed, and a different
// tool name is a removed tool beside an added 1.
func (r *Result) compareTools(old, next Operation) {
	switch {
	case old.Tool == next.Tool:
	case old.Tool != "":
		r.Breaking = append(r.Breaking, "tool "+old.Tool+" removed")
		if next.Tool != "" {
			r.Additions = append(r.Additions, "tool "+next.Tool+" added")
		}
	case next.Tool != "":
		r.Additions = append(r.Additions, "tool "+next.Tool+" added")
	}
}

// removedOperation records the removal of 1 operation, and of its tool.
func (r *Result) removedOperation(op Operation) {
	r.Breaking = append(r.Breaking, "operation "+op.Name+" removed")
	if op.Tool != "" {
		r.Breaking = append(r.Breaking, "tool "+op.Tool+" removed")
	}
}

// addedOperation records the addition of 1 operation, and of its tool.
func (r *Result) addedOperation(op Operation) {
	r.Additions = append(r.Additions, "operation "+op.Name+" added")
	if op.Tool != "" {
		r.Additions = append(r.Additions, "tool "+op.Tool+" added")
	}
}

// sortLines puts both lists in their printed order.
func (r *Result) sortLines() {
	slices.Sort(r.Breaking)
	slices.Sort(r.Additions)
}

// operationsByName indexes operations by their name.
func operationsByName(ops []Operation) map[string]Operation {
	out := make(map[string]Operation, len(ops))
	for _, op := range ops {
		out[op.Name] = op
	}
	return out
}

// eventsByType indexes events by their type.
func eventsByType(events []Event) map[string]Event {
	out := make(map[string]Event, len(events))
	for _, event := range events {
		out[event.Type] = event
	}
	return out
}

// selectorPairs returns the set of selectors as a sorted list of
// "<field>=<selector>" pairs. The field is part of the contract: the generated
// permission check reads the selector's value from that request field, so a
// selector that moves to another field is a breaking change.
func selectorPairs(selectors []Selector) []string {
	out := make([]string, 0, len(selectors))
	for _, s := range selectors {
		pair := s.Field + "=" + s.Selector
		if !slices.Contains(out, pair) {
			out = append(out, pair)
		}
	}
	slices.Sort(out)
	return out
}
