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
  - `!close` and `/close`
- Reopen closed tickets from the same ticket channel:
  - `!reopen` and `/reopen`
- Transcript export on close (sent to `LOG_CHANNEL_ID` when configured)
- Optional automatic channel deletion after close
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

- `DISCORD_TOKEN` (required)
- `GUILD_ID` (required)
- `STAFF_CATEGORY_ID` (required)
- `STAFF_ROLE_ID` (optional, recommended)
- `LOG_CHANNEL_ID` (optional)
- `COMMAND_PREFIX` (default `!`)
- `DB_PATH` (default `/data/modmail.sqlite`)
- `ENABLE_SLASH_COMMANDS` (default `true`)
- `AUTO_DELETE_CLOSED_TICKET_AFTER` (default `0s`, examples: `30m`, `24h`)

## Local Run

```bash
export $(grep -v '^#' .env | xargs)
go run ./cmd/bot
```

## Docker Run

```bash
docker compose up --build -d
```

Data is stored in `./data` by default.

## Behavior Notes

- If a user already has an open ticket, the bot reuses it instead of opening another.
- Closing a ticket changes DB status to `closed`, not deleted.
- Reopen only works when no other open ticket exists for that user.
- Transcript export currently includes up to the latest 500 messages in the ticket channel.

## Testing

```bash
go test ./...
```
