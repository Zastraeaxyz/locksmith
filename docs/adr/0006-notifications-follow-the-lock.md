# Notifications follow the lock, not the command

A member's reminders are one row in `notifications`, and the presence of that
row is the on state. A member watches one lock at a time. Everything else about
the subscription is derived from the lock: draws, check-ins, and freezes.

An earlier version unsubscribed a member when a card freeze outlasted its
expected window, from 2x to 4x the draw interval, and sent a DM asking them to
run `/lock notify` to turn reminders back on. That made a routine event look
like the member's mistake, deleted the watch just as the freeze was about to
end, and was impossible to follow: `/lock notify` refused a frozen lock,
because lock selection preferred a drawable one. Turning reminders back on also
depended on the member remembering a command they never chose to run.

Locksmith now treats a freeze as internal scheduling state:

- A freeze never ends a watch. The bot keeps polling a frozen card lock at the
  half-interval checkpoints from 2x to 4x, then every fifteen minutes, until
  the lock is unfrozen.
- The first draw after a freeze is announced even when the draw count did not
  grow, because a stun card can wipe the draws the last poll saw.
- At 4x the draw interval, the longest a freeze card holds, the bot checks the
  lock again a minute later. ChastiSafe can report `frozenByCard` briefly past
  the real end, so a flag still set at that point is treated as a timing lag,
  not a freeze that overran. The bot sends one notification when the freeze is
  actually over, and it never asks the member to run anything.
- `/lock notify` stays as the entry point for a lock loaded in the ChastiSafe
  app, and now accepts a frozen card lock.
- At startup the bot restores a watch for every member with a saved code and no
  subscription, unless they turned reminders off on purpose. That repair needs
  a recorded opt-out, because a missing row alone cannot tell a bug from a
  choice.

## Considered options

- **Keep unsubscribing and improve the message.** Rejected: the member did
  nothing wrong, the reminder matters most when the freeze ends, and the only
  fix in the message was a command that could not work on a frozen lock.
- **Poll only at 4x.** Rejected: a freeze that ends at 2x would leave draws
  unannounced for two more intervals, and catching the early end is the point
  of the checkpoints.
- **Ask the member to reply to the notice to resubscribe.** Rejected: it is
  still work created by a normal freeze, and it needs message handling for a
  decision the bot can make itself.
- **Backfill from a missing row alone.** Rejected: `/lock notify enabled:false`
  also deletes the row, so a backfill would override a deliberate choice.

## Consequences

- A `notifications` row stays until the member turns reminders off, logs out,
  or the lock is ready or gone. Ending a watch on a freeze is a bug.
- Freeze state is stored in `freeze_start_at` and `freeze_ping_at`, so the
  late check survives a restart and the notice is sent once per freeze.
- A member who turned reminders off is not restored at startup. Watching a lock
  again through `/lock load` or `/lock notify` clears the opt-out.
- An opt-out made before this change was not recorded, so that member can be
  restored once. Turning reminders off again records it.
- A freeze that started before the bot first saw it does not arm a stale 4x
  message. The member gets the draw notification when the freeze ends instead.
- Analytics record `notify freeze ping` and `notify backfill` as funnel steps,
  with no lock IDs or member content.
