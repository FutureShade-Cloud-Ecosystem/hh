# The npm scope rename: @hh to @hardscapehouse

This note is the repository's single record of the npm scope rename, kept so
the scope gate (scripts/check-scope.sh) can name exactly one file allowed to
carry the old scope. It is history, not a live instruction: nothing in the
repository may name the old scope except this note.

## What happened

The npm scope `hh` belongs to someone else. Colton tried to reserve it and npm
refused, because a stranger already holds it. He then reserved the npm
organisation `hardscapehouse` under his own npm account. The choice was put to
him as option A (`@hardscapehouse`) or option B (`@futurebuild` with the
product moved into the package name); his words in chat were "option A".

Source: cycle HH-4 relay message 049-colton-rules-scope.md, carrying Colton's
ruling given in chat.

## The rename

    @hh/api-client     to  @hardscapehouse/api-client
    @hh/auth           to  @hardscapehouse/auth
    @hh/design-system  to  @hardscapehouse/design-system
    @hh/offline-store  to  @hardscapehouse/offline-store
    @hh/shell-chrome   to  @hardscapehouse/shell-chrome

Nothing was published to the public registry then and nothing is published by
this work; the five packages are marked `"private": true`, so no accidental
publish can carry either name. The reason for the rename is ownership of the
name: our packages must not carry a scope a stranger owns on the public
registry. The work lands as HH-30, one small pull request from main, before
the adoption of Lit, so the Lit work lands on the final names.

## Deliberately not renamed

- Browser storage keys `hh-auth.session.v2` and `hh-auth.lock.v1` (and the
  custody probe key `hh-auth.storage-probe`). They are not package names;
  renaming them would orphan a stored session and a held device lock.
- Every custom element tag name beginning `hh-` (hh-dashboard,
  hh-guided-flow, hh-inbox, hh-scan-and-confirm, hh-list-detail) and every CSS
  custom property in the `--hh-` vocabulary. These are the product's short
  name in its own markup and styles, not a registry scope; the product is
  still called HH, only the npm scope moved.
