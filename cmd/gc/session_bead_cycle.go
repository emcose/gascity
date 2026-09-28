package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	convoycore "github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/storeref"
)

// recordCurrentBeadIDOnWake persists the work bead a session is being woken
// for. The reconciler writes this whenever a session is brought up (asleep
// → awake or alive cycle) so that subsequent reconciler ticks can detect
// when the assignee has been pointed at a different bead. The metadata
// survives session restart, so crash recovery can resume the same bead
// instead of jumping to a sibling assignment.
// recordCurrentBeadIDOnWake returns the metadata patch it applied (the
// currently_processing_bead_id write) so the reconciler can fold it onto the
// infoByID snapshot (write-returns-Info), or nil when it was a no-op. It reads
// the session id and the currently-processing bead off the caller's coherent
// typed Info (Info.ID / Info.CurrentlyProcessingBeadID, both verbatim raw
// mirrors); the fold the caller applies keeps the snapshot in step.
func recordCurrentBeadIDOnWake(info sessionpkg.Info, sessFront *sessionpkg.Store, beadID string, stderr io.Writer) sessionpkg.MetadataPatch {
	if strings.TrimSpace(info.ID) == "" || sessFront == nil {
		return nil
	}
	beadID = strings.TrimSpace(beadID)
	if beadID == "" {
		return nil
	}
	if info.CurrentlyProcessingBeadID == beadID {
		return nil
	}
	if err := sessFront.RecordCurrentBead(info.ID, beadID); err != nil {
		if stderr != nil {
			fmt.Fprintf(stderr, "session reconciler: recording %s for %s: %v\n", sessionpkg.CurrentBeadIDKey, info.SessionNameMetadata, err) //nolint:errcheck
		}
		return nil
	}
	return sessionpkg.MetadataPatch{sessionpkg.CurrentBeadIDKey: beadID}
}

// prevAssignedBeadStatus looks up a single bead by id and reports whether it
// is still open (non-terminal, via convoycore.IsTerminalStatus) and, when
// terminal, the closed_at timestamp from its metadata. One store round trip
// answers both questions, so the fresh-cycle guard in session_reconciler.go
// does not need a second lookup to get closed_at after checking status.
// closedAt is the zero Time when the bead is open or closed_at is absent or
// unparseable.
func prevAssignedBeadStatus(store beads.Store, id string) (open bool, closedAt time.Time, err error) {
	b, err := store.Get(id)
	if err != nil {
		return false, time.Time{}, err
	}
	if !convoycore.IsTerminalStatus(b.Status) {
		return true, time.Time{}, nil
	}
	if ca := b.Metadata["closed_at"]; ca != "" {
		if t, perr := time.Parse(time.RFC3339Nano, ca); perr == nil {
			closedAt = t
		}
	}
	return false, closedAt, nil
}

// prevBeadStillAssignedToSession fetches id with one fresh, uncached read and
// reports whether its LIVE assignee is still one of this session's own
// identifiers. ga-pvjbx3 Q3: after ga-2weagw's store/closed_at fix, row A/C
// can defer on a previous bead that is still open but has since been handed
// to someone else (e.g. a reviewer) — that deferral is then keyed to an
// unrelated future close event with no relationship to what this session is
// doing by then. Narrowing row A/C to require the live assignee still match
// is a strict subset of the pre-existing (open ⇒ defer) condition, so it
// cannot regress any case where the assignee still matches; when it does not
// match, the caller falls through immediately to the Q1/Q2 checks instead of
// deferring on a bead this session no longer owns.
func prevBeadStillAssignedToSession(store beads.Store, id string, identifiers []string) (bool, error) {
	b, err := store.Get(id)
	if err != nil {
		return false, err
	}
	assignee := strings.TrimSpace(b.Assignee)
	if assignee == "" {
		return false, nil
	}
	for _, ident := range identifiers {
		if ident != "" && ident == assignee {
			return true, nil
		}
	}
	return false, nil
}

// sessionHasFreshInProgressClaim reports whether this session currently
// holds ANY assigned work bead — anywhere its topology can reach, via one
// fresh, uncached read — with status == in_progress. ga-pvjbx3 Q1: the
// fresh-cycle guard's old self-claim check compared the anchor
// (ComputeAwakeSet's assignedAnchor, which discards every candidate but one
// — exact match to the stamped bead, else first-in-order fallback) against
// current_claim_bead_id, but the anchor and this session's real self-claim
// can be two different, unrelated beads. The correct check is anchor-agnostic
// existence, not anchor equality: does this session hold ANY live
// in_progress claim at all, regardless of which bead the anchor separately
// resolved to. Delegates to the existing progress-stall-recycle existence
// check, which already sweeps the unrestricted store+rig topology
// (assignedWorkSweepPlan, not the agent-scoped
// assignedWorkPlanForSessionInfo) and already excludes mail and session
// beads — the same population workBeadHasAwakeDemand treats as
// awake-eligible.
func sessionHasFreshInProgressClaim(cityPath string, cfg *config.City, store beads.Store, rigStores map[string]beads.Store, info sessionpkg.Info) (bool, error) {
	return sessionHasInProgressAssignedWorkForConfig(cityPath, cfg, store, rigStores, info)
}

