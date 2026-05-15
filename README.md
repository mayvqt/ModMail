# ModMail

A self-hosted Discord modmail bot written in Go with SQLite persistence and Docker deployment.

## Features

- Users DM the bot to open or continue a ticket
- One open ticket per user (DB-enforced)
- Staff ticket channels are created under a configured category
- Optional staff-role-only access to ticket channels
- Bidirectional relay:
  - User DM -> staff channel
  - Staff message -> user DM
- Close tickets via prefix command or slash command:
  - `!close [reason]` and `/close reason:<reason>`
- Reopen closed tickets from the same ticket channel:
  - `!reopen` and `/reopen`
- Add staff-only internal notes:
  - `!note <text>` and `/note text:<text>`
- Show current ticket details:
  - `!ticket` and `/ticket`
- Transcript export on close (sent to `LOG_CHANNEL_ID` when configured)
- Optional automatic channel deletion after close
- Relayed messages suppress Discord mentions to avoid accidental `@everyone`, role, or user pings
- Sticker-only DMs and replies are preserved as sticker labels in relayed messages
- SQLite WAL mode + busy timeout tuning for better runtime reliability
- Docker-ready

## Required Discord Setup

1. Create a bot in the Discord Developer Portal.
2. Enable **Message Content Intent** for prefix commands.
3. Invite with permissions:
   - View Channels
   - Send Messages
   - Manage Channels
   - Read Message History
4. Copy IDs:
   - `GUILD_ID`
   - `STAFF_CATEGORY_ID`
   - `STAFF_ROLE_ID` (recommended)
   - `LOG_CHANNEL_ID` (optional)

## Environment

Copy and edit:

```bash
cp .env.example .env
```

Variables:

- `IMAGE_REPOSITORY` (for Compose image name, used by `docker compose build/push`; example `ghcr.io/mayvqt/modmail`)
- `IMAGE_TAG` (for Compose image tag; example `latest`)
- `MODMAIL_DATA_DIR` (host path mounted to `/data`; default `./data`, Unraid example `/mnt/user/appdata/modmail`)
- `DISCORD_TOKEN` (required)
- `GUILD_ID` (required)
- `STAFF_CATEGORY_ID` (required)
- `STAFF_ROLE_ID` (optional, recommended)
- `LOG_CHANNEL_ID` (optional)
- `COMMAND_PREFIX` (default `!`)
- `DB_PATH` (default `/data/modmail.sqlite`)
- `ENABLE_SLASH_COMMANDS` (default `true`)
- `AUTO_DELETE_CLOSED_TICKET_AFTER` (default `0s`, examples: `30m`, `24h`)
- `STAFF_IDENTITY` (default `anonymous`; one of `anonymous`, `named`, `role`)
- `STAFF_REPLY_LABEL` (default `Moderator`)
- `LOG_LEVEL` (default `info`; one of `debug`, `info`, `warn`, `error`)

## Local Run

```bash
export $(grep -v '^#' .env | xargs)
go run ./cmd/bot
```

## Docker Run

```bash
docker compose up --build -d
```

Data is stored in `${MODMAIL_DATA_DIR}` (defaults to `./data`).

## Build + Push Image

1. Authenticate to your registry (example for GHCR):

```bash
echo "<github_token>" | docker login ghcr.io -u <github_username> --password-stdin
```

2. Set image values in `.env`:

```env
IMAGE_REPOSITORY=ghcr.io/mayvqt/modmail
IMAGE_TAG=latest
```

3. Build and push:

```bash
docker compose build
docker compose push
```

## Unraid Notes

- Set `MODMAIL_DATA_DIR=/mnt/user/appdata/modmail` in `.env`.
- Keep `DB_PATH=/data/modmail.sqlite` (already correct for the container mount).
- Use host networking only if your Discord environment requires it; bridge works for normal operation.

## Behavior Notes

- If a user already has an open ticket, the bot reuses it instead of opening another.
- Closing a ticket changes DB status to `closed`, not deleted.
- Close reasons are saved in SQLite, sent to the user, and included in the log channel message.
- Internal notes are posted in the ticket channel and mirrored to `LOG_CHANNEL_ID` when configured. They are not sent to the user.
- Staff replies are only relayed while a ticket is open. Reopen a closed ticket before replying.
- Empty DMs do not open tickets.
- Reopen only works when no other open ticket exists for that user.
- Automatic deletion re-checks the ticket status before deleting, so reopened tickets are not deleted by an old close timer.
- Transcript export currently includes up to the latest 500 messages in the ticket channel.
- Docker Compose stores SQLite data in the host path set by `MODMAIL_DATA_DIR` (default `./data`). Make sure that directory is writable by the non-root container user.

## Testing

```bash
GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./...
```
