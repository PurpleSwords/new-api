# Deploying with the GHCR image

The GitHub Actions workflows publish multi-architecture images to:

```text
ghcr.io/purpleswords/new-api
```

The server only needs Docker and Docker Compose. It does not need Go, Bun,
the source tree, or a local compiler.

## Build an image

Use the `Publish Docker image (manual branch)` workflow to build a branch.
For the currently deployed customization, enter:

```text
custom/web-search
```

The workflow publishes both a branch tag and an immutable commit-based tag,
for example:

```text
ghcr.io/purpleswords/new-api:custom-web-search
ghcr.io/purpleswords/new-api:custom-web-search-20260807-ca380fb
```

Prefer the commit-based tag for production deployments. The `latest` tag is
mutable and should not be used as the only rollback reference.

## Prepare GHCR access

After the first successful workflow run, set the package visibility to Public
if the server should pull without credentials. For a private package, log in
on the server with a token that has `read:packages`; do not put that token in
Compose or commit it to the repository.

## Deploy or roll back

Set the image tag when invoking Compose:

```bash
export NEW_API_IMAGE=ghcr.io/purpleswords/new-api:custom-web-search-20260807-ca380fb
docker compose pull new-api
docker compose up -d --no-deps --force-recreate new-api
```

To roll back, set `NEW_API_IMAGE` to the previous known-good immutable tag and
repeat the two Compose commands. The `data` and `logs` volumes are preserved.
