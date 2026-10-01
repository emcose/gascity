package worker

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/sessionlog"
	"github.com/gastownhall/gascity/internal/usage"
)

// I3/I8: session metadata is never evidence of a run. The constructor must
// retain the session key even when old session metadata contains chain keys.
func TestModelUsageFactKeepsSessionKeyAcrossRunMetadata(t *testing.T) {
	u := sessionlog.TailUsage{EntryUUID: "entry-1", MessageID: "msg-1", InputTokens: 7}
	f := modelUsageFact(u, map[string]string{
		"workflow_id":     "workflow-A",
		"molecule_id":     "molecule-B",
		"gc.root_bead_id": "root-C",
		"gc.step_id":      "session-step-is-not-acting-step",
	}, "session-1", "session-1", "worker-1", "claude", 0.01, true, time.Unix(1, 0))
	if f.RunID != "session-1" || f.SessionID != "session-1" {
		t.Fatalf("model fact changed the legacy session join: run_id=%q session_id=%q", f.RunID, f.SessionID)
	}
	if want := usage.ModelIdempotencyKey("session-1", "msg-1"); f.IdempotencyKey != want {
		t.Fatalf("attribution changed model key: %q, want %q", f.IdempotencyKey, want)
	}
	secondRun := modelUsageFact(u, map[string]string{"molecule_id": "run-B"}, "session-1", "session-1", "worker-1", "claude", 0.01, true, time.Unix(1, 0))
	if secondRun.IdempotencyKey != f.IdempotencyKey {
		t.Fatalf("changing attribution from A to B changed invocation key: %q != %q", secondRun.IdempotencyKey, f.IdempotencyKey)
	}
	noRun := modelUsageFact(u, nil, "session-1", "session-1", "worker-1", "claude", 0.01, true, time.Unix(1, 0))
	if noRun.IdempotencyKey != f.IdempotencyKey {
		t.Fatalf("missing attribution changed invocation key: %q != %q", noRun.IdempotencyKey, f.IdempotencyKey)
	}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["identity_version"] != float64(1) {
		t.Fatalf("new model fact needs revision marker: %s", data)
	}
	if _, ok := wire["run_root_id"]; ok {
		t.Fatalf("session chain keys must not become model attribution: %s", data)
	}
	if _, ok := wire["step_id"]; ok {
		t.Fatalf("session step metadata must not become acting-step attribution: %s", data)
	}
}
