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

The easiest way to share either install path is `/help`: it posts the guide, the
user-install button, and a link to this repository.

## Commands

| Command | What it does |
| --- | --- |
| `/login` | Opens a private modal and stores your ChastiSafe developer code, encrypted |
| `/logout` | Removes your saved code and draw subscription |
| `/lock status [public]` | Shows your active ChastiSafe locks with draws available, deck, timers, and check-ins |
| `/lock load link:<url>` | Starts a lock from a ChastiSafe share link; generates the device code from your ChastiSafe code settings and asks privately for the keyholder passcode |
| `/lock draw` | Opens a public draw board with Draw and Stop while the lock runs, and Open, Put green back, Reset, and Let keyholder decide when it is ready to unlock; only you and your keyholder can press them |
| `/lock open [ref]` | Opens a ready lock and reveals the device code; the reply is private in servers and a normal message in DMs |
| `/lock checkin [ref] [public]` | Records a required check-in on demand; `ref` suggests locks that need one |
| `/lock share ref:<template>` | Keyholders: posts a lock template with Load with Locksmith, Simulation (card locks), and Open in ChastiSafe buttons; `ref` suggests your own templates |
| `/lock notify [enabled]` | Turns draw and check-in reminders on (default) or off for locks loaded in the ChastiSafe app; locks loaded with `/lock load` are watched automatically |
| `/lock log [full] [ref] [public]` | Recent draws from the last 5 minutes, the full log, a chosen lock, or a public reply; `ref` suggests active and previous locks |
| `/history [include_test] [include_old] [public]` | Your completed locks, newest first, with lock codes in a private reply |
| `/profile view [user]` | Shares a member's markdown profile in the chat |
| `/profile edit` | Opens a prefilled modal to set your profile |
| `/rules view [user]` | Shares a member's personal rules of play |
| `/rules edit` | Opens a prefilled modal to set your rules |
| `/kinks view [user]` | Shares a member's kinks document |
| `/kinks edit` | Opens a prefilled modal to set your kinks |
| `/changelog [public]` | Shows recent Locksmith changes, newest first |
| `/help` | Posts the guide with the recommended user-install link and a link to this repository |

Every `ref` option autocompletes. `/lock checkin` lists your locks that need a
check-in, `/lock open` lists ready locks, `/lock share` lists your own templates,
and `/lock log` lists active and previous locks. Typing a name or ID still
works.

`/login` uses a Discord modal: the code is entered in a private form, the reply
is ephemeral, and the value is never echoed. Discord does not offer masked
password inputs in modals, so this is the closest equivalent.

## Loading and sharing locks

Keyholders create and edit lock templates on chastisafe.com, then share one with
`/lock share ref:<template>`. The post includes the lock name, type, cards or
timer, check-ins, allowed purposes, and a note when a passcode is required. It
has two buttons:

- **Load with Locksmith** opens a private form for the optional keyholder
  passcode, then starts the lock for the member who pressed it. Locksmith
  generates the device code from the member's ChastiSafe code settings, so the
  lockee cannot pick a code they would remember, and shows it in the private
  reply so they can set the device. Draws, reminders, and logs then work in
  Discord. ChastiSafe withholds the code while the lock runs, and Locksmith can
  show it again when the lock is done. A member who does not have Locksmith gets
  a private prompt with an install button instead.
- **Open in ChastiSafe** opens `https://chastisafe.com/#l/<template>` so a
  lockee can load the lock without the bot.

A lockee can also run `/lock load link:<share link>` with a ChastiSafe share
link or a bare template ID. The form asks only for the keyholder passcode when
the template has one. The reply shows the generated device code, so the lockee
can set the device and then forget it.

## Opening a lock

A card lock is ready to unlock when nothing is left to draw, and a timer lock
when its timer runs out. `/lock open [ref]` opens a ready lock and reveals the
device code again. `ref` suggests ready locks by name or ID.

The reply is a private message in servers. In a bot DM or group DM it is a
normal message, because the conversation is already limited to the member and
their keyholder. Running `/history` privately shows the code alongside any
completed lock, so a missed open reply is recoverable. A public `/history` reply
never includes codes.

Templates that use a keyholder passcode are supported: enter the passcode in the
form. The share post marks it as required so the lockee knows to ask for it.

Card templates also get a **Simulation** button, on the share post and on the
private reply after a load. It asks ChastiSafe for the same estimate the website
shows: average duration and cards drawn, the ranges, the draw interval, and how
many runs the estimate used. The reply is private to whoever presses the button,
so a public share post stays clean.

## Notifications

`/lock load` subscribes the member automatically, so a lock loaded from Discord
is watched without another command. `/lock notify` records the subscription for
locks loaded in the ChastiSafe app: their active card lock when they have one,
otherwise a lock with a check-in schedule. A member watches one lock at a time,
and a new subscription replaces the old one. The scheduler checks each
subscription against the lock's draw schedule and sends a DM when one or more
draws become available. The DM lists draws available, cards left, greens needed,
and the next draw time, and has an **Open draw board** button that posts the
board in that same DM. After a notification the bot waits for the member to
draw, then schedules the next check from the lock's `wait` value, so it does not
poll ChastiSafe constantly.

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
`public:true` to post the reply in the channel. A hygiene unlock's code is
redacted there, because a log reply can be public and permanent.

`/history` lists your completed locks, newest first, with duration, unlock
reason, keyholder, rating, and the lock code when the reply is private. `TEST`
locks and locks older than a year are hidden by default; add
`include_test:true` or `include_old:true` to bring them back, and `public:true`
to post the reply in the channel without codes. `/lock status` also accepts
`public:true`.

`/lock draw` posts a public draw board so the channel can watch the session.
While the lock runs, the board has Draw and Stop. When the lock is ready to
unlock, the board has Open, Put green back, Reset, and Let keyholder decide.
Open reveals the code privately in a server and normally in a DM. Put green back
returns the last green card to the deck so drawing continues. Reset asks for
typed confirmation and restarts the lock from the beginning. Let keyholder
decide sends the keyholder ChastiSafe's decision prompts; a self lock does not
show that button. The actions are available to the lockee and their keyholder;
anyone else gets a private refusal. The board keeps a running log of the cards
drawn, a running total, and a countdown of the cards left in the deck. The
buttons are removed when the session stops or the draws run out.

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
