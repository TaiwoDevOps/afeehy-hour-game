# Deploying Afeehy Hour: local dry run, then AWS

**Who this is for:** you, to understand and, if needed, perform the deployment yourself from your own laptop. DevOps will do something equivalent on the company subdomain; Part E explains how their setup differs.

**What you end up with:** one small Linux server on AWS running two containers. The first is the game server built from the application files. The second is Caddy, a web server that gets a free HTTPS certificate and forwards traffic to the game. Players open `https://afeehy.your-company.com/`, hosts open the same address with `?host=…`.

```
 Internet ──443──▶ Caddy (TLS, certificate) ──8080──▶ afeehy-hour (Go)
                      │                                  │
                      └── certificate volume             └── /data volume: afeehy-data.json
                  ............ one EC2 instance, one Docker Compose file ............
```

The same Compose file runs on your Mac first, so you see the whole thing work before touching AWS.

---

## 0. Why this shape

- **No database.** The app keeps state in memory and writes `afeehy-data.json` once a second. It therefore needs exactly **one** instance and a disk that survives restarts. A single virtual machine with a Docker volume is the simplest thing that satisfies that.
- **Why Caddy.** It handles HTTPS automatically (Let's Encrypt), sets `X-Forwarded-For` and `X-Forwarded-Proto`, and streams the live `/events` connection without buffering. Nginx would need manual certificates and more configuration.
- **Why EC2 and not ECS, App Runner or Lambda.** Those are designed for stateless, horizontally scaled services. App Runner also cuts every request at 120 seconds, which breaks the live stream. For a stateful one-hour game, a single VM is cheaper, simpler and easier to reason about. The appendix lists what it would take to use them anyway.

## 1. Prerequisites

On your Mac:

```bash
brew install awscli            # AWS command line
docker --version               # Docker Desktop must be installed and running
unzip -l ~/Downloads/afeehy-hour-deploy.zip   # the application files
```

In AWS: an account where you can create EC2 instances, and an access key for the CLI. Configure it once and confirm it works:

```bash
aws configure                  # access key, secret, default region (e.g. eu-west-1), output json
aws sts get-caller-identity    # prints your account id and user: the CLI is working
```

Every command below runs in your terminal. The AWS ones talk to the AWS API, the `ssh` ones run on the server.

## 2. The deployment files

They live in `deploy/` in this folder:

| File | Purpose |
|---|---|
| `docker-compose.yml` | describes the two containers, their ports, volumes and environment |
| `Caddyfile` | tells Caddy which domain to serve and where to forward |
| `.env.example` | template for the three values you must supply |
| `app/` (you create it) | the unzipped application files, the build input |

The three values:

| Variable | What it is |
|---|---|
| `DOMAIN` | `localhost` for the dry run, the real subdomain on AWS |
| `HOST_KEY` | the secret that turns a browser into a host. Generate: `openssl rand -hex 16` |
| `EVENT_CODE` | the short code players type to join |

## Part A: dry run on your Mac

This runs the production stack locally, including HTTPS, so every later step on AWS is a repeat of something you've already seen.

```bash
cd ~/Downloads/afeehy-hour/deploy
rm -rf app && mkdir app && unzip -q ~/Downloads/afeehy-hour-deploy.zip -d app
cp .env.example .env
# edit .env: DOMAIN=localhost, a HOST_KEY, an EVENT_CODE
docker compose up -d --build
```

What happens:

1. `--build` reads `app/Dockerfile`. Stage one compiles the Go program inside a `golang` image. Stage two copies the single binary into a tiny Alpine image. The result is a ~15 MB image with no compiler in it.
2. Compose creates the volumes `data`, `caddy_data` and `caddy_config`, a private network, and starts both containers.
3. Caddy sees `DOMAIN=localhost`, creates a local certificate authority and issues itself a certificate.

Check it:

```bash
docker compose ps                     # both "running", app "healthy" after ~30 s
docker compose logs app               # prints the host link and the event code
curl -k https://localhost/healthz     # "ok"  (-k accepts the local certificate)
```

Open `https://localhost/` in a browser. Accept the certificate warning (local only), enter the event code and a name. Open `https://localhost/?host=<HOST_KEY>` in a private window: you're the host. Run a question between the two windows.

Prove persistence:

```bash
docker compose restart app
```

Reload both windows: the player keeps their name and score, the host stays host. Now stop everything but keep the data:

```bash
docker compose down                   # containers removed, volumes kept
docker compose up -d                  # back, with the same state
docker compose down -v                # removes the volumes too: a true reset
```

That's the whole operational model. Everything in Part B is the same commands on a remote machine.

## Part B: AWS

Variables used throughout. Set them once in your terminal:

```bash
export AWS_REGION=eu-west-1                 # pick a region near your players
export DOMAIN=afeehy.your-company.com       # the subdomain DevOps gave you
export MY_IP=$(curl -s https://checkip.amazonaws.com)/32   # for SSH access only from you
```

### B1. Firewall: a security group

A security group is the instance's firewall. Allow HTTPS and HTTP from everyone (HTTP is only used by Let's Encrypt and to redirect to HTTPS), and SSH from your IP only.

