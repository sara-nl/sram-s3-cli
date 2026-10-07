# sram-s3 CLI

Command-line tool for obtaining temporary S3 credentials from an
[SRAM S3 broker](https://github.com/SURFscz/sram-s3) via the OIDC device code flow.

The Go binary is the recommended tool; bash scripts are provided as an alternative.

---

## Install

```bash
curl -fsSL https://github.com/SURFscz/sram-s3-cli/releases/latest/download/install.sh | bash
```

Or build from source (requires Go 1.22+):

```bash
cd sram-s3
make build
sudo cp sram-s3 /usr/local/bin/   # makes it available from any directory
```

### One-time shell setup

Add a wrapper function to your shell so you just type `srams3`:

```bash
echo 'srams3() { eval "$(sram-s3 login "$@")"; }' >> ~/.zshrc && source ~/.zshrc
```

(`install.sh` does this automatically.)

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

`.env` is **only auto-loaded by the bash scripts** (`scripts/sram-s3-sts-pam.sh`). The Go
binary does not read `.env` — it reads environment variables from your shell.

For the Go binary built with `make build`, the production broker URL is baked in and no
configuration is needed. If you built with `go build .` directly, set `SRAM_BROKER_URL`
in your shell or pass `-b <url>`.

To configure the bash scripts, copy `.env.example` to `.env`:

```bash
cp .env.example .env
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
sram-s3/      Go binary
scripts/      bash scripts for device code and PAM login
.env.example
install.sh
```
