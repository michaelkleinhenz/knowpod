# Operations

Running knowpod-service in production. All settings are environment variables; the full
list is in the [README](../README.md#configuration).

## What it needs

| Dependency | Notes |
|---|---|
| MongoDB 7 | Replica set recommended (enables transactions). A standalone server works; the service logs a warning. |
| S3 bucket | An existing Amazon S3 bucket in any region. The service doesn't create it. |
| Persistent volume | Mounted at `UPLOAD_DIR` (`/data/uploads` in the image). |
| TLS | The service speaks plain HTTP. Put it behind a load balancer or reverse proxy that terminates HTTPS and sets `X-Forwarded-Proto: https`. Device tokens, admin tokens and web UI session cookies are bearer credentials. |

## Railway

The service is deployed on [Railway](https://railway.com). `railway.toml` in the repository
root sets the build (the root `Dockerfile`) and the deploy health check (`/healthz`).
Everything else is configured in the Railway dashboard:

1. **MongoDB.** Add a MongoDB service to the project.
2. **App service** from this repository. Railway picks up `railway.toml`.
3. **Volume.** Attach a volume to the app service with mount path `/data/uploads` (the
   `UPLOAD_DIR` of the image). Without it, uploads in progress and recordings not yet
   archived are lost on every redeploy.
4. **Variables** on the app service:

   | Variable | Value |
   |---|---|
   | `MONGO_URI` | `${{MongoDB.MONGO_URL}}` (reference to the MongoDB service's variable) |
   | `MONGO_DATABASE` | `knowpod` |
   | `ADMIN_EMAIL`, `ADMIN_PASSWORD` | The default web UI login |
   | `AWS_S3_BUCKET_NAME`, `AWS_DEFAULT_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | The S3 bucket and an IAM user's keys (see [S3](#s3)) |
   | `RAILWAY_RUN_UID` | `0` (see below) |
   | `ADMIN_TOKEN` | Optional, for scripts |
   | `POCKET_WEBHOOK_SECRET`, `POCKET_API_KEY` | Optional, for the [Pocket integration](#pocket-integration) |

   Don't set `PORT`; Railway provides it and the service listens on it.
5. **Networking.** Generate a public domain for the app service. Railway terminates HTTPS
   and sets `X-Forwarded-Proto` and the client IP headers that the session cookie and the
   login rate limit rely on.

**Why `RAILWAY_RUN_UID=0`:** Railway mounts volumes owned by root, and the image runs as the
unprivileged user `app` (uid 10001). Without the variable, the service can't write to
`/data/uploads`: uploads fail with permission errors.

**One replica.** Keep the app service at one replica; the spool is local to the instance
(see [Scaling](#scaling-and-the-spool)). A Railway service with a volume is limited to one
replica anyway.

If Railway's MongoDB runs as a standalone server rather than a replica set, the service
logs `MongoDB is not a replica set member` at startup. That's fine; nothing uses
transactions yet.

## Startup checks

On start the service validates the configuration, connects to MongoDB and creates any
missing collections and indexes, checks that the bucket is reachable (`HeadBucket`), and
creates the spool directory. If any of these fails it logs the reason and exits with
status 1, so misconfiguration shows up at deploy time.

## S3

**IAM policy** for the service's identity (instance role, task role or access keys):

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:ListBucket"],
      "Resource": "arn:aws:s3:::knowpod-audio"
    },
    {
      "Effect": "Allow",
      "Action": ["s3:PutObject", "s3:GetObject", "s3:DeleteObject"],
      "Resource": "arn:aws:s3:::knowpod-audio/*"
    }
  ]
}
```

`s3:ListBucket` is needed for the startup check. If you set `AWS_S3_PREFIX`, you can narrow the
object resource to `arn:aws:s3:::knowpod-audio/<prefix>*`.

**Credentials** are resolved by the AWS SDK's default chain. That includes the environment
variables `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_SESSION_TOKEN`, `AWS_PROFILE`,
web identity (EKS), and ECS/EC2 roles. The region must be set with `AWS_DEFAULT_REGION` (or
`AWS_REGION`, which takes precedence).

**Bucket settings** (recommended): block all public access, keep default encryption on, and
consider a lifecycle rule that moves older recordings to a cheaper storage class. The
service only reads objects when an admin downloads them.

## Scaling and the spool

The spool holds uploads in progress and received recordings not yet archived. Normally it
stays small. If S3 is unreachable, received recordings pile up there until archiving
succeeds or gives up, so size the volume for the audio you might receive during an outage.

Run **one instance**. Upload chunks go to local disk and appends are coordinated
in-process, so several instances behind a plain load balancer would split one upload
across disks. Scaling out would need routing each device to a fixed instance with its own
volume, or moving the spool to shared storage.

Long uploads mean long requests. Make sure the proxy in front allows large request bodies
and doesn't time out slow uploads too early. The protocol resumes after a cut connection,
but every cut costs a round trip.

## Web UI sign-in

People sign in to the web UI with email and password. There is one account, the admin:

1. **Default login.** `ADMIN_EMAIL` and `ADMIN_PASSWORD` from the environment. Use a strong
   password; `docker compose` refuses to start without one.
2. **Change the password** under **Account** in the UI. The new password is stored in
   MongoDB (bcrypt hash, collection `users`) and from then on replaces `ADMIN_PASSWORD`,
   which no longer works for this email. Other signed-in browsers are signed out.

Sessions are stored in MongoDB and last `SESSION_TTL` (7 days by default). Expired sessions
are deleted automatically. The session cookie is `HttpOnly` and `SameSite=Strict`, and it's
marked `Secure` when the request came in over HTTPS (directly, or with
`X-Forwarded-Proto: https` from the proxy). Login attempts are limited to 10 per minute per
client IP.

**Forgotten password.** Delete the stored account; the default login from the environment
then works again:

```js
db.users.deleteOne({ email: "admin@example.com" })
```

**Changing `ADMIN_EMAIL`** after the password was changed leaves the old account in the
database, still able to sign in with its stored password. Delete it as above if it
shouldn't.

## Pocket integration

Recordings made with a [Pocket](https://heypocket.com) recorder can flow into knowpod.
Pocket calls a webhook whenever something happens to a recording; knowpod then downloads
the recording's audio through the Pocket API and archives it in S3 like the other
recordings.

**Setup**

1. Open **Status** in the knowpod web UI and copy the **Webhook URL**
   (`https://<your domain>/api/v1/webhooks/pocket`).
2. In the Pocket app's integrations settings, add a webhook with that URL. Pocket shows the
   webhook's **signing secret** once; set it as `POCKET_WEBHOOK_SECRET`.
3. Create a Pocket **API key** (`pk_…`) and set it as `POCKET_API_KEY`.
4. Redeploy. The Pocket card on the Status page shows both as configured.

Until both variables are set, the webhook answers `503` and nothing is stored.

**What happens**

- Each webhook's signature (`X-HeyPocket-Signature`, HMAC-SHA256 over
  `<X-HeyPocket-Timestamp>.<body>`) is verified, and its timestamp must be within 5 minutes
  of the server clock. Unsigned or stale requests get `401`.
- Every event that names a recording (`recording.created`, `transcription.completed`,
  `summary.completed`, …) queues that recording **once**; Pocket delivers at least once and
  later events for the same recording are ignored. `recording.deleted` is ignored: the
  archived copy is kept.
- The worker asks the Pocket API for a download URL
  (`GET /public/recordings/{id}/audio-url`), streams the file to the spool, and archives
  it: WAV is transcoded to FLAC, other formats (e.g. MP3, M4A) are stored as they are.
  Failures, including audio that isn't available yet, are retried with backoff and end in
  `failed` after `WORKER_MAX_ATTEMPTS`.
- Pocket recordings appear in `/api/v1/admin/recordings` with `deviceId` `pocket`,
  `source` `pocket`, the Pocket `title`, and the Pocket recording ID as `recordingId`.

Pocket's transcripts, summaries and action items in the webhook payload are not stored.

**Log messages:** `pocket recording queued`, `pocket audio fetched`, and
`pocket webhook rejected` (signature problems, with the reason).

## Provisioning devices

**In the web UI**, open **Devices**:

- **Add device:** enter a name. The device's API token appears in its row with a copy
  button. Configure it on the recorder right away: the token is shown only once and is gone
  when you leave the page (only its hash is stored).
- **New token:** issues a replacement token, e.g. when the old one was lost. The old token
  stops working immediately; the device keeps its recordings.
- **Remove:** revokes the device. Its token stops working and it disappears from the list;
  recordings it already uploaded are kept.

**From scripts**, use the admin API with `ADMIN_TOKEN` as a bearer token. `ADMIN_TOKEN` is
optional; set it to a long random value, e.g. `openssl rand -base64 32`. When it's empty,
only signed-in web UI sessions can use the admin API.

```bash
API=https://knowpod.example.com/api/v1
ADMIN="Authorization: Bearer $ADMIN_TOKEN"

# Register a device. The token is returned only once; put it on the gadget.
curl -s -X POST -H "$ADMIN" -d '{"name":"recorder-kitchen"}' $API/admin/devices

# List devices (shows lastSeenAt and revokedAt).
curl -s -H "$ADMIN" $API/admin/devices

# Issue a new token for a device (the old one stops working).
curl -s -X POST -H "$ADMIN" $API/admin/devices/<deviceId>/token

# Remove (revoke) a device. Its recordings are kept.
curl -s -X DELETE -H "$ADMIN" $API/admin/devices/<deviceId>
```

## Monitoring

- **`GET /healthz`** returns `200` when MongoDB answers and `503` otherwise. It doesn't
  check S3. The **Status** page in the web UI shows the same result.
- **Logs** are JSON lines on stdout. Useful messages:

  | Message | Meaning |
  |---|---|
  | `recording archived` | A recording reached `stored` (includes WAV and FLAC sizes and processing time) |
  | `stage failed, will retry` | Archiving failed and will be retried; see `err` |
  | `stage failed permanently` | A recording became `failed` after all attempts |
  | `purged stale uploads` | Abandoned uploads were deleted |
  | `request failed` | Unexpected error behind a `500` response |

- **Recording state:** `GET /api/v1/admin/recordings?status=failed` lists failed
  recordings with their `lastError`. A growing number of `received` recordings means
  archiving is falling behind or failing.

## Recovering failed recordings

A recording can fail for two reasons, which `lastError` distinguishes:

- **The device sent something that isn't a supported WAV.** Nothing was kept, so there is
  nothing to recover on the server.
- **Archiving failed** (e.g. S3 was unreachable longer than the retries lasted). The WAV is
  still in the spool as `<id>.wav`, where `<id>` is the recording's `id` (the device's
  `uploadId`, not its `recordingId`). Once the cause is fixed, put the recording back in the
  queue with `mongosh`:

  ```js
  db.recordings.updateOne(
    { _id: "<id>", status: "failed" },
    { $set: { status: "received", attempts: 0, notBefore: new Date() }, $unset: { lastError: "" } }
  )
  ```

  The worker picks it up within `WORKER_POLL_INTERVAL`.

## Backups

MongoDB holds the metadata (devices, recording states, S3 keys) and the admin's stored
password hash; back it up as usual. The
audio lives in S3, where you can enable versioning or replication. The spool is only a
transit area, but it does contain received recordings until they are archived, so don't
wipe it while `received` recordings exist.

## Known limitations

- Single instance only (see [Scaling](#scaling-and-the-spool)).
- One web UI account (the admin); there is no user management.
- The web UI covers sign-in, password change, devices and the Status page; recordings are
  only accessible through the admin API.
- The client IP for the login rate limit is taken from `X-Forwarded-For` / `X-Real-IP`.
  Without a proxy that sets these, clients can spoof them and bypass the limit.
- No API to delete recordings or their objects.
- No retry endpoint for failed recordings; use the `mongosh` update above.
- `/healthz` doesn't cover S3.
- FLAC compression is weaker than the reference encoder (see
  [Architecture](architecture.md#flac-encoding-audioflacgo)).
