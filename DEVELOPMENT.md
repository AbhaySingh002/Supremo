# Development and releases

Run the full local check before committing:

```sh
make precommit
```

## Release

The release tag and `VERSION` must match. Prepare both on `main` before pushing the tag:

```sh
git pull --ff-only origin main
printf 'vX.Y.Z\n' > VERSION
git add VERSION <release-changes>
git commit -m "release: vX.Y.Z"
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin main --follow-tags
```

Pushing the tag starts the GitHub release workflow. It builds the archives and publishes the release; it does not write a follow-up commit.
