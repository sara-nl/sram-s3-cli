# Changelog

All notable changes to this project. Format based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/).

## [1.0.0] — 2026-11-27

Needs broker 3.0.0 or later; CLI 0.x does not work against broker 3.0.0, and 1.0.0 does not work
against broker 2.x.

### Changed (breaking)
- Uses the broker's `/cli/v1` API: one credentials loop for device and PAM logins. The CLI no
  longer receives or sends any token; it hands the broker only the IdP's device code or PAM
  session + PIN.
- Targets are the role's SRAM entitlement (e.g. `org1:project1:s3admin`), or `<id>:<role>` as
  listed, instead of `<collaboration-uuid>:<group>`.
- Cache entries written by 0.x are discarded; log in once again.

### Added
- New role list, grouped by project, with the names known from SRAM (`member`,
  `project admin (s3admin)`, `group <name>`) and the target to pass to `-p`. After a choice,
  the IAM role used is shown. Without a terminal, the list is printed with "re-run with
  -p <target>"; a `-p` that is not one of your roles shows your roles instead of a bare error.
- `sram-s3 login --pam <SRAM username>`: SRAM PAM weblogin in the binary; the PIN is read from
  the terminal without echo.
- `sram-s3 status [-p TARGET]`: the cached credentials per target (valid until, broker) with a
  ready-to-paste `~/.aws/config` profile (absolute path, target, endpoint, region). The cache
  entry stores broker, target, endpoint and region for this.
- `AWS_SESSION_EXPIRATION` is always exported; region and endpoint come from the broker.

### Changed
- `--debug` shows the raw `/cli/v1` responses, with the secret key and session token redacted.
- The bash scripts are merged into one `scripts/sram-s3-login.sh` (device and PAM, `-u` for
  PAM; needs only `bash`, `curl`, `jq`). It prints `export` lines like the binary
  (`eval "$(scripts/sram-s3-login.sh)"`) and never prints credentials or the PIN to the
  terminal; the PIN is no longer echoed.
- README rewritten for 1.0.0.

### Removed
- `OIDC_ISSUER` as a broker URL fallback, and `SRAM_CLI_TARGET` (use `SRAM_PROJECT`).
- The `"default"` region fallback and `AWS_REGION` / `AWS_DEFAULT_REGION` from the environment as
  a fallback: the broker always sends the region.
- `scripts/cli-common.sh`, `sram-s3-sts-device.sh`, `sram-s3-sts-pam.sh`,
  `sram-s3-select-target.py` (and its Python dependency), the scripts' `.env` loading and
  `.env.example`.

### Fixed
- The "missing broker URL" error used a Unicode minus in `-b`.

## [0.3.1] — 2026-11-25

### Changed
- Expired or unreadable cached credentials are deleted automatically whenever
  new credentials are cached.

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

[1.0.0]: https://github.com/sara-nl/sram-s3-cli/compare/v0.3.1...v1.0.0
[0.3.1]: https://github.com/sara-nl/sram-s3-cli/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/sara-nl/sram-s3-cli/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/sara-nl/sram-s3-cli/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/sara-nl/sram-s3-cli/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/sara-nl/sram-s3-cli/releases/tag/v0.1.0
