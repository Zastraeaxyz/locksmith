# Lock codes may be shown in private replies

Supersedes [ADR 0002](0002-locksmith-never-shows-lock-codes.md).

ChastiSafe returns a lock's code from `/recent` and from `/open`, and the code
is what the lockee needs to set on the physical device. A private reply can
still be missed: ephemeral messages disappear, clients reload, and members
switch devices. Refusing to repeat the code made a missed reply unrecoverable
inside Locksmith.

Locksmith now shows the code in two places:

- `/lock open`, and the draw board's **Open** action, once a lock is ready to
  unlock.
- `/history`, when the reply is private.

A guild reply for `/lock open` is always ephemeral, so a server never receives
the code. In a bot DM or group DM the reply is a normal message, because the
conversation is already limited to the member and their keyholder. `/history`
shows codes only in a private reply; `public:true` omits them and says so.

## Consequences

The code is never stored, logged, or sent to analytics. Locksmith reads it from
ChastiSafe when it renders the reply. `/lock log` keeps redacting the code from
a hygiene unlock, because a log reply can be public and permanent.

A public `/history` reply and every `/lock status` reply remain code-free. Do not
add the code to an embed or a message that can be seen by the channel.
