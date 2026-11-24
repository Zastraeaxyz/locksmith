# Keyholder identity is the ChastiSafe Discord link

A keyholder is a ChastiSafe role, but ChastiSafe identifies an account by the
Discord snowflake linked in ChastiSafe, so `LoadedLock.KeyholderID` is already a
Discord user ID. Locksmith therefore compares it directly with the Discord user
who pressed a button. A keyholder does not need a Locksmith credential, because
the bot calls ChastiSafe with the lockee's saved developer code.

## Considered options

- **Require the keyholder to run `/login`.** Rejected: keyholders rarely operate
  the bot, the lockee's credential already authorizes the draw, and it would
  store a second secret for no gain.
- **Trust Discord permissions.** Rejected: a draw board may be public, so anyone
  in the channel could take draws.
- **Match ChastiSafe usernames to Discord handles.** Rejected: the two namespaces
  are unrelated, and a display name is not an identity.

## Consequences

A keyholder who has not linked Discord in ChastiSafe cannot press a board's
buttons, even though they can see the board. That is intentional: viewing a lock
is public with the link, acting on it is not.

`/login` rejects a developer code whose ChastiSafe account is linked to a
different Discord user (`Identity.OwnedBy`), so a stored credential always
belongs to the member who owns it.