```bash
SG=$(aws ec2 create-security-group --group-name afeehy-hour \
      --description "Afeehy Hour game server" --query GroupId --output text)
aws ec2 authorize-security-group-ingress --group-id $SG --protocol tcp --port 443 --cidr 0.0.0.0/0
aws ec2 authorize-security-group-ingress --group-id $SG --protocol tcp --port 80  --cidr 0.0.0.0/0
aws ec2 authorize-security-group-ingress --group-id $SG --protocol tcp --port 22  --cidr $MY_IP
echo $SG
```

### B2. An SSH key pair

AWS generates the key; you keep the private half. Without it you can't log in to the instance.

```bash
aws ec2 create-key-pair --key-name afeehy-hour --query KeyMaterial --output text > ~/.ssh/afeehy-hour.pem
chmod 400 ~/.ssh/afeehy-hour.pem
```

### B3. Launch the instance

Find the current Amazon Linux 2023 image id (it changes as AWS patches it), then launch a `t3.small` (2 vCPU, 2 GB RAM). The 2 GB matters because the Go compiler runs on the instance during `docker compose build`.

```bash
AMI=$(aws ssm get-parameters \
      --names /aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64 \
      --query 'Parameters[0].Value' --output text)

ID=$(aws ec2 run-instances --image-id $AMI --instance-type t3.small \
      --key-name afeehy-hour --security-group-ids $SG \
      --block-device-mappings 'DeviceName=/dev/xvda,Ebs={VolumeSize=16,VolumeType=gp3}' \
      --tag-specifications 'ResourceType=instance,Tags=[{Key=Name,Value=afeehy-hour}]' \
      --query 'Instances[0].InstanceId' --output text)
echo $ID
aws ec2 wait instance-running --instance-ids $ID
```

### B4. A fixed public IP

An instance's default public IP changes when it stops. An Elastic IP is a fixed address you attach to it, so the DNS record stays valid.

```bash
ALLOC=$(aws ec2 allocate-address --domain vpc --query AllocationId --output text)
aws ec2 associate-address --instance-id $ID --allocation-id $ALLOC
IP=$(aws ec2 describe-addresses --allocation-ids $ALLOC --query 'Addresses[0].PublicIp' --output text)
echo $IP
```

### B5. DNS

The subdomain must point at `$IP` as an **A record** before Caddy can get a certificate.

- If DevOps controls the DNS: send them `afeehy.your-company.com  A  <IP>` and wait for them to add it.
- If the zone is in your own Route 53:

```bash
ZONE=$(aws route53 list-hosted-zones-by-name --dns-name your-company.com \
        --query 'HostedZones[0].Id' --output text)
aws route53 change-resource-record-sets --hosted-zone-id $ZONE --change-batch "{
  \"Changes\": [{\"Action\": \"UPSERT\", \"ResourceRecordSet\": {
    \"Name\": \"$DOMAIN\", \"Type\": \"A\", \"TTL\": 60,
    \"ResourceRecords\": [{\"Value\": \"$IP\"}]}}]}"
```

- **No domain yet?** Use an interim name that needs no DNS setup at all: `afeehy.<ip-with-dashes>.sslip.io`, for example `afeehy.34-252-155-119.sslip.io`. The public `sslip.io` service resolves it to that IP and Let's Encrypt issues a certificate for it. Switch to the real name later by editing `DOMAIN` in `.env` and running `docker compose up -d`.

Confirm from your laptop before going on (it can take a few minutes):

```bash
dig +short $DOMAIN        # must print the IP
```

### B6. Install Docker on the instance

Log in and install Docker and the Compose plugin. Amazon Linux 2023 ships Docker in its package repository but not the Compose plugin, which is downloaded from GitHub.

```bash
ssh -i ~/.ssh/afeehy-hour.pem ec2-user@$IP
```

On the instance:

