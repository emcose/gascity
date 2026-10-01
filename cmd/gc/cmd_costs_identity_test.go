package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// I1/I2/I4/I6: invoke the command over the mixed local stream. A reused
// session owns both executions and the entire spanning awake interval.
func TestCostsCommandMixedIdentitySessionAndRunViews(t *testing.T) {
	city := t.TempDir()
	if err := os.WriteFile(filepath.Join(city, "city.toml"), []byte("[workspace]\nname = \"usage-identity\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(city, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The legacy model fact has no revision marker or run attribution. The
	// final compute fact predates session_id; its run_id was still the session.
	// The last line replays A after a cursor race and must not add 900 tokens.
	const facts = `{"kind":"model","run_id":"session-1","session_id":"session-1","run_root_id":"run-A","identity_version":1,"idempotency_key":"a","input_tokens":100,"cost_usd_estimate":0.01}` + "\n" +
		`{"kind":"model","run_id":"session-1","session_id":"session-1","run_root_id":"run-B","identity_version":1,"idempotency_key":"b","input_tokens":40,"unpriced":true}` + "\n" +
		`{"kind":"model","run_id":"session-1","session_id":"session-1","idempotency_key":"c","input_tokens":7,"cost_usd_estimate":0.005}` + "\n" +
		`{"kind":"compute","run_id":"session-1","session_id":"session-1","identity_version":1,"idempotency_key":"wall-1","wall_seconds":90}` + "\n" +
		`{"kind":"compute","run_id":"session-old","idempotency_key":"wall-old","wall_seconds":10}` + "\n" +
		`{"kind":"model","run_id":"session-1","session_id":"session-1","run_root_id":"run-A","identity_version":1,"idempotency_key":"a","input_tokens":900}` + "\n"
	if err := os.WriteFile(filepath.Join(city, ".gc", "usage.jsonl"), []byte(facts), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GC_CITY_PATH", city)
	priorCityFlag, priorRigFlag := cityFlag, rigFlag
	t.Cleanup(func() { cityFlag, rigFlag = priorCityFlag, priorRigFlag })
	cityFlag, rigFlag = "", ""

	run := func(t *testing.T, args ...string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		cmd := newCostsCmd(&stdout, &stderr)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("gc costs %v: %v; stderr=%s", args, err, stderr.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("gc costs %v unexpected stderr: %s", args, stderr.String())
		}
		return stdout.String()
	}
	t.Run("default-is-session-granular", func(t *testing.T) {
		out := run(t)
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) < 4 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "SESSION ") {
			t.Fatalf("default view must label actual granularity SESSION: %s", out)
		}
		for _, want := range []string{
			"session-1 3 147 0 0 0 90.0 0.0150 1",
			"session-old 0 0 0 0 0 10.0 0.0000 0",
			"TOTAL 3 147 0 0 0 100.0 0.0150 1",
		} {
			found := false
			for _, line := range lines {
				if strings.Join(strings.Fields(line), " ") == want {
					found = true
				}
			}
			if !found {
				t.Errorf("missing conserved session row %q in:\n%s", want, out)
			}
		}
	})
	t.Run("run-view-excludes-wall-and-keeps-unattributed", func(t *testing.T) {
		out := run(t, "--by-run")
		want := map[string]struct {
			invocations, input, unpriced int
			cost                         float64
		}{
			"run-A":          {1, 100, 0, 0.01},
			"run-B":          {1, 40, 1, 0},
			"(unattributed)": {1, 7, 0, 0.005},
		}
		seen := map[string]bool{}
		for _, line := range strings.Split(out, "\n") {
			parts := strings.Fields(line)
			if len(parts) < 5 {
				continue
			}
			w, ok := want[parts[0]]
			if !ok {
				continue
			}
			seen[parts[0]] = true
			invocations, err1 := strconv.Atoi(parts[1])
			input, err2 := strconv.Atoi(parts[2])
			cost, err3 := strconv.ParseFloat(parts[len(parts)-2], 64)
			unpriced, err4 := strconv.Atoi(parts[len(parts)-1])
			if err1 != nil || err2 != nil || err3 != nil || err4 != nil || invocations != w.invocations || input != w.input || cost != w.cost || unpriced != w.unpriced {
				t.Errorf("run row %q did not conserve its own model facts: %q", parts[0], line)
			}
		}
		for id := range want {
			if !seen[id] {
				t.Errorf("missing run bucket %s in:\n%s", id, out)
			}
		}
		if strings.Contains(out, "WALL_S") || strings.Contains(out, "90.0") || strings.Contains(out, "100.0") {
			t.Fatalf("run view must never allocate session wall to a run: %s", out)
		}
		if strings.Contains(out, "session-old") || !strings.Contains(out, "147") || !strings.Contains(out, "0.0150") || !strings.Contains(out, "UNPRICED") {
			t.Fatalf("run totals must conserve model tokens, priced cost and unpriced count: %s", out)
		}
		lower := strings.ToLower(out)
		if !strings.Contains(lower, "wall") || !strings.Contains(lower, "session") || !strings.Contains(lower, "coverage") || !strings.Contains(out, "2/2") {
			t.Fatalf("run view needs a session-wall note and 2/2 revision-one attribution coverage: %s", out)
		}
	})
}
