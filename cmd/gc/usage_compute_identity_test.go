package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/usage"
)

// I8: run-chain keys on the SESSION bead must not turn an awake interval into
// one run's wall time. The interval can span several actual executions.
func TestComputeFactIsAlwaysSessionScopedAcrossRunMetadata(t *testing.T) {
	start := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		key  string
	}{
		{"workflow", "workflow_id"},
		{"molecule", "molecule_id"},
		{"root-bead", "gc.root_bead_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := beads.NewMemStore()
			b, err := store.Create(beads.Bead{Title: "session", Metadata: map[string]string{
				"awake_started_at": start.Format(time.RFC3339),
				"slept_at":         start.Add(90 * time.Second).Format(time.RFC3339),
				tc.key:             "run-A",
			}})
			if err != nil {
				t.Fatal(err)
			}
			sink := &captureSink{}
			if !emitComputeFactForBead(context.Background(), sink, store, b, "local", "city", start.Add(95*time.Second), nil, true) {
				t.Fatal("spanning interval should emit one fact")
			}
			if len(sink.facts) != 1 {
				t.Fatalf("spanning interval emitted %d facts, want one", len(sink.facts))
			}
			f := sink.facts[0]
			if f.RunID != b.ID || f.SessionID != b.ID || f.WallSeconds != 90 {
				t.Fatalf("compute fact must retain full wall at session granularity: %+v", f)
			}
			if want := usage.ComputeIdempotencyKey(b.ID, b.ID, start.Format(time.RFC3339)); f.IdempotencyKey != want {
				t.Fatalf("run attribution changed interval key: %q, want %q", f.IdempotencyKey, want)
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
				t.Fatalf("new compute fact needs revision marker: %s", data)
			}
			for _, key := range []string{"run_root_id", "step_id", "formula_name"} {
				if _, ok := wire[key]; ok {
					t.Errorf("compute fact must not carry %s: %s", key, data)
				}
			}
		})
	}
}
