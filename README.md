# Locksmith

A small Discord bot for ChastiSafe locks. Each member stores their own
developer code once, then runs `/lock` commands against their own account. The
bot never shares one member's code with another.

Locksmith supports both install types: server install and Discord's native
user install. With user install, the same commands work in DMs with the bot, in
group DMs, and in servers where Locksmith is not a member.

The ChastiSafe client lives in
[`github.com/zastraeaxyz/chastisafe-go`](https://github.com/zastraeaxyz/chastisafe-go).

## Install

Add Locksmith to a server. This installs the bot and the slash commands with
View Channels, Send Messages, and Embed Links:

```
https://discord.com/oauth2/authorize?client_id=1574588415783997481&integration_type=0&scope=bot%20applications.commands&permissions=19456
```

Install Locksmith to your Discord account. This is Discord's User Install: the
same commands work in DMs, group DMs, and servers where Locksmith is not a
member:

```
https://discord.com/oauth2/authorize?client_id=1574588415783997481&integration_type=1&scope=applications.commands
```

The easiest way to share either install path is `/locksmith`: it posts a short
public embed with the user-install button and a link to this repository.

## Commands

| Command | What it does |
| --- | --- |
| `/login` | Opens a private modal and stores your ChastiSafe developer code, encrypted |
| `/logout` | Removes your saved code and draw subscription |
| `/lock status` | Shows your active ChastiSafe locks with draws available, deck, timers, and check-ins |
| `/lock draw` | Opens a public draw board with Draw and Stop buttons; only you and your keyholder can press them |
| `/lock notify [enabled]` | Turns draw notifications on (default) or off |
| `/lock log [full] [ref] [public]` | Recent draws from the last 5 minutes, the full log, a chosen lock, or a public reply |
| `/locksmith` | Posts a short overview with the recommended user-install link and a link to this repository |

`/login` uses a Discord modal: the code is entered in a private form, the reply
is ephemeral, and the value is never echoed. Discord does not offer masked
password inputs in modals, so this is the closest equivalent.

## Notifications

`/lock notify` records the member and their current card lock. A scheduler
checks each subscription against the lock's draw schedule and sends a DM when
one or more draws become available. The DM lists draws available, cards left,
greens needed, and the next draw time. After a notification the bot waits for
the member to draw, then schedules the next check from the lock's `wait`
value, so it does not poll ChastiSafe constantly.

`/lock log` shows pick events from the last five minutes by default. Add
`full:true` for every event, `ref:` to pick a running or previous lock, and
`public:true` to post the reply in the channel.

`/lock draw` posts a public draw board so the channel can watch the session.
Draw and Stop are available to the lockee and their keyholder; anyone else
gets a private refusal. The board keeps a running log of the cards drawn, and
the buttons are removed when the session stops or the draws run out.

## Contexts

User install authorizes the app to your Discord account and enables the same
commands in bot DMs, group DMs, and servers where Locksmith has not been added.

Commands are registered globally with guild, bot DM, and private channel
contexts. Global command changes can take up to an hour to appear after a
deploy. When `GUILD_ID` is set, the bot also registers guild-scoped commands in
that server so updates there are immediate.

In a DM only the lockee can press the draw buttons because the keyholder
cannot see the DM. In a group DM or a server, the keyholder can see and press
them.

## Configuration

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `DISCORD_TOKEN` | yes | | Bot token |
| `GUILD_ID` | no | | Home server for instant guild-scoped commands; global commands are always registered |
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
