# sram-s3 CLI

Command-line tool for obtaining temporary S3 credentials from an
[SRAM S3 broker](https://github.com/SURFscz/sram-s3) via the OIDC device code flow.

The Go binary is the recommended tool; bash scripts are provided as an alternative.

---

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/sara-nl/sram-s3-cli/main/install.sh | sh
```

The script detects your OS and architecture, downloads the correct binary, installs it to
`/usr/local/bin/` (or `~/.local/bin/` as fallback), and adds a shell wrapper function.

Or build from source (requires Go 1.22+):

```bash
cd sram-s3
make build
sudo cp sram-s3 /usr/local/bin/
```

### One-time shell setup

The install script does this automatically. If you built from source, add the wrapper manually:

```bash
echo 'srams3() { eval "$(sram-s3 login "$@")"; }' >> ~/.zshrc && source ~/.zshrc
```

---

## Usage

```bash
srams3
# browser opens automatically — sign in, then your terminal continues
aws --endpoint-url "$AWS_ENDPOINT_URL" s3 ls
```

Override broker or pre-select a project:

```bash
srams3 -b https://sram-s3.playground.sdp.surf.nl
srams3 -p surf_demo:project2:members
```

Without the shell wrapper:

```bash
eval "$(sram-s3 login)"
```

---

## Configuration

The Go binary reads environment variables from your shell. The production broker URL is
baked in at build time — no configuration needed for installed binaries.

If you built with `go build .` directly (no ldflags), set the broker URL explicitly:

```bash
export SRAM_BROKER_URL=https://sram-s3.production.sdp.surf.nl
```

or pass it per invocation:

```bash
srams3 -b https://sram-s3.production.sdp.surf.nl
```

---

## Build

```bash
cd sram-s3
make build      # with version + default broker URL via ldflags
make test
make release    # cross-compile all platforms to dist/
```

---

## Repository layout

```
sram-s3/          Go binary source
scripts/          bash scripts (alternative to the Go binary)
  cli-common.sh
  sram-s3-sts-device.sh   device code flow
  sram-s3-sts-pam.sh      PAM weblogin flow
install.sh        end-user install script
```