```bash
sudo dnf install -y docker unzip
sudo systemctl enable --now docker
sudo usermod -aG docker ec2-user
sudo mkdir -p /usr/local/lib/docker/cli-plugins
sudo curl -SL https://github.com/docker/compose/releases/latest/download/docker-compose-linux-x86_64 \
     -o /usr/local/lib/docker/cli-plugins/docker-compose
# Compose's "build" needs the buildx plugin, which the Amazon Linux package leaves out
BUILDX=$(curl -s https://api.github.com/repos/docker/buildx/releases/latest | grep -o '"tag_name": *"[^"]*"' | cut -d'"' -f4)
sudo curl -SL "https://github.com/docker/buildx/releases/download/$BUILDX/buildx-$BUILDX.linux-amd64" \
     -o /usr/local/lib/docker/cli-plugins/docker-buildx
sudo chmod +x /usr/local/lib/docker/cli-plugins/docker-compose /usr/local/lib/docker/cli-plugins/docker-buildx
exit                      # log out so the docker group membership takes effect
```

### B7. Copy the deployment files

From your Mac. `scp` copies files over SSH.

```bash
cd ~/Downloads/afeehy-hour/deploy
ssh -i ~/.ssh/afeehy-hour.pem ec2-user@$IP 'mkdir -p ~/afeehy/app'
scp -i ~/.ssh/afeehy-hour.pem docker-compose.yml Caddyfile ec2-user@$IP:~/afeehy/
scp -i ~/.ssh/afeehy-hour.pem ~/Downloads/afeehy-hour-deploy.zip ec2-user@$IP:~/afeehy/
```

Note that `.env` is **not** copied. Secrets are typed on the server, so they never sit in a file on your laptop or in a chat.

### B8. Configure and start

```bash
ssh -i ~/.ssh/afeehy-hour.pem ec2-user@$IP
cd ~/afeehy && unzip -q afeehy-hour-deploy.zip -d app && rm afeehy-hour-deploy.zip
docker --version && docker compose version      # both answer: Docker is ready

cat > .env <<EOF
DOMAIN=afeehy.your-company.com
HOST_KEY=$(openssl rand -hex 16)
EVENT_CODE=AFEEHY26
EOF
chmod 600 .env
cat .env                                         # note the HOST_KEY: this is your host link

docker compose up -d --build
```

The first build downloads the Go image and compiles, about one to two minutes. Then:

```bash
docker compose ps                     # both running, app healthy
docker compose logs caddy | tail      # "certificate obtained successfully" for your domain
docker compose logs app  | tail       # host link, event code, "trusting ... TRUST_PROXY is set"
```

If Caddy logs certificate errors, the DNS record isn't pointing here yet or port 80 isn't open. Fix and `docker compose restart caddy`.

### B9. Verify from outside

From your Mac and ideally from a phone on mobile data:

```bash
curl -I https://$DOMAIN/healthz            # HTTP/2 200, strict-transport-security present
curl -sI https://$DOMAIN/ | grep -i content-security-policy
```

Open `https://$DOMAIN/` and join as a player. Open `https://$DOMAIN/?host=<HOST_KEY>` and run a test question. Then press **Clear players** so the room is clean for the event.

## Part C: operating it

All on the instance, in `~/afeehy`.

| Task | Command |
|---|---|
| See what's running | `docker compose ps` |
| Follow the server log | `docker compose logs -f app` |
| Restart the game (state kept) | `docker compose restart app` |
| Apply new application files | unzip into `app/`, then `docker compose up -d --build` |
| Change the event code or host key | edit `.env`, then `docker compose up -d` (sessions already marked host stay host until the data is reset) |
| Back up the results | `docker compose cp app:/data/afeehy-data.json ./results-$(date +%F).json` |
| Full reset of the game | `docker compose down -v && docker compose up -d` (new certificate request too, so do this sparingly) |
| Reset only the game data | `docker compose stop app && docker run --rm -v afeehy_data:/d alpine rm -f /d/afeehy-data.json && docker compose start app` |

Open browsers survive a restart. If you reset the data, every open page reloads itself to the join screen within a few seconds.

**Before the event:** start it at least 15 minutes early, open the host link on the three host devices, and test one question with a colleague on a phone.

## Part D: teardown

The security brief promises deletion right after the event. Take a backup of the results first if you want them.

On the instance:

```bash
cd ~/afeehy && docker compose cp app:/data/afeehy-data.json ./results.json   # optional
docker compose down -v
exit
```

From your Mac (the variables from Part B, or look the ids up in the console):

