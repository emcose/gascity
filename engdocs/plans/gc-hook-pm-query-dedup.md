# PM hook work-query duplication

The `gascity/pm` hook runs the same rig-pinned ready query three times when no
PM work is ready. The resulting duplicate ledger reads add startup cost without
expanding work discovery. This verifies `ga-lofwgh`; implementation is already
owned by the pack migration `ga-atvk13`.

## Evidence

- `packs/actual/pm/pack.toml:67` defines a custom work query that expands to
  `gc bd --rig gascity ready --label=needs-pm --exclude-label hold:mayor,hold:external --exclude-type=step,molecule,convoy --json 2>/dev/null`.
- One `strace -f -e execve,chdir gc hook gascity/pm` on 2026-09-30 recorded
  three launches of that exact query: rig-primary and agent-env legs ran from
  `/home/jaword/projects/gascity`; the city-tertiary leg ran from
  `/home/jaword/projects/gc-management`. Their store environments differ, but
  `--rig gascity` explicitly selects the same ledger for each launch.
- An untraced no-work hook took 2,798 ms. One standalone query took 402 ms.
  The traced hook took 9,878 ms with tracing overhead. These measurements prove
  duplicate reads, but do not attribute all hook time to them.
- `cmd/gc/cmd_hook.go` leaves a custom query verbatim for federated and
  single-store forms. `scopeFederatedHookStores` therefore keeps all three
  legs; the existing primary-leg optimization does not apply to this pack.

## Work package and dependency

`ga-atvk13` already owns replacing label-shaped raw `work_query` overrides,
including this PM pack, with the approved RouteLabel configuration. It remains
blocked on `ga-878prh.1` and must also wait until the new configuration is
present in the deployed `gc` binary. The measurement and acceptance below are
recorded on `ga-atvk13`; no parallel implementation bead is needed.

Acceptance for the PM migration: a no-work `gc hook gascity/pm` executes the
equivalent PM ready query at most once, while ready PM work retains its intended
discovery and claim behavior. Verify the query count with an exec trace or an
equivalent observation after migration.
