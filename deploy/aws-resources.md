# Afeehy Hour on AWS: history (created and torn down 16 Oct 2026)

**All of the resources below were deleted after the event on 16 Oct 2026.** Nothing is running or billing. The DNS record `afeehyhour.africhange.com` is DevOps' to remove. Final leaderboard: `results-2026-10-16.json` in the project root.

Kept for reference, in case the game runs again:

Account 961913912488, region eu-west-1, CLI profile `afeehy` (IAM user `afeehy-deploy`).

| Resource | Id |
|---|---|
| Instance (t3.small, Amazon Linux 2023) | `i-0a92cb31cd57d38f3` |
| Elastic IP | `34.252.155.119` (allocation `eipalloc-02e76130958e93d5f`) |
| Security group | `sg-0cf031e713c4c0f98` (443 and 80 from anywhere, 22 from Taiwo's IPs) |
| Key pair | `afeehy-hour`, private key at `~/.ssh/afeehy-hour.pem` |
| Hostname | `afeehyhour.africhange.com`, an A record DevOps created, **proxied through Cloudflare** (orange cloud). Let's Encrypt certificate obtained on the origin via HTTP-01 through the proxy on 16 Oct 2026. The earlier interim name `afeehy.34-252-155-119.sslip.io` is no longer served. |

Secrets (`HOST_KEY`, `EVENT_CODE`) live only in `~/afeehy/.env` on the instance.

Log in: `ssh -i ~/.ssh/afeehy-hour.pem ec2-user@34.252.155.119`, then `cd ~/afeehy`.

Cloudflare in front: verified that the live stream, heartbeats (every 15 s, under Cloudflare's 100 s idle limit) and a 40-player burst all pass through the proxy. Rate limits key on `CF-Connecting-IP` because `TRUST_PROXY=1`.

Teardown (docs/DEPLOY-AWS.md Part D) with `export AWS_PROFILE=afeehy` and the ids above.
Note: on this instance the image was built with `DOCKER_BUILDKIT=0 docker build -t afeehy-app ./app`; after changing app files, repeat that and run `docker compose up -d --no-build`.
