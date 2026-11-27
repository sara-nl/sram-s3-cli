# sram-s3 CLI

`sram-s3` logs you in with SRAM through the SRAM-S3 broker and gives you
temporary S3 credentials for your project's object store: as shell variables, or for AWS tools
through a profile (`credential_process`). You log in with a device code in the browser, or with
SRAM PAM weblogin (`--pam`) where no browser is at hand.

CLI 1.x needs broker 3.0.0 or later (the `/cli/v1` API). A bash script that does the same is
included as an alternative to the binary.

---

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/sara-nl/sram-s3-cli/main/install.sh | bash
```

The script downloads the binary for your OS and architecture, verifies its checksum and installs
it to `~/.local/bin`; no sudo needed. It adds a short block to your shell rc file (`~/.zshrc`,
`~/.bashrc`, or `~/.bash_profile` on macOS) that puts `~/.local/bin` on your `PATH` and defines
the `srams3` shell function. Open a new terminal afterwards.

Options, set as environment variables for `bash`:

```bash
# install system-wide (uses sudo if the directory is not writable)
curl -fsSL https://raw.githubusercontent.com/sara-nl/sram-s3-cli/main/install.sh | SRAM_S3_INSTALL_DIR=/usr/local/bin bash

# install a specific version
curl -fsSL https://raw.githubusercontent.com/sara-nl/sram-s3-cli/main/install.sh | SRAM_S3_VERSION=v1.0.0 bash

# do not touch shell rc files (prints what to add instead)
curl -fsSL https://raw.githubusercontent.com/sara-nl/sram-s3-cli/main/install.sh | SRAM_S3_NO_MODIFY_PATH=1 bash
```

Or build from source (requires Go 1.22+):

```bash
cd sram-s3
make build
mkdir -p ~/.local/bin && cp sram-s3 ~/.local/bin/
```

### One-time shell setup: `sram-s3` and `srams3`

`sram-s3` is the program. It cannot change your shell's variables itself, so `sram-s3 login`
prints `export AWS_…=…` lines, and your shell runs them with `eval`. `srams3` is a shell function
that does both in one word. The install script adds it; otherwise add to your `~/.zshrc` or
`~/.bashrc`:

```bash
export PATH="$HOME/.local/bin:$PATH"
srams3() { eval "$(sram-s3 login "$@")"; }
```

### Uninstall

```bash
rm ~/.local/bin/sram-s3
rm -rf ~/.cache/sram-s3 ~/Library/Caches/sram-s3   # cached credentials
```

and remove the `# >>> sram-s3 >>>` … `# <<< sram-s3 <<<` block from your shell rc file.

---

## Log in

```bash
srams3
# the browser opens: sign in with SRAM, then your terminal continues
aws s3 ls
```

`srams3` (or `eval "$(sram-s3 login)"`) sets `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`,
`AWS_SESSION_TOKEN`, `AWS_SESSION_EXPIRATION`, `AWS_ENDPOINT_URL`, `AWS_REGION` and
`AWS_DEFAULT_REGION` in your shell. On a machine without a browser (SSH), open the printed URL
on any device.

### Choosing a role

When you hold several roles, `sram-s3` lists them, grouped by project:

```
Choose a role:
  Project 1
    1) member                    org1:project1
    2) project admin (s3admin)   org1:project1:s3admin
    3) group group1              org1:project1:group1
  Project 2
    4) member                    org2:project2
    5) project admin (s3admin)   org2:project2:s3admin
    6) group readonly            org2:project2:readonly
Role (1-6):
```

- **member**: every member of the SRAM collaboration;
- **project admin (s3admin)**: members of the collaboration's `s3admin` group: the project's
  S3 admin role;
- **group &lt;name&gt;**: members of another SRAM group of the collaboration that has its own role.

The last column is the role's **target**: its SRAM entitlement (`org1:project1:s3admin`), or
`<id>:<role>` when your collaboration has several object store accounts. Pass it with `-p` to
skip the list:

```bash
srams3 -p org1:project1:s3admin
```

You can also set it once with `export SRAM_PROJECT=org1:project1:s3admin`.

### PAM weblogin

To log in with SRAM PAM weblogin instead of a device code, give your SRAM username:

```bash
srams3 --pam jdoe
```

`sram-s3` shows SRAM's message with a link; open it, log in, and type the PIN you get at the
`PIN:` prompt (it is not echoed). PAM weblogin must be enabled on the broker.

---

## AWS tools: profiles with `credential_process`

