package usage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These bytes are the pre-migration, session-scoped key contract. Attribution
// and identity_version must never become key inputs (ga-rea483 I3, I5).
func TestUsageIdentityMigrationKeyGoldens(t *testing.T) {
	const modelKey = "cb5128b5997a1763402786874e672cae60a1731b82b85b2388a580f2eba66cd0"
	const computeKey = "03e2dbda324de99db19ed6f86129195ea14acd9571eff9aead3687d6e3829868"
	const otherSessionKey = "58a9a4e88861f8681af83dfca6986cd139d48c106267097fcb809535fcdcb8f1"
	if got := ModelIdempotencyKey("session-1", "total:42"); got != modelKey {
		t.Fatalf("model key = %q, want frozen session key %q", got, modelKey)
	}
	if got := ComputeIdempotencyKey("session-1", "session-1", "2026-06-15T10:00:00Z"); got != computeKey {
		t.Fatalf("compute key = %q, want frozen session key %q", got, computeKey)
	}
	if got := ModelIdempotencyKey("session-2", "total:42"); got != otherSessionKey || got == modelKey {
		t.Fatalf("the same codex total in another session must survive: key = %q", got)
	}
}

func TestUsageIdentityMigrationLegacyFactsStayReadableWithoutRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	// The compute record has the pre-session_id shape found in the historical
	// local log. Keys are stored bytes, rather than recomputed by the fixture.
	const legacy = `{"kind":"model","run_id":"session-1","session_id":"session-1","upstream_req_id":"total:42","idempotency_key":"cb5128b5997a1763402786874e672cae60a1731b82b85b2388a580f2eba66cd0","input_tokens":42}` + "\n" +
		`{"kind":"compute","run_id":"session-1","upstream_req_id":"session-1:2026-06-15T10:00:00Z","idempotency_key":"03e2dbda324de99db19ed6f86129195ea14acd9571eff9aead3687d6e3829868","wall_seconds":90}` + "\n"
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	facts, warnings, err := ReadFacts(path)
	if err != nil || len(warnings) != 0 || len(facts) != 2 {
		t.Fatalf("read legacy facts: count=%d warnings=%v err=%v", len(facts), warnings, err)
	}
	if got := ModelIdempotencyKey(facts[0].RunID, facts[0].UpstreamReqID); got != facts[0].IdempotencyKey {
		t.Fatalf("stored model key %q no longer recomputes as %q", facts[0].IdempotencyKey, got)
	}
	if got := ComputeIdempotencyKey(facts[1].RunID, facts[1].RunID, "2026-06-15T10:00:00Z"); got != facts[1].IdempotencyKey {
		t.Fatalf("stored compute key %q no longer recomputes as %q", facts[1].IdempotencyKey, got)
	}
	if facts[1].SessionID != "" || facts[1].RunID != "session-1" {
		t.Fatalf("legacy missing session_id must remain readable at its real session granularity: %+v", facts[1])
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != legacy {
		t.Fatalf("reading history must not rewrite it: err=%v bytes=%q", err, got)
	}
}

// I5 is a characterization guard: codex total:N is transcript-local, so two
// sessions in one real run have two billable invocations even for the same N.
func TestUsageIdentityMigrationSameCodexTotalInTwoSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	const stream = `{"kind":"model","run_id":"session-1","session_id":"session-1","run_root_id":"run-A","upstream_req_id":"total:42","idempotency_key":"cb5128b5997a1763402786874e672cae60a1731b82b85b2388a580f2eba66cd0","input_tokens":42,"cost_usd_estimate":0.01}` + "\n" +
		`{"kind":"model","run_id":"session-2","session_id":"session-2","run_root_id":"run-A","upstream_req_id":"total:42","idempotency_key":"58a9a4e88861f8681af83dfca6986cd139d48c106267097fcb809535fcdcb8f1","input_tokens":42,"cost_usd_estimate":0.01}` + "\n"
	if err := os.WriteFile(path, []byte(stream), 0o644); err != nil {
		t.Fatal(err)
	}
	facts, warnings, err := ReadFacts(path)
	if err != nil || len(warnings) != 0 || len(facts) != 2 {
		t.Fatalf("two sessions must remain distinct: facts=%+v warnings=%v err=%v", facts, warnings, err)
	}
	if facts[0].IdempotencyKey == facts[1].IdempotencyKey || facts[0].InputTokens+facts[1].InputTokens != 84 || facts[0].CostUSDEstimate+facts[1].CostUSDEstimate != 0.02 {
		t.Fatalf("cross-session total:N collision lost real cost: %+v", facts)
	}
}

