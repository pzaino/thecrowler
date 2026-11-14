# WP-04C2L E2E Image Provenance

## Contract

The E2E fixture must run API and Events from the exact source under review. It
must never silently reuse a generic `crowler-api` or `crowler-events` image.

## Build path

1. The source tree is copied into the run work directory, excluding `.git`,
   `bin`, `data`, `.env` and `artifacts`.
2. A source-tree SHA-256 is computed from the copied files before generated
   config is added.
3. `Dockerfile.searchapi` and `Dockerfile.events` are built from that copy.
4. The fake model is built from `fixture-mcp`/`fake-model` source into a minimal
   run-scoped Alpine image.
5. Each image is tagged with a run-scoped name, never a generic tag.

Build commands use:

```text
--label org.crowler.e2e.run=<RUN_ID>
--label org.crowler.e2e.project=<PROJECT>
--label org.crowler.e2e.source.sha=<HEAD>
--label org.crowler.e2e.source.tree=<SOURCE_TREE_SHA256>
--label org.crowler.e2e.role=api|events|model
```

## Verified final run

Final isolated run:
`/home/pzaino/tmp/opencode/wp04c2l-e2e-dind7.log`

Source SHA:
`904f5684ccd2fbc2650e5a3ffa1ff41f51d35757`

Source tree digest:
`21126342a7230fa3957b417fdf6ad1b6f56753ea529e23929ea843851a27d7c0`

| Role | Image | Image ID | Run label |
| --- | --- | --- | --- |
| API | `crowler-e2e-a5384e2cdbea-api:a5384e2cdbeac8ee8a7ace7388e66ddc` | `sha256:3b78ec78d999afc96451292dfccb5c40b969979674d88b563ae20cb8a99a93a0` | owned |
| Events | `crowler-e2e-a5384e2cdbea-events:a5384e2cdbeac8ee8a7ace7388e66ddc` | `sha256:0a567076cb1a22f91a4c7362ef5d97e313c56e4b8d5bfd08de865935fc775bcb` | owned |
| Model | `crowler-e2e-a5384e2cdbea-model:a5384e2cdbeac8ee8a7ace7388e66ddc` | `sha256:ee879589b9b3db91fd048d2817fa966b1bef83bb4ff68ce09b7310afc176192e` | owned |

The IDs and labels are retained in:

- `/tmp/opencode/wp04c2l-e2e-dind7/a5384e2cdbeac8ee8a7ace7388e66ddc/images.json`
- `meta/api-image.json`
- `meta/events-image.json`
- `meta/model-image.json`

## Runtime verification

Before assertions, `verify-runtime` inspects each container and fails if:

- the container lacks the run label;
- the container's `Image` ID differs from the source-labelled image ID recorded
  in `images.json`;
- the image's own source SHA/tree labels differ from the current source;
- API/Events `HostConfig.PortBindings` is not `127.0.0.1`;
- the service is missing or not running.

Compose service labels alone are not accepted as provenance proof; the running
image ID and the image's own labels are checked.

The E2E script also refreshes the API dynamic port and checks Docker Compose
port output is loopback-only.

## Reuse policy

Default behavior builds fresh run-scoped images. Reuse is supported only with:

```text
CROWLER_E2E_REUSE_IMAGES=1
CROWLER_E2E_API_IMAGE=...
CROWLER_E2E_EVENTS_IMAGE=...
CROWLER_E2E_MODEL_IMAGE=...
```

Each reused image must carry `org.crowler.e2e.source.sha` equal to the current
`HEAD` and `org.crowler.e2e.source.tree` equal to the current source tree
digest. Missing or stale labels are BLOCKED. Reused images are recorded with
`owned=false` and are not removed by cleanup.

A generic `crowler-api:latest` or `crowler-events:latest` image has no matching
source labels and is rejected.

## Build environment

- API/Events build with the repository Dockerfiles and their configured
  CGO/runtime mode.
- The model fixture builds `CGO_ENABLED=0`.
- Images are built in the disposable Docker-in-Docker daemon used by the E2E
  run; they do not overwrite user image tags.
- The API/Events build copies a placeholder `config.yaml`; the real generated
  config is mounted read-only at runtime and is not baked into the image.

## Limitations

- A Docker build is not bit-for-bit reproducible across base-image updates even
  with a stable source tree. The source labels and image IDs bind the observed
  artifact to the run.
- The E2E workflow is opt-in and not enabled on ordinary pull requests.