// freshAnchorBeadTerminal fetches the fresh-cycle guard's anchor bead
// (ComputeAwakeSet's decision.AssignedWorkBeadID) with one fresh, uncached
// read and reports whether it is already terminal (closed). ga-pvjbx3 Q2:
// the tick's assigned-work snapshot can already be several minutes stale by
// the time the kill decision runs; cycling a session onto an anchor that has
// since closed is pure loss — there is no work left to reassign onto.
// Sweeps the same unrestricted store+rig topology assignedWorkSweepPlan
// gives sessionHasFreshInProgressClaim (not the agent-scoped
// assignedWorkPlanForSessionInfo), because the anchor itself was resolved
// from that same unrestricted candidate population, not from this session's
// own reachable stores.
func freshAnchorBeadTerminal(cityPath string, cfg *config.City, store beads.Store, rigStores map[string]beads.Store, info sessionpkg.Info, anchorID string) (bool, error) {
	anchorID = strings.TrimSpace(anchorID)
	if anchorID == "" {
		return false, nil
	}
	identifiers := sessionAssignmentIdentifiersForConfigInfo(info, cfg)
	plan, err := assignedWorkSweepPlan(cityPath, cfg, store, rigStores, identifiers)
	if err != nil {
		return false, err
	}
	var anchor beads.Bead
	var found bool
	res, err := storeref.Walk(plan, func(leg storeref.Leg) (bool, error) {
		if leg.Store == nil {
			return false, nil
		}
		b, gerr := leg.Store.Get(anchorID)
		if gerr != nil {
			if errors.Is(gerr, beads.ErrNotFound) {
				return false, nil
			}
			return false, gerr
		}
		anchor = b
		found = true
		return true, nil
	})
	if err != nil {
		return false, err
	}
	if !found {
		if serr := assignedWorkScanComplete(res); serr != nil {
			return false, serr
		}
		return false, nil
	}
	return convoycore.IsTerminalStatus(anchor.Status), nil
}

// cycleAliveSessionForFreshReassign tears down a live wake_mode=fresh
// session whose assigned bead has changed, then primes the bead so the
// next reconciler tick wakes the session on a brand-new conversation.
// Returns (true, fold) when the cycle ran; the caller must `continue` so it
// does not double-process the drain/idle bookkeeping for a session it just
// killed. The fold is the in-memory mirror it applied (RestartRequestPatch
// minus ResetCommittedAtKey), for the reconciler to fold onto the infoByID
// snapshot (write-returns-Info).
//
// The teardown path mirrors the agent-initiated restart handoff
// (`gc runtime request-restart`): kill the process, reset the named-session
// circuit breaker (a cycle is deliberate, not a crash — accumulated breaker
// state must not block the post-cycle wake), optionally rotate session_key
// for providers that accept --session-id, then apply RestartRequestPatch so
// the next wake observes firstStart=true and uses the fresh-wake
// conversation reset. We also update currently_processing_bead_id to the
// new anchor so the divergence check does not refire on the next tick.
func cycleAliveSessionForFreshReassign(
	info sessionpkg.Info,
	tp TemplateParams,
	sp runtime.Provider,
	store beads.Store,
	cfg *config.City,
	cb *sessionCircuitBreaker,
	name string,
	newBeadID string,
	now time.Time,
	stdout, stderr io.Writer,
	trace *sessionReconcilerTraceCycle,
) (bool, sessionpkg.MetadataPatch) {
	if store == nil {
		return false, nil
	}
	newBeadID = strings.TrimSpace(newBeadID)
	if newBeadID == "" {
		return false, nil
	}
	prevBeadID := strings.TrimSpace(info.CurrentlyProcessingBeadID)
	if err := workerKillSessionTargetWithConfig("", store, sp, cfg, name); err != nil {
		if stderr != nil {
			fmt.Fprintf(stderr, "session reconciler: stopping fresh-cycle %s: %v\n", name, err) //nolint:errcheck
		}
		return false, nil
	}
	if identity := namedSessionIdentityInfo(info); identity != "" {
		if err := resetSessionCircuitBreakerState(store, info.ID, identity, cb); err != nil {
			if stderr != nil {
				fmt.Fprintf(stderr, "session reconciler: clearing session circuit breaker for fresh-cycle %s: %v\n", name, err) //nolint:errcheck
			}
			return false, nil
		}
	}
	newSessionKey, hasCapability := freshRestartSessionKeyInfo(tp, info)
	batch := sessionpkg.RestartRequestPatch(newSessionKey, now)
	if hasCapability && newSessionKey == "" {
		batch["session_key"] = ""
	}
	batch[sessionpkg.CurrentBeadIDKey] = newBeadID
	if err := sessionFrontDoor(store).ApplyPatch(info.ID, batch); err != nil {
		if stderr != nil {
			fmt.Fprintf(stderr, "session reconciler: recording fresh-cycle handoff for %s: %v\n", name, err) //nolint:errcheck
		}
		return false, nil
	}
	// The returned fold carries every batch key EXCEPT the durable reset commit
	// marker: keeping ResetCommittedAtKey out of this tick's snapshot mirrors the
	// restart-requested handoff so on-demand sessions are not force-woken without
	// demand within the same tick. The former raw session.Metadata mirror loop is
	// deleted — it wrote the identical key set as this fold, and the caller applies
	// the fold to infoByID before `continue`ing (no later raw read this tick).
	fold := make(sessionpkg.MetadataPatch, len(batch))
	for key, value := range batch {
		if key == sessionpkg.ResetCommittedAtKey {
			continue
		}
		fold[key] = value
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "Cycled fresh-mode session '%s' for bead reassign: %s → %s\n", name, prevBeadID, newBeadID) //nolint:errcheck
	}
	if trace != nil {
		trace.RecordDecision(TraceSiteReconcilerBeadReassignCycle, TraceReasonFreshCycle, TraceOutcomeRestart, tp.TemplateName, name, traceRecordPayload{
			"previous_bead_id": prevBeadID,
			"new_bead_id":      newBeadID,
		})
	}
	return true, fold
}
