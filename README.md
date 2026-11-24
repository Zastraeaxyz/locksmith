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
| `/lock status [public]` | Shows your active ChastiSafe locks with draws available, deck, timers, and check-ins |
| `/lock load link:<url>` | Starts a lock from a ChastiSafe share link; asks privately for the lock code and keyholder passcode |
| `/lock draw` | Opens a public draw board with Draw and Stop buttons; only you and your keyholder can press them |
| `/lock checkin [ref] [public]` | Records a required check-in on demand |
| `/lock share ref:<template>` | Keyholders: posts a lock template with Load with Locksmith and Open in ChastiSafe buttons |
| `/lock notify [enabled]` | Turns draw and check-in notifications on (default) or off |
| `/lock log [full] [ref] [public]` | Recent draws from the last 5 minutes, the full log, a chosen lock, or a public reply |
| `/history [include_test] [include_old] [public]` | Your completed locks, newest first |
| `/profile view [user]` | Shares a member's markdown profile in the chat |
| `/profile edit` | Opens a prefilled modal to set your profile |
| `/rules view [user]` | Shares a member's personal rules of play |
| `/rules edit` | Opens a prefilled modal to set your rules |
| `/kinks view [user]` | Shares a member's kinks document |
| `/kinks edit` | Opens a prefilled modal to set your kinks |
| `/locksmith` | Posts a short overview with the recommended user-install link and a link to this repository |

`/login` uses a Discord modal: the code is entered in a private form, the reply
is ephemeral, and the value is never echoed. Discord does not offer masked
password inputs in modals, so this is the closest equivalent.

## Loading and sharing locks

Keyholders create and edit lock templates on chastisafe.com, then share one with
`/lock share ref:<template>`. The post includes the lock name, type, cards or
timer, check-ins, allowed purposes, and a note when a passcode is required. It
has two buttons:

- **Load with Locksmith** opens a private form for the lock code and an optional
  keyholder passcode, then starts the lock for the member who pressed it. Draws,
  reminders, and logs then work in Discord. A member who does not have Locksmith
  gets a private prompt with an install button instead.
- **Open in ChastiSafe** opens `https://chastisafe.com/#l/<template>` so a
  lockee can load the lock without the bot.

A lockee can also run `/lock load link:<share link>` with a ChastiSafe share
link or a bare template ID. The form suggests a random four digit code. Set the
device to that code, or replace it with your own, then submit.

Templates that use a keyholder passcode are supported: enter the passcode in the
form. The share post marks it as required so the lockee knows to ask for it.

## Notifications

`/lock notify` records the member and the lock to watch: their active card lock
when they have one, otherwise a lock with a check-in schedule. The scheduler
checks each subscription against the lock's draw schedule and sends a DM when
one or more draws become available. The DM lists draws available, cards left,
greens needed, and the next draw time. After a notification the bot waits for
the member to draw, then schedules the next check from the lock's `wait`
value, so it does not poll ChastiSafe constantly.

The same subscription also watches the lock's check-in schedule. The bot sends a
first reminder `CHECKIN_LEAD_TIME` before the check-in is due, an unskippable
reminder five minutes before, and one more when the check-in is overdue. Each
reminder has a **Check-in** button that records the check-in from Discord; the
button is removed after it succeeds. Reminders are tracked per check-in cycle, so
a member who ignores one is not spammed and a member who checks in gets the next
cycle's reminders as usual. If a reminder never arrives, or notifications are
off, `/lock checkin` records the same check-in on demand.

`/lock log` shows pick events from the last five minutes by default. Add
`full:true` for every event, `ref:` to pick a running or previous lock, and
`public:true` to post the reply in the channel.

`/history` lists your completed locks, newest first, with duration, unlock
reason, keyholder, and rating. `TEST` locks and locks older than a year are
hidden by default; add `include_test:true` or `include_old:true` to bring them
back, and `public:true` to post the reply in the channel. `/lock status` also
accepts `public:true`.

`/lock draw` posts a public draw board so the channel can watch the session.
Draw and Stop are available to the lockee and their keyholder; anyone else
gets a private refusal. The board keeps a running log of the cards drawn, a
running total, and a countdown of the cards left in the deck. The buttons are
removed when the session stops or the draws run out.

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

## Profiles, rules, and kinks

`/profile`, `/rules`, and `/kinks` are Locksmith documents, separate from
ChastiSafe. Each member can keep one profile, one rules-of-play, and one kinks
document as plain markdown.

- `/profile view [user]`, `/rules view [user]`, and `/kinks view [user]` post
  the document publicly in an embed.
- `/profile edit`, `/rules edit`, and `/kinks edit` open a private modal
  prefilled with the current markdown. Submitting an empty modal clears the
  document.
- Documents are limited to 4000 characters, which is Discord's modal limit.
- Mentions in a document are suppressed, so `@everyone` cannot ping.
- `user` accepts a Discord user ID, a ChastiSafe username such as `zastraea`
  or `zastraea#0`, or the Discord handle saved when the document was last
  edited. ChastiSafe usernames are tried first.

## Configuration

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `DISCORD_TOKEN` | yes | | Bot token |
| `GUILD_ID` | no | | Home server for instant guild-scoped commands; global commands are always registered |
| `DATABASE_PATH` | no | `./data/locksmith.db` | SQLite database |
| `STORE_KEY` | no | generated key file | 32-byte base64 or hex key for encrypted codes |
| `CHASTISAFE_BASE_URL` | no | `https://chastisafe.com/api-v1` | API root |
| `NOTIFY_INTERVAL` | no | `30s` | Scheduler wake-up interval |
| `CHECKIN_LEAD_TIME` | no | `1h` | First check-in reminder lead; the 5 minute and overdue reminders always fire |
| `PORT` | no | `8080` | Health server port |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn`, `error` |
| `UMAMI_SITE_ID` | no | | Umami website ID for anonymous usage statistics |
| `UMAMI_ENDPOINT` | no | | Umami base URL, for example `https://umami.example.com` |

When `STORE_KEY` is empty the bot creates `<database dir>/locksmith.key` with
mode 0600. Back up that file with the database; losing it makes stored codes
unreadable.

## Analytics

When both `UMAMI_SITE_ID` and `UMAMI_ENDPOINT` are set, Locksmith sends
anonymous usage events to Umami: one event per handled command, modal, and
component, such as `/lock status` or `component draw`. Events carry only that
label. Discord user IDs, usernames, guild IDs, lock IDs, profiles, rules, kinks,
and stored codes are never sent, so statistics cannot be tied to a member.

The login funnel is explicit: `/login` counts modal opens, `modal login` counts
submissions, and the handler reports `login success` or `login failed: <reason>`
where the reason is one of `rejected`, `not owner`, `unconfigured`,
`unreachable`, `rate limited`, or `empty code`.

Statistics are best effort: events are queued and posted in the background, and
a slow or unavailable Umami instance never delays or fails an interaction. Local
runs stay out of production statistics when the two variables are empty. Umami
drops traffic it flags as bots, so the client sends a browser-shaped user agent
with a `Locksmith/1` suffix.

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