func TestUsageIdentityMigrationReplayFirstOccurrenceWins(t *testing.T) {
	const legacy = `{"kind":"model","run_id":"session-1","session_id":"session-1","idempotency_key":"same","input_tokens":10,"cost_usd_estimate":0.02}`
	const revisionOne = `{"kind":"model","run_id":"session-1","session_id":"session-1","run_root_id":"run-A","identity_version":1,"idempotency_key":"same","input_tokens":99,"unpriced":true}`
	for _, tc := range []struct {
		name       string
		lines      []string
		wantTokens int
		wantRoot   bool
	}{
		{"legacy-then-revision-one", []string{legacy, revisionOne}, 10, false},
		{"revision-one-then-legacy", []string{revisionOne, legacy}, 99, true},
		{"revision-one-twice", []string{revisionOne, revisionOne}, 99, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "usage.jsonl")
			if err := os.WriteFile(path, []byte(strings.Join(tc.lines, "\n")+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			facts, warnings, err := ReadFacts(path)
			if err != nil || len(warnings) != 0 || len(facts) != 1 {
				t.Fatalf("replay read: facts=%+v warnings=%v err=%v", facts, warnings, err)
			}
			if facts[0].InputTokens != tc.wantTokens {
				t.Fatalf("first occurrence lost: tokens=%d want %d", facts[0].InputTokens, tc.wantTokens)
			}
			encoded, err := json.Marshal(facts[0])
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(encoded, &wire); err != nil {
				t.Fatal(err)
			}
			_, hasRoot := wire["run_root_id"]
			if hasRoot != tc.wantRoot {
				t.Fatalf("first occurrence attribution lost: wire=%s want run_root_id present=%v", encoded, tc.wantRoot)
			}
		})
	}
}

func TestUsageIdentityMigrationComputeReplayKeepsOneWholeInterval(t *testing.T) {
	const legacy = `{"kind":"compute","run_id":"session-1","idempotency_key":"interval","wall_seconds":90}`
	const revisionOne = `{"kind":"compute","run_id":"session-1","session_id":"session-1","identity_version":1,"idempotency_key":"interval","wall_seconds":95}`
	for _, tc := range []struct {
		name     string
		lines    []string
		wantWall float64
	}{
		{"legacy-then-revision-one", []string{legacy, revisionOne}, 90},
		{"revision-one-then-legacy", []string{revisionOne, legacy}, 95},
		{"revision-one-twice", []string{revisionOne, revisionOne}, 95},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "usage.jsonl")
			if err := os.WriteFile(path, []byte(strings.Join(tc.lines, "\n")+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			facts, warnings, err := ReadFacts(path)
			if err != nil || len(warnings) != 0 || len(facts) != 1 || facts[0].WallSeconds != tc.wantWall {
				t.Fatalf("compute replay must retain first whole interval once: facts=%+v warnings=%v err=%v", facts, warnings, err)
			}
		})
	}
}

func TestUsageIdentityMigrationExecSinkKeepsAdditiveWire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facts.jsonl")
	sink := NewExecSink(writeSinkScript(t, "cat >> "+path))
	// Decode through Fact first: the exec sink must forward the new fields
	// after a normal typed emitter constructs the record.
	var model Fact
	if err := json.Unmarshal([]byte(`{"kind":"model","run_id":"session-1","session_id":"session-1","run_root_id":"run-A","step_id":"step-2","formula_name":"review","identity_version":1,"idempotency_key":"model-key","input_tokens":13}`), &model); err != nil {
		t.Fatal(err)
	}
	if err := sink.Record(context.Background(), model); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "\n") || strings.Count(string(data), "\n") != 1 {
		t.Fatalf("exec sink must send one newline-terminated JSON fact: %q", data)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{"run_id": "session-1", "session_id": "session-1", "run_root_id": "run-A", "step_id": "step-2", "formula_name": "review", "identity_version": float64(1)} {
		if wire[key] != want {
			t.Errorf("exec sink %s = %v, want %v; wire=%s", key, wire[key], want, data)
		}
	}
	// A pre-migration decoder must ignore the additive fields and retain its
	// existing session grouping, kind, and dedup key (I11).
	type oldFact struct {
		RunID          string `json:"run_id"`
		SessionID      string `json:"session_id"`
		Kind           Kind   `json:"kind"`
		IdempotencyKey string `json:"idempotency_key"`
		InputTokens    int    `json:"input_tokens"`
		WallSeconds    int    `json:"wall_seconds"`
	}
	var old oldFact
	if err := json.Unmarshal(data, &old); err != nil {
		t.Fatalf("old reader rejected revision-one fact: %v", err)
	}
	if old.RunID != "session-1" || old.SessionID != "session-1" || old.Kind != KindModel || old.IdempotencyKey != "model-key" {
		t.Fatalf("old reader's grouping or dedup changed: %+v", old)
	}

	// An old consumer sees the same session groups when historical rows and
	// revision-one rows share its input stream. It must not infer a run from
	// the new attribution fields it does not know about.
	const legacy = `{"kind":"model","run_id":"session-1","session_id":"session-1","idempotency_key":"old-model","input_tokens":7}` + "\n" +
		`{"kind":"compute","run_id":"session-old","idempotency_key":"old-wall","wall_seconds":12}` + "\n"
	stream := legacy + string(data)
	modelBySession := map[string]int{}
	wallBySession := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(stream), "\n") {
		var fact oldFact
		if err := json.Unmarshal([]byte(line), &fact); err != nil {
			t.Fatalf("old reader rejected mixed stream: %v", err)
		}
		switch fact.Kind {
		case KindModel:
			modelBySession[fact.RunID] += fact.InputTokens
		case KindCompute:
			wallBySession[fact.RunID] += fact.WallSeconds
		}
	}
	if modelBySession["session-1"] != 20 || wallBySession["session-old"] != 12 || len(modelBySession) != 1 || len(wallBySession) != 1 {
		t.Fatalf("legacy grouping changed on mixed stream: models=%v wall=%v", modelBySession, wallBySession)
	}
}
