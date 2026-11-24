# Locksmith never displays a lock code

ChastiSafe reveals a lock's code when the lock is done, and includes it in the
log for a hygiene unlock, so Locksmith receives the value in `/history` and
`/lock log`. We still never print it: a Discord reply can be public, permanent,
and forwarded, which is the opposite of what a withheld lock code is for. The
ChastiSafe app is the only place a member sees it.

## Consequences

`/lock log` redacts the code on a hygiene unlock and says so. `/history` ignores
`RecentLock.LockCode` even though the API returns it. Do not "fix" either by
adding the code to a reply.
