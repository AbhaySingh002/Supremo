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

Installers read the latest tag from the raw `VERSION` file in `main`, then download the matching release asset and `checksums.txt`. The release workflow publishes Linux and macOS archives for amd64 and arm64, plus Windows ZIPs for amd64 and arm64.

After the first tagged release, smoke test installers:

```sh
curl -fsSL https://raw.githubusercontent.com/AbhaySingh002/Supremo/main/scripts/install.sh | sh
# Open a new terminal if this was the first installation.
supremo --version
```

```powershell
irm https://raw.githubusercontent.com/AbhaySingh002/Supremo/main/scripts/install.ps1 | iex
supremo --version
```
