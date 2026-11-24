# AGENTS.md

## Rules

- Use conventional commits (`feat:`, `fix:`, `chore:`, ...) and commit in small, frequent increments.
- never leave uncommitted changes
- After committing deploy the bot with `mise deploy`
- Always commit as `Author: zastraeaxyz <zastraea@proton.me>`

## Time and dates

- Always render a specific moment in time with Discord timestamp syntax, for
  example `<t:1795500000:R>` or `<t:1795500000:f>`.
- Use relative (`:R`) for upcoming or elapsed moments, and `:f`, `:F`, or `:t`
  when the exact clock time is the point.
- Never hard-code a countdown like "in 5 minutes" or "8h 11m" for a deadline.
- Durations that are periods, not moments (for example "every 1 day" or a
  completed lock's total duration), may stay as normal text.
- Dates without a time may stay as normal text.
