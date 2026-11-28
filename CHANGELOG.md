# Changelog

Notable changes to Locksmith, newest first.

## 2026-11-27

### Added

- `/lock progress` shows a day-by-day UTC report for a card lock: net green progress, net red progress, red/yellow pool changes, and total deck changes. A visible deck is reconstructed exactly from the live composition. A hidden deck uses stored deck counts, and a double up is exact when the counts before and after it were recorded. `days` limits the report, `ref` picks another card lock, and `verbose` adds per-card draws, yellow details, and the double up math.

### Changed

- Locksmith stores small deck snapshots for active locks, so `/lock progress` can report exact deck and double up counts after a lock has been running. Hidden decks store only the count, never their card faces.
- `/lock status` and `/lock draw` show the maximum a freeze can last, 4x the draw interval, alongside how long the lock has been frozen.
- A freeze no longer turns draw notifications off. Locksmith keeps watching a frozen lock and announces the first draw after the freeze. At 4x the draw interval, the longest a freeze card holds, it checks again shortly and pings only once the freeze is actually over. The message that asked members to run `/lock notify` is gone.
- `/lock notify` accepts a frozen card lock, and Locksmith restores a missing watch at startup for members who did not turn notifications off on purpose.

### Fixed

- A card lock with a draw available is no longer treated as frozen. ChastiSafe can leave `frozenByCard` set after a freeze ends, so Locksmith follows the app: an available pick wins, and a keyholder freeze still blocks.
- `/lock status`, `/lock draw`, and `/lock open` read the frozen state from `GET /api-v1/loaded` on every use. A card freeze that ChastiSafe still reports past 4x the draw interval gets one more read, and the reply never claims a duration the freeze cannot have.
- The 4x freeze message no longer says a freeze outlasted its maximum. A flag still set at that point is a timing lag, so Locksmith waits a minute and checks again before pinging.
- A freeze's elapsed time in `/lock status` and `/lock draw` now comes from the card that froze the lock. Sticky cards are not freezes, while stun and dice cards are, so a stun freeze no longer reports the whole lock duration.
- Check-in reminders no longer offer a **Check-in** button before the check-in is due. ChastiSafe's tolerance is a late window, not an early one, so the button now appears at the due moment and stays through the overdue period.

## 2026-11-26

### Added

- `/about` posts a short introduction for someone who has not installed Locksmith yet, with install, docs, ChastiSafe, and repository buttons. **Browse docs** opens the documentation index privately.
- `/lock draw auto:true` draws auto: the board takes every draw the lock allows in a row and updates after each card, including extra draws from greens, stickies, doubles, and go-again cards. It stops when the lock is ready, freezes, or runs out.
- `/docs` opens a private documentation index with pages for getting started, locks and templates, drawing, every card and its effect, check-ins, history and logs, and member documents. Each page has a **Back to docs** button and links to the install, ChastiSafe, and the repository.
- `/lock load` shows the lock privately before loading it, with **Load lock**, **Simulation** (card locks), and **Open in ChastiSafe** buttons. Nothing is loaded until **Load lock** is pressed.
- `/lock deck` shows the known deck composition as a grid of icons: revealed cards show their faces, and cards still in a hidden deck show their backs.

### Changed

- The **Simulation** button stays visible when the keyholder has turned simulation off. Pressing it explains that the keyholder does not want the lock simulated.
- Notification DMs are temporary: a newer draw or check-in notification replaces the old one, anything older than a day is deleted, and ending the subscription clears the rest.
- A frozen lock's status shows the minimum freeze, twice its draw interval.

## 2026-11-25

### Changed

- `/lock log` condenses draws that happen within a minute of each other into one line of card icons. Keyholder draws keep a key marker and dice keep the number they threw.
- The recent `/lock log` view always shows at least the last ten draws, with older picks under **Earlier draws**, so a quiet lock still shows how its last draws went.

## 2026-11-24

### Added

- `/changelog` shows recent changes inside Discord.
- `/lock open [ref]` opens a ready lock and reveals the device code. The reply is private in servers and a normal message in DMs.
- Private `/history` replies include each completed lock's code. Public replies omit codes.
- Draw notification DMs have an **Open draw board** button that posts the board in the same DM.
- When a lock is ready to unlock, the draw board offers **Open**, **Put green back**, **Reset**, and **Let keyholder decide**.
- Card templates have a **Simulation** button that previews the expected duration and draws.
- Lock codes are generated from the account's ChastiSafe code settings at load time, and loaded locks are watched automatically.

### Changed

- `/lock draw` opens the ready board when a lock is already ready, so the ready actions are reachable there too.
- Draw notification text points at the **Open draw board** button instead of only the command.
- A frozen card lock is checked at half-draw-interval steps from 2x to 4x the draw time. If it is still frozen after that window, notifications stop with a DM explaining how to turn them back on.

### Fixed

- Check-in reminders only show the **Check-in** button once the lock's check-in window is open. Early button presses and `/lock checkin` now explain when the window opens instead of calling ChastiSafe too soon.
