# Releasing

A release is a `v*` tag pushed to `main`. `.github/workflows/release.yml` does the rest.

## What the workflow does

1. **release** runs the tests, builds the Windows `.exe` and the macOS and Linux `.tar.gz` archives, writes `SHA256SUMS.txt`, and publishes the GitHub release.
2. **tap** points `Formula/dpi-bypass.rb` in [hong4rc/homebrew-tap](https://github.com/hong4rc/homebrew-tap) at the new tag, using `scripts/bump-formula.py` and the release checksums, and opens a pull request there.
3. **verify-brew** installs from that pull request's branch on a clean macOS runner. It runs `brew audit --strict --online`, `brew install`, `brew test`, then checks that `dpi-bypass --version` equals the tag and that the installed binary has no `com.apple.quarantine` attribute (that attribute is what triggers the "Apple could not verify" warning).

## One-time setup

Create a repository secret named `TAP_TOKEN` in this repository: a personal access token with read and write access to contents and pull requests on `hong4rc/homebrew-tap`. The default workflow token cannot write to another repository. Without the secret the **tap** job fails with a clear message and the formula is not bumped.

## After the workflow finishes

Merge the pull request on the tap once the **verify-brew** job is green. Until it is merged, `brew install hong4rc/tap/dpi-bypass` still installs the previous version.

## Checking by hand

```sh
brew install hong4rc/tap/dpi-bypass
xattr -l "$(brew --prefix)/bin/dpi-bypass"   # no com.apple.quarantine line
dpi-bypass --version                          # prints the release version
```

Run it on a Mac that has never had the tool, or after `brew uninstall dpi-bypass && brew untap hong4rc/tap`.
