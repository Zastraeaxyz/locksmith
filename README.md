# Locksmith

A small Discord bot for ChastiSafe locks. Each member stores their own
developer code once, then runs `/lock` commands against their own account. The
bot never shares one member's code with another.

The ChastiSafe client lives in
[`github.com/zastraeaxyz/chastisafe-go`](https://github.com/zastraeaxyz/chastisafe-go).

## Commands

| Command | What it does |
| --- | --- |
| `/login` | Opens a private modal and stores your ChastiSafe developer code, encrypted |
| `/logout` | Removes your saved code and draw subscription |
| `/lock status` | Shows your active ChastiSafe locks |
| `/lock draw` | Draws the next card from your active card lock |
| `/lock notify [enabled]` | Turns draw notifications on (default) or off |

`/login` uses a Discord modal: the code is entered in a private form, the reply
is ephemeral, and the value is never echoed. Discord does not offer masked
password inputs in modals, so this is the closest equivalent.

## Notifications

`/lock notify` records the member and their current card lock. A scheduler
checks each subscription against the lock's draw schedule and sends a DM when a
new draw is available. After a notification it waits for the member to draw,
then schedules the next check from the lock's `wait` value, so it does not poll
ChastiSafe constantly.

## Configuration

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `DISCORD_TOKEN` | yes | | Bot token |
| `GUILD_ID` | yes | | The one server the bot serves |
| `DATABASE_PATH` | no | `./data/locksmith.db` | SQLite database |
| `STORE_KEY` | no | generated key file | 32-byte base64 or hex key for encrypted codes |
| `CHASTISAFE_BASE_URL` | no | `https://chastisafe.com/api-v1` | API root |
| `NOTIFY_INTERVAL` | no | `30s` | Scheduler wake-up interval |
| `PORT` | no | `8080` | Health server port |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn`, `error` |

When `STORE_KEY` is empty the bot creates `<database dir>/locksmith.key` with
mode 0600. Back up that file with the database; losing it makes stored codes
unreadable.

## Development

```sh
cp .env.example .env   # fill in DISCORD_TOKEN
go run .
```

## Deploy

The bot deploys to the `locksmith` service in the Railway `zxyz` project from
the repo's Dockerfile. SQLite lives on a volume mounted at `/data`.

```sh
railway up
```
