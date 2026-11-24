# Locksmith generates lock codes and shows them once

The lockee must set the lock code on the physical device, but a code they choose
is a code they remember. Locksmith therefore generates the code from the
account's ChastiSafe code settings, the length and the character set, and picks
each character at most once so the result has no pattern to hold on to. The
private load reply shows the generated code once, so the lockee can set the
device and then forget it. The member cannot edit or replace it.

The code is never logged, stored, or sent to analytics. ADR 0002 kept it out of
every later reply; [ADR 0004](0004-lock-codes-in-private-replies.md) supersedes
that for two private surfaces: `/lock open` and a private `/history` reply.
`/lock status`, `/lock log`, and public replies remain code-free.

## Considered options

- **Keep the code as an editable field with a suggestion.** Rejected: a
  suggestion the member can replace is still a choice, and a familiar code is a
  remembered code.
- **Never show the code.** Rejected: the member would have no way to set the
  device. The private load reply is the narrowest surface that works.
- **Generate the code before the modal and show it in the form.** Rejected:
  Discord modals have no read-only inputs, so the field would stay editable.

## Consequences

The load reply is the single exception to ADR 0002. Keep it out of logs and every
other reply, and do not add the code to a copy-pasteable public surface.
