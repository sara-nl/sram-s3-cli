# Changelog

All notable changes to this project. Format based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/).

## [0.3.0] — 2026-11-25

### Added
- `sram-s3 logout` removes cached `--credential-process` credentials: all of
  them, or one project with `-p TARGET` (and optional `-b URL`). Issued
  credentials stay valid until they expire; only the local copy is removed.
- Tip on stderr when the credentials were cached under a different target than
  requested (e.g. after choosing a project interactively): add `-p <target>`
  to reuse the cache.
- Unit tests for the credential cache and `logout`.

### Changed
- `install.sh` installs to `~/.local/bin` and no longer uses sudo on its own.
  Previously it used sudo whenever the `sudo` command existed, which failed for
  users without sudo rights (e.g. on HPC login nodes).
- `install.sh` adds a marked block to the shell rc file (`~/.zshrc`,
  `~/.bashrc`, or `~/.bash_profile` on macOS). The block puts the install
  directory on `PATH` and defines the `srams3` wrapper. The rc file is created
  if missing and left alone on re-runs.
- `install.sh` options (environment variables):
  - `SRAM_S3_INSTALL_DIR`: install directory; sudo only if it is not writable
  - `SRAM_S3_VERSION`: install a specific release
  - `SRAM_S3_NO_MODIFY_PATH=1`: print the rc lines instead of writing them
- `install.sh` stops with a clear message when run with `sh` instead of `bash`
  (`curl … | bash`).
- `install.sh` prints the absolute binary path for use in
  `credential_process`.
- Help text and README describe the credential cache, `logout`, install
  options, and uninstalling.

## [0.2.1] — 2026-11-13

### Added
- On-disk credential cache for `--credential-process`
  (`~/Library/Caches/sram-s3/` on macOS, `~/.cache/sram-s3/` on Linux;
  files `0600`). Credentials are reused until 5 minutes before they expire, so
  AWS tools no longer start a browser login for every command.

## [0.2.0] — 2026-11-13

### Added
- `login --credential-process` prints AWS `credential_process` JSON
  (Version 1), so AWS CLI, boto3, Terraform etc. can use SRAM credentials via
  a profile in `~/.aws/config`.
- `--debug` logs the raw broker response on every `/cli/device-sts` call,
  including the intermediate project-selection step.
- README section on AWS profile integration.

### Removed
- `--json`: only ever showed the final broker response; use `--debug`.
- `--eval`: shell export lines are the default output.

## [0.1.0] — 2026-11-11

First release.

### Added
- `sram-s3 login`: OIDC device code flow against the SRAM S3 broker; prints
  shell `export` lines (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`,
  `AWS_SESSION_TOKEN`, `AWS_ENDPOINT_URL`, `AWS_REGION`) for `eval`.
- Opens the browser automatically; always prints the URL for SSH/headless use.
- Interactive project selection when the account has multiple projects;
  `-p TARGET` to pre-select.
- Production broker URL built in; override with `-b` or `SRAM_BROKER_URL`.
- `--json`, `--eval` and `--debug` flags; `sram-s3 version`.
- Pre-built binaries for macOS and Linux (amd64, arm64) via GoReleaser and
  GitHub Releases, with `checksums.sha256`.
- `install.sh`: downloads the binary, verifies the checksum, installs it, and
  adds the `srams3` shell wrapper.
- Bash scripts as an alternative: device code flow and PAM weblogin flow.
- Apache 2.0 license.

[0.3.0]: https://github.com/sara-nl/sram-s3-cli/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/sara-nl/sram-s3-cli/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/sara-nl/sram-s3-cli/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/sara-nl/sram-s3-cli/releases/tag/v0.1.0
