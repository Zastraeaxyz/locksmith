# Locksmith

A Discord bot that operates ChastiSafe locks for their lockees, with
keyholder-approved loading, draws, check-ins, and a shared record of the lock.

## Language

### People

**Member**:
A Discord user known to Locksmith, identified by Discord user ID. A member owns
at most one credential, one subscription, and one document of each kind.
_Avoid_: User, account, owner

**Role**:
A member's relationship to one lock: lockee, keyholder, or both for a self lock.
Every lock has exactly one lockee and one keyholder, and roles are never global.
_Avoid_: Type, position

**Lockee**:
The one member a lock is on. The role is per lock: the same member can be a
lockee on one lock and a keyholder on another.
_Avoid_: Owner, user

**Keyholder**:
The one person who controls a given lock, identified by the Discord account
linked to their ChastiSafe account. The role is per lock, so a keyholder may
also be the lockee of another lock.
_Avoid_: Owner, master

**External keyholder**:
A keyholder who is a different person from the lockee. A self lock has none.
_Avoid_: Other keyholder, remote keyholder

### Locks

**Template**:
A lock definition on chastisafe.com that can be shared by link. Viewing one is
public to anyone with the link.
_Avoid_: Lock, preset

**Lock**:
A loaded instance of a template on one member's ChastiSafe account.
_Avoid_: Template, session, instance

**Self lock**:
A lock whose keyholder is the lockee.
_Avoid_: Solo lock, personal lock

**Card lock**:
A lock with a deck, so it can be drawn from.
_Avoid_: Deck lock, draw lock

**Check-in lock**:
A lock with a check-in schedule. It may also be a card lock.
_Avoid_: Timed lock, recurring lock

**Purpose**:
What a lock is for, chosen when loading: chastity, test, bondage, task, loyalty,
or emergency.
_Avoid_: Type, mode

**Test lock**:
A lock whose purpose is TEST. Hidden from history by default.
_Avoid_: Trial lock, dummy lock

**Deck**:
The card set a card lock draws from.
_Avoid_: Pile, stack

**Draw**:
One pick from a deck, recorded as an entry in the lock log and the draw board.
_Avoid_: Pick, pull, turn

**Ready to unlock**:
A lock state with nothing left to draw, so the device may be opened.
_Avoid_: Finished, complete, done

**Frozen**:
A lock state that blocks drawing and changes. It is set by the keyholder or by a
freeze card.
_Avoid_: Paused, locked out

### Secrets

**Developer code**:
The ChastiSafe account secret that lets Locksmith act for a member. It is
labelled "developer code" in the ChastiSafe app and functions as that account's
password.
_Avoid_: Password, token, API key

**Keyholder passcode**:
A code the keyholder gives the lockee to permit loading one shared lock. The
link alone is enough to view a template; the passcode gates loading only.
_Avoid_: Lock passcode, load code, password

**Lock code**:
The code the lockee sets on the physical device and gives to ChastiSafe at load
time. ChastiSafe withholds it during the lock and reveals it when the lock is
done; Locksmith never displays it.
_Avoid_: Device code, combination, unlock code

### Watching a lock

**Subscription**:
The stored record of a member watching one lock: the lock, its draw schedule
state, and the current check-in reminder stage.
_Avoid_: Notification, watcher, monitor

**Notification**:
A DM sent because of a subscription. Draw notifications and check-in reminders
are the two kinds.
_Avoid_: Alert, ping

**Check-in**:
A confirmation a lockee must record during a lock, and the act of recording it.
_Avoid_: Report, attendance, verification

**Check-in cycle**:
The period between one check-in deadline and the next. Reminder state resets at
each new cycle.
_Avoid_: Check-in period, interval

**Reminder**:
A DM that a check-in is due soon, is due in five minutes, or is overdue.
_Avoid_: Notification, warning

**Reminder stage**:
How far a check-in reminder has progressed in the current cycle: lead, final, or
overdue. Each stage fires at most once per cycle.
_Avoid_: Level, step

### Discord surfaces

**Draw board**:
The public Discord message where a lock's draws are taken and logged. Only the
lockee and an external keyholder may press its buttons.
_Avoid_: Draw message, draw panel

**Draw session**:
The state behind one draw board, keyed by an opaque ID so the board survives
edits. A session expires, after which the board's buttons stop working.
_Avoid_: Draw, game

**Lock log**:
The ordered record of draws in one lock, shown by `/lock log`. The full log adds
the lock's other events.
_Avoid_: History, draw log

**History**:
The record of a member's completed locks, newest first, shown by `/history`.
_Avoid_: Lock log, archive

**Document**:
Member-authored markdown shown by `/profile`, `/rules`, or `/kinks`. A member has
at most one of each kind.
_Avoid_: Page, bio, notes
