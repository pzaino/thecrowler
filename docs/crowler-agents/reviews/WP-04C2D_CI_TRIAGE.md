# WP-04C2D CI Triage: VDI Legacy Audio Control

## Failure

Reviewed commit `1c98d753` failed the GitHub `build` check at
`pkg/vdi/camouflage_node_test.go:864`:

```text
control failed: harness did NOT detect the legacy AudioBuffer
cumulative-mutation defect (audioRawIntact=true on legacy)
```

Compilation passed; this was a runtime negative-control assertion.

## Reproduction and root cause

Local reproduction:

```sh
go test ./pkg/vdi -run '^TestCamouflageControlsDetectLegacyDefects$' -count=20
```

The control failed intermittently. Source inspection found:

- `audioProbe` runs `audioCopyToChannel` before `audioRawIntact`, which fills
  the live Float32Array store with `1.0`.
- The legacy fixture adds `Math.random() * 0.0000001` to samples at indices
  `0`, `100` and `200`.
- The float32 ULP at `1.0` is about `1.19e-7`; half-ULP is about `5.96e-8`.
  An increment below half-ULP rounds back to `1.0`. With three sampled indices,
  the chance that all three round back is material (roughly several percent),
  which makes the negative control nondeterministic.
- The fixed patch leaves the live store intact, so its assertion was not the
  problem.

The VDI harness and test are byte-identical between parent `cc2461d4` and
reviewed `1c98d753`:

```text
git rev-parse cc2461d4:pkg/vdi/testdata/camouflage_harness.js
git rev-parse 1c98d753:pkg/vdi/testdata/camouflage_harness.js
# both 1d9fa5c8ef6f721951ca132556f76df40a5a5713
```

The defect therefore predates WP-04C2C.

## Fix

`pkg/vdi/testdata/camouflage_harness.js` now resets the live store to
`REFBASE` (`0.123456789`) immediately before the `audioRawIntact` snapshot.
At that magnitude, the legacy `1e-7` noise is above float32 resolution and is
always observable, while the fixed patch still leaves the store unchanged.
The assertion was not skipped, weakened or removed.

A focused regression test was added:

```text
TestCamouflageLegacyAudioControlIsDeterministic
```

It runs the legacy control five times and requires detection every time.

## Evidence

- `go test ./pkg/vdi -run '^TestCamouflageControlsDetectLegacyDefects$' -count=20`
  with Node 24.22.1: PASS.
- Node 20.20.2 single run (Docker `node:20-bookworm`): PASS.
- Node 22.23.5 single run (Docker `node:22-bookworm`): PASS.
- `TestCamouflageLegacyAudioControlIsDeterministic`: PASS.
- Full `go test ./...`: PASS.

The Docker-backed Node 20/22 `-count=10` attempts timed out at the Go test
10-minute limit because each harness invocation starts a container; single-run
Node 20 and Node 22 evidence is recorded instead. This is environmental Docker
overhead, not a test assertion failure.

## CI status

The GitHub `build` check was not rerun because this worktree is intentionally
not pushed. The failure is reproduced and fixed locally; the maintainer must
push/rerun the workflow to obtain a fresh CI result. This report does not claim
the GitHub check is green.
