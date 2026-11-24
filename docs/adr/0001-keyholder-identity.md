# Keyholders act through their own credential

A keyholder is a ChastiSafe role, identified by a ChastiSafe account, while every
Discord surface in Locksmith is keyed by Discord user ID. We decided that a
keyholder gains keyholder powers in Locksmith (pressing Draw and Stop, seeing the
keyholder side of a lock) only after running `/login`, which links their
ChastiSafe account to their Discord ID; Locksmith resolves a lock's keyholder
through stored credentials rather than guessing.

## Considered options

- **Trust Discord permissions.** Anyone in the channel could press a board's
  buttons. Rejected: a draw board may be public, and draws are the point of the
  lock.
- **Match ChastiSafe usernames to Discord handles.** Rejected: the two namespaces
  are unrelated, and a display name is not an identity.
- **Treat every viewer as a potential keyholder.** Rejected: it removes the
  lockee/keyholder boundary that the whole feature rests on.

## Consequences

A keyholder who has never run `/login` can read a public draw board but cannot
press its buttons. This is intentional: seeing the lock is public, acting on it
is not.

The current implementation does not do this resolution yet. `draw.go` and
`status.go` compare `LoadedLock.KeyholderID`, a ChastiSafe ID, directly against a
Discord user ID, so an external keyholder's buttons never unlock. Until the
lookup exists, the glossary's definition of Keyholder is the intended model, not
the current behaviour.
