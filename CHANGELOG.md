# Changelog

Notable changes to Locksmith, newest first.

## 2026-11-26

### Added

- `/docs` opens a documentation index with pages for getting started, locks and templates, drawing, every card and its effect, check-ins, history and logs, and member documents. Each page has a **Back to docs** button and links to the install, ChastiSafe, and the repository.
- `/lock load` shows the lock privately before loading it, with **Load lock**, **Simulation** (card locks), and **Open in ChastiSafe** buttons. Nothing is loaded until **Load lock** is pressed.

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