`sram-s3` can be an AWS `credential_process` provider, so any AWS tool (AWS CLI, boto3, Terraform,
…) gets credentials through a named profile in `~/.aws/config`, without `eval`.

Log in once for the role you want, then let `sram-s3 status` print the profile:

```bash
sram-s3 login --credential-process -p org1:project1:s3admin > /dev/null
sram-s3 status
```

```
org1:project1:s3admin   valid until 14:32 (47 min)   https://sram-s3.production.sdp.surf.nl

[profile org1-project1-s3admin]
credential_process = /home/you/.local/bin/sram-s3 login --credential-process -p org1:project1:s3admin
endpoint_url = https://objectstore.surf.nl
region = default
```

Paste the `[profile …]` block into `~/.aws/config` and use it:

```bash
aws --profile org1-project1-s3admin s3 ls
```

The profile has the absolute path to `sram-s3` (tools started from an IDE often do not see your
shell's `PATH`), the exact target, and the endpoint and region from the broker. Add one profile
per role. `sram-s3 status -p TARGET` shows one role; expired entries are listed too, as their
profile stays valid (the next AWS command simply logs in again).

`sram-s3` caches the credentials on disk (`~/Library/Caches/sram-s3/` on macOS,
`~/.cache/sram-s3/` on Linux, readable only by you) and reuses them until 5 minutes before they
expire, so the browser opens only when a new login is needed. The cache needs `-p`: without it,
you choose a role at every login.

To remove cached credentials (after a role change, or on a shared machine):

```bash
sram-s3 logout                              # all cached credentials
sram-s3 logout -p org1:project1:s3admin    # one role
```

`logout` only deletes the local copy; issued credentials stay valid until they expire.

---

## Bash alternative

`scripts/sram-s3-login.sh` does the same login without the Go binary. It needs only `bash`,
`curl` and `jq`, and prints the same `export` lines:

```bash
eval "$(scripts/sram-s3-login.sh)"                          # device code, in the browser
eval "$(scripts/sram-s3-login.sh -p org1:project1:s3admin)"
eval "$(scripts/sram-s3-login.sh -u jdoe)"                  # PAM weblogin
```

Options: `-b URL` (broker), `-p TARGET`, `-u SRAM_USERNAME` (PAM), `-d` (debug). It reads
`SRAM_BROKER_URL` and `SRAM_PROJECT`; there is no built-in broker URL, so set one of them or
pass `-b`. There is no credential cache or `credential_process` mode: use the binary for that.

---

## Troubleshooting

| Message | What to do |
|---------|------------|
| `Your SRAM entitlements give you no role on any project of this broker (HTTP 403 …)` | Your SRAM account has no role on a project of this broker. |
| `You have no role for target "…"` followed by your roles | The `-p` target (or `SRAM_PROJECT`) is not one of your roles. Use a target from the list. |
| `the login expired before sign-in was completed` or `login_session expired or already used` | Run the login again. |
| `PAM weblogin is not enabled on this broker; use the device login (HTTP 404 …)` | Log in without `--pam`. |

`sram-s3 login -d` shows the broker's answers, with the secret key and session token redacted.

---

## Configuration

| Setting | Flag | Environment | Default |
|---------|------|-------------|---------|
| Broker URL | `-b`, `--broker` | `SRAM_BROKER_URL` | the production broker, built into released binaries |
| Role (target) | `-p`, `--project` | `SRAM_PROJECT` | choose from the list when you hold several roles |

A binary built with plain `go build` has no broker URL built in: set `SRAM_BROKER_URL` or pass
`-b`.

---

## Build and release

```bash
cd sram-s3
make build      # version from git, production broker URL via ldflags
make test       # includes tests of scripts/sram-s3-login.sh (needs bash, curl, jq)
make check      # go vet, gofmt, tests, shellcheck: run before a commit
DEFAULT_BROKER_URL=https://sram-s3.playground.sdp.surf.nl make build   # for tests on playground
```

Releases (all platforms, checksums, GitHub Release) are built with
[GoReleaser](https://goreleaser.com) from the repository root, on a laptop:

```bash
goreleaser release --snapshot --clean   # local test build into dist/
git tag vX.Y.Z && git push origin vX.Y.Z
goreleaser release --clean              # publish
```

---

## Repository layout

```
sram-s3/              Go binary source
  main.go             commands and flags
  client.go           broker /cli/v1 API and the login loop
  picker.go           role list and choice; PIN prompt
  output.go           export lines, credential_process JSON
  cache.go            credential cache, logout, status
scripts/
  sram-s3-login.sh    bash alternative to the binary
install.sh            end-user install script
.goreleaser.yaml      release build configuration
```