```bash
scp -i ~/.ssh/afeehy-hour.pem ec2-user@$IP:~/afeehy/results.json .   # optional
aws ec2 terminate-instances --instance-ids $ID
aws ec2 wait instance-terminated --instance-ids $ID
aws ec2 release-address --allocation-id $ALLOC
aws ec2 delete-security-group --group-id $SG
aws ec2 delete-key-pair --key-name afeehy-hour && rm ~/.ssh/afeehy-hour.pem
# remove the DNS A record (Route 53: same change-resource-record-sets call with "Action": "DELETE")
```

After `terminate-instances` nothing bills any more except an unreleased Elastic IP, hence the `release-address` line.

**Cost while running:** a `t3.small` is roughly USD 0.02 per hour plus a few cents per month for the 16 GB disk and the public IPv4 address. A full day of testing plus the event is well under a dollar.

## Part E: what DevOps will do differently

Their deployment on the company subdomain replaces Caddy with the company's own edge (load balancer or reverse proxy with the company certificate). What stays identical:

- the image built from the same application files,
- `HOST_KEY`, `EVENT_CODE` and `TRUST_PROXY=1` as environment variables,
- a persistent volume at `/data`,
- exactly one running instance,
- `GET /healthz` as the health check,
- their proxy must allow long-lived streaming responses on `GET /events` without buffering and with an idle timeout above 60 seconds.

If their proxy drops idle connections after, say, 60 seconds, the page shows "Reconnecting" briefly and recovers by itself, but it's worth asking them to raise it. The server sends a heartbeat every 15 seconds precisely so proxies see traffic.

## Appendix: other AWS options and what they'd need

| Option | What changes | Why not for this |
|---|---|---|
| **Lightsail** | same as EC2 with a simpler console and flat price; Docker install identical | fine alternative; fewer knobs, same steps |
| **ECS on Fargate + ALB** | task definition, ALB with idle timeout raised to ≥ 120 s, EFS file system mounted at `/data`, ACM certificate, desired count fixed at 1 | three more services to configure for the same result; EFS adds latency to the one-second save |
| **App Runner** | push image to ECR, point App Runner at it | requests are cut at 120 s, so the live stream reconnects constantly; no persistent disk |
| **Elastic Beanstalk (single Docker)** | zip with a `Dockerrun.aws.json` | still one EC2 instance underneath, with extra layers to learn |
| **Lambda / API Gateway** | — | the server holds state and open streams; not a fit |

## Streaming test (run after any deployment)

The page is useless if the proxy in front holds back the live stream, and a plain page load won't reveal that. This does:

```bash
T=$(curl -s -X POST https://$DOMAIN/api -d '{"op":"hello","eventCode":"<EVENT_CODE>"}' | sed 's/.*"token":"\([^"]*\)".*/\1/')
timeout 5 curl -s -N "https://$DOMAIN/events?token=$T" | head -c 100
```

You must see `event: snapshot` within a second or two. If nothing prints in 5 seconds, the proxy is buffering and must be told to stream `text/event-stream` responses unbuffered (for Caddy: `flush_interval -1`; for nginx: `proxy_buffering off`). Then delete the probe session's player entry or press **Clear players** before the event.

## Troubleshooting

| Symptom | Likely cause | Check |
|---|---|---|
| `ssh` hangs or times out, though it worked a minute ago | port 22 not open from your current IP. On a VPN or Cloudflare WARP the address rotates every few minutes | `aws ec2 authorize-security-group-ingress --group-id $SG --protocol tcp --port 22 --cidr $(curl -s https://checkip.amazonaws.com)/32` and retry |
| Caddy: "could not get certificate" | DNS not pointing here yet, or port 80 blocked | `dig +short $DOMAIN`; security group rule for 80 |
| Browser: "Can't reach the game server" | app container not running | `docker compose ps`, `docker compose logs app` |
| Page stuck on "Getting Afeehy Hour ready…", strip says "Connecting" | the proxy in front buffers the `/events` stream (Cloudflare quick tunnels over HTTP/2 do this) | run the streaming test below; the snapshot must arrive within 2 s |
| Status strip stuck on "Reconnecting" | proxy killing idle `/events` connections | raise the proxy's idle timeout above 60 s; the app sends a heartbeat every 15 s |
| Players see "Too many tries" though codes are right | `TRUST_PROXY` missing, so everyone shares the proxy's IP | `docker compose logs app \| grep Proxy` must say "trusting" |
| `docker compose build` killed | instance too small for the compiler | use `t3.small` or larger, or build on your Mac and push the image to ECR |
| `compose build requires buildx 0.17.0 or later` | buildx plugin missing (Amazon Linux's Docker package) | install it as in B6, or build once with `DOCKER_BUILDKIT=0 docker build -t afeehy-app ./app` and start with `docker compose up -d --no-build` |
