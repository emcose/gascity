# DoltLite shared-server coherence

`ga-n227th` asked whether a DoltLite scope should ignore a polluted user-level
shared-server setting or follow it during both initialization and later reads.
The requested comparison cannot yet be made on the deployed `bd 1.1.0`
(`0954be416`): that binary does not open `backend=doltlite` metadata. The
architecture decision is assigned to `ga-wghyst`; implementation work follows
the ruling.

## Measured behavior

All commands used an isolated workspace and HOME under
`/var/tmp/ga-n227th.YWBJG5`. The real city and operator HOME were untouched.

| Probe | Result |
| --- | --- |
| Direct `bd init` with `BEADS_BACKEND=doltlite`, clean HOME | Exit 0, but metadata says `backend=dolt`, `dolt_mode=embedded`. Create, list, and show work against that ordinary Dolt store. |
| Fork `gc-beads-bd.sh init` with both DoltLite backend environment keys, clean HOME | Exit 0 and rewrites metadata to `backend=doltlite`, while the created database remains under `.beads/embeddeddolt/hq`; no `.beads/doltlite` directory exists. |
| `bd create`, `bd list`, `bd show` after wrapper init, clean HOME | Each exits 1: `storage backend "doltlite" in metadata.json is not recognized or supported; ... the supported backend is "dolt"`. |
| `bd list` and `bd show` after adding `dolt.shared-server: true` to the same scratch HOME | Same error. `strace -f -e trace=flock,openat,connect` recorded no shared-server gate open and no port-3308 connection; there was no scratch server PID and port 3308 remained free. |

The checked-out beads source has no Go reference to `BEADS_BACKEND` or
`GC_BEADS_BACKEND`, consistent with the direct-init result. The alternate
`/home/jaword/projects/beads/bd` binary (`1.1.0 eada50a02`) also refuses
`backend=doltlite` on the scratch scope.

The earlier polluted direct-init run D in `ga-wr5ltv` still shows that `bd`
can enter host shared-server mode under polluted configuration. Its attribution
to a functioning DoltLite backend needs revalidation: the clean direct-init
control above ignored the DoltLite selector. This does not change the separate
S4/S1 managed-server init ruling in `ga-wr5ltv`.

## Work package

`ga-wghyst` goes to the architect with four acceptance points: identify a
supported, working DoltLite init/read path and binary; reinterpret run D on
that path; measure clean-to-polluted reads against one real store; then choose
one policy for both init and steady state. The decision must explain store
identity under a user-level `dolt.shared-server: true` and inherited
`BEADS_DOLT_SHARED_SERVER=1`. It must also say whether an incompatible bd
binary causes a visible refusal or uses another supported path.

After that ruling, create implementation and validation work with measurable
checks for the chosen policy. No builder bead is ready until the backend path
and store-identity invariant are settled. The current PM bead records the
measurement and hands the technical decision back to architecture.
