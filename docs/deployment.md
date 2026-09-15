# Production deployment

Gopad's production delivery uses GitHub Actions to build the exact commit that
passed CI, publish an immutable image to GitHub Container Registry (GHCR), and
restart the server over SSH. The server keeps the Compose files in this
repository, while the image tag identifies the tested commit.

## One-time server setup

Install Docker Engine with the Compose plugin, Git, and curl on the server.
Create a deploy directory and clone the public repository:

```sh
sudo mkdir -p /srv/gopad
sudo chown "$USER":"$USER" /srv/gopad
git clone https://github.com/SlmnFz/gopad.git /srv/gopad
cd /srv/gopad
cp .env.example .env
```

Edit `.env` on the server. At minimum, replace the Grafana password and set
any deployment-specific ports or runtime values. The app, Prometheus, and
Grafana ports bind to loopback by default; Nginx should be the public entry
point. Keep the existing `deploy/nginx/gopad.conf` in front of the app and
configure TLS at the server or your certificate manager.

The production Compose file adds restart policies and is intentionally kept
separate from the local development defaults:

```sh
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build
curl --fail http://127.0.0.1:8080/healthz
```

The CD workflow pulls `ghcr.io/slmnfz/gopad:<commit-sha>` and does not build
on the server. Make the GHCR package public, or log in to GHCR on the server
once with a token that has `read:packages`:

```sh
echo "$GHCR_READ_TOKEN" | docker login ghcr.io -u YOUR_GITHUB_USERNAME --password-stdin
```

Do not put that token in the repository or in `.env`.

## GitHub configuration

Create a GitHub environment named `production` in the repository settings.
Add these environment secrets:

| Name | Value |
| --- | --- |
| `DEPLOY_HOST` | Server hostname or IP address |
| `DEPLOY_USER` | SSH user that can run Docker Compose |
| `DEPLOY_SSH_PRIVATE_KEY` | Private Ed25519 key for that user |
| `DEPLOY_KNOWN_HOSTS` | Reviewed output of `ssh-keyscan -H your-host` |

Add this environment variable:

| Name | Value |
| --- | --- |
| `DEPLOY_PATH` | `/srv/gopad` |

Generate a dedicated key locally, install only its public key in the deploy
user's `~/.ssh/authorized_keys`, and store the private half as the secret. Do
not use a personal key or send private key material through issues, commits,
or chat.

## How deployment runs

After a push to `main`, the `CI` workflow runs first. When it succeeds, `CD`:

1. Checks out the exact commit tested by CI.
2. Builds and publishes both `latest` and `sha-<full-commit-sha>` to GHCR.
3. Waits for the `production` environment, including any configured approval.
4. SSHes to the server, checks out that commit, pulls the immutable image, and
   recreates only the Gopad service.
5. Retries `/healthz` before marking the deployment successful.

The workflow is guarded so a failed CI run or a workflow run from a fork cannot
publish or deploy. The deploy concurrency group prevents overlapping production
rollouts.

## Rollback

Every deployment uses an immutable commit tag. To roll back, identify the last
known-good commit and run on the server:

```sh
cd /srv/gopad
git fetch origin main --prune
git checkout --detach <previous-commit-sha>
export GOPAD_IMAGE="ghcr.io/SlmnFz/gopad:sha-<previous-commit-sha>"
docker compose -f docker-compose.yml -f docker-compose.prod.yml pull gopad
docker compose -f docker-compose.yml -f docker-compose.prod.yml up \
  -d --no-build --pull always --force-recreate gopad
curl --fail http://127.0.0.1:8080/healthz
```

After the incident is understood, restore the server checkout to `main` so
the next automated deployment can advance normally.
