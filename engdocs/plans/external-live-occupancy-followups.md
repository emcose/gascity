# External live worktree occupancy follow-ups

Root bead: `ga-quk26t` · PM plan · 2026-10-01

## Outcome and current state

Pool capacity should charge a live worktree only to work that no live Gas City session already owns. An existing worker's own bead must not consume a second slot, and a bead stranded between templates must not suppress the destination template's legitimate request. Desired-state builds without relevant external work should avoid unnecessary worktree/process scans.

Reviewer `ga-8nlp4r` passed the original change at commit `18137b7550` and filed these follow-ups separately. Deploy bead `ga-qw96c9` is open, so the reviewed code is not yet assumed to be on `origin/main`. A child may test against that exact branch head or a verified landed descendant; every handoff must name which baseline it used. No follow-up reopens the passed review.

## Priority and evidence

The highest-priority gap is a mutation-proven test hole: removing the `alreadyClaimed` guard leaves all 22 tests from the reviewed diff green. A cap-two probe with one live worker and one queued demand then produces only one request, not a retained worker plus a new request. Production live in-progress beads commonly carry a live work directory.

The ownership mismatch is latent but potentially blocks work: a bead assigned to a live session of template A, routed to B, and still carrying A's live work directory suppresses B's new request. The reviewer found no current bead in this state. Architecture must settle what “external” means and how that rule interacts with wake/config counts, cached demand, and discovery errors before implementation.

The scan cost is lower priority. The reviewer measured roughly 50 ms per desired-state build on this host, including builds with no relevant rig work; the non-`/proc` fallback has a 20-second ceiling. Its optimization must preserve correctness and not delay the original deploy.

## Work packages

| Order | Bead | Route | Acceptance |
| --- | --- | --- | --- |
| 1 | `ga-v43r3u` | validator, `needs-tests`, P2 | Pin the cap-two live-worker guard with a mutation-proven regression test and a truly external control case. |
| 1 | `ga-vvz4fk` | architect, `needs-architecture`, P3 | Rule external ownership, wake/config count consistency, demand-snapshot freshness, and per-rig versus whole-scan error behavior. |
| 2 | `ga-4jx7mp` | validator, `needs-tests`, P3 | Reproduce the stranded A-to-B handoff and the architect's named/pool and error cases before the fix. |
| 3 | `ga-k1xyk0` | builder, `ready-to-build`, P3 | Make the cap-two and ownership cases pass while preserving real external-work protection. |
| 4 | `ga-ymfa3l` | builder, `ready-to-build`, P4 | Avoid scans for builds without relevant candidates; measure cost and retain the approved failure behavior. |

`ga-4jx7mp` waits for `ga-vvz4fk`; `ga-k1xyk0` waits for both validator beads; `ga-ymfa3l` waits for `ga-k1xyk0`. Each child carries `discovered-from:ga-quk26t`. If the architect keeps its ruling bead open as a record, it must release or convert the validator's blocking edge when the ruling lands so the chain does not deadlock.

## Delivery risks

The original change is in deployment, not on main. Follow-up tests or fixes based on the wrong tree could test a nonexistent function or duplicate the deploy work. The ownership rule also affects capacity and wake decisions, so a narrow predicate fix must not create duplicate sessions or silently strand a request. The scan optimization is measured separately from correctness; no builder should trade an external-work safety check for the approximately 50 ms saving without the architecture rule and regression coverage.
