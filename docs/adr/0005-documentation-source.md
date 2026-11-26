# Documentation lives in Discord and follows the ChastiSafe app

`/docs` renders static embeds from `internal/feature/docs.go`. It makes no
ChastiSafe call, so it works before a member logs in and costs no API quota.
The pages describe Locksmith's own commands, buttons, and reminders first, and
then explain the ChastiSafe mechanics a member meets while using them.

Card copy is checked against the help text in the ChastiSafe web app
(`main.dart.js`, Cards and Card locks sections), not against the short
`Card.Label` and `Card.Description` strings. Those strings are enough for a log
line, but they leave out the rules that matter: a red card waits out the
regularity time, a reset rebuilds the original deck without resetting locked
time, a double up doubles only reds and yellows, and a dice card can freeze the
lock until a keyholder unfreezes it.

Locksmith documents only the surfaces it exposes. Task rollers, bots, deck
editing, corner time, pillory, and PINK script authoring stay in the ChastiSafe
app; a card page may mention them when the card's effect depends on them, but
it does not teach them.

## Consequences

- Editing `docs.go` is the only step needed to change a page. A new page in
  `docsPages` appears in the index, the `/docs page:` choices, and the buttons
  automatically.
- The card page is grouped into embed fields to stay inside Discord's field and
  total embed limits. Extend a group before adding a new field, and run the
  docs tests after a copy change.
- The ChastiSafe app help is the correctness reference. When ChastiSafe changes
  a card's behavior, update the page and the CHANGELOG together.
- Because the pages are static, they cannot show a member's own lock state. Use
  `/lock status`, `/lock log`, or `/history` for that, and link to those
  commands from the page copy instead of duplicating their data.
