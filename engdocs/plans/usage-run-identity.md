# Usage facts: run and session identity

Root bead: `ga-2xgzuz` · 2026-10-01 · PM plan

## User outcome

An operator should be able to compare the model tokens and estimated cost of two executions handled by the same long-lived session. A session's wall time should appear at run level only when there is evidence for how that interval was divided. Existing usage history must remain readable at its actual session granularity.

The architect measured 0 of 55,864 production model facts with `run_id != session_id`; 1,228 of 557,300 compute facts differ. Thus today's `gc costs` “run” rows generally describe sessions, including one row spanning about 10.5 days. The earlier `gc.current_run_id` claim-time writer was removed, although `engdocs/design/usage-facts-v0.md` still describes it. The separate `ga-mxrnzl` ruling for contributor PR #6504 addresses per-invocation `formula_name`; it does not settle the identity of a usage fact's `run_id` or allocate an awake interval across runs.

## Product decisions

- Pursue run-level model token and estimated-cost insight. Keep `SessionID` as the stable join to spend and transcripts.
- Do not change `run_id` for model facts alone, silently reinterpret historical session-scoped facts, or attribute an entire multi-run awake interval from a single work pointer.
- Keep wall time at session or unattributed granularity until a reliable interval-level allocation exists. Accurate model cost by run has higher priority than wall time by run.
- Treat local and external `exec:` sink consumers as part of the migration. A change to their grouping key needs a visible compatibility contract. Replays across the changeover must count once.
- Let the architect choose the schema, identity mechanism, and migration path. No technical design is decided by this plan.

## Work packages and order

| Order | Bead | Route | Acceptance focus |
| --- | --- | --- | --- |
| 1 | `ga-rea483` | architect, `needs-architecture` | One coherent model/compute identity contract; legacy and external-consumer compatibility; supported compute granularity; replay rule; boundary with PR #6504. |
| 2 | `ga-uede6n` | validator, `needs-tests` | Red-first evidence for two runs in one session, spanning compute interval, old/new facts, replay, missing lineage, and unpriced totals. |
| 3 | `ga-211c2h` | builder, `ready-to-build` | Emit facts under the approved contract, preserving session joins and honest compute granularity. |
| 4 | `ga-cj8i5h` | builder, `ready-to-build` | Report historical session groups and new run groups accurately; update docs and remove dead identity references after the live path is proven. |

`ga-uede6n` is blocked by `ga-rea483`; `ga-211c2h` by `ga-uede6n`; `ga-cj8i5h` by `ga-211c2h`. Every child has a `discovered-from:ga-2xgzuz` edge. The architecture decision may narrow the later work if it finds run-level compute attribution infeasible now; no downstream bead should claim unsupported per-run wall time.

## Delivery risks

Changing an identity used in idempotency keys can create duplicate facts on replay. Historical facts lack enough evidence to reconstruct the run that produced them. An `exec:` sink consumer may group directly by `run_id` and cannot safely infer a semantic change from the field name alone. The architect's contract and validator's mixed-era cases must settle these before emission changes. PR #6504 may touch the same usage and costs paths; builders should integrate its final attribution work without reopening that separate ruling.
