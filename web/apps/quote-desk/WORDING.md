# The Quote Desk's wordings

One word per field, one voice per sentence. This is the app's wording list for the payment desk
(the round 2 review's P3 8: two money words in one sentence). Where the server sends words, they
are shown as sent and never rewritten; where this app writes the sentence, the words below hold.

## The money word

- The desk's own figures and sentences come from `moneyWord` (`_quote-edit.ts`): integer cents in,
  `CA$` out, a minus when negative. Every figure the desk names itself is a `CA$` figure: the
  preview ("Taking CA$12.50 by Cash"), the figures line ("balance CA$27.06"), the desk's own
  refusal ("The payment is more than the balance of CA$27.06"), the receipt rows, and the entry
  page's notes at sign out.
- The server's own sentences arrive with the server's `$` figures (for example "the payment is
  more than the balance of $27.06"). They are shown as the server sent them, beside the desk's
  `CA$` figures, never rewritten to match: rewriting a server sentence would put words in the
  server's mouth. One voice per sentence: a sentence is wholly the desk's (CA$) or wholly the
  server's (as sent).

## The fields

- The figure still owed on the invoice is "the balance", everywhere: the figures line, the desk's
  over balance refusal, the receipt's row ("Balance on the invoice"). Never "amount owed",
  "outstanding", or "total".
- The pre invoice bound (what the one action raises the invoice for, before any order or invoice
  exists) is "the agreed total", and only that: it is a different field from the balance, and the
  figures line names it once ("Quote Q is Accepted at CA$63.28, the agreed total").
- The tender is the contract's own name (CASH, CARD, CHEQUE, EFT), spoken as Cash, Card, Cheque,
  EFT in the desk's sentences.

## The amount box

- A refused input is said in one of three wordings, all the desk's own:
  - "Use a point for cents, for example 12.50." (a decimal comma or a loose space; the words that
    teach the point),
  - "Type the digits and use one separator every three digits, like 1,000.50." (a mixed grouping,
    a comma and a space in one figure),
  - "The amount is not a number this desk can send: type dollars and cents, like 40.00." (letters,
    signs, unicode digits, a second point, any junk; too many decimal places),
  - "The amount is larger than this desk can send." (above the greatest exact integer cents).
- The parsed figure is shown beside the Take payment button before it is pressed.

## The paid state

- "Paid in full: nothing is owed on this invoice, so no payment is taken here." (the desk's line
  when every attempt on the order is known and the balance reads zero).
- "The invoice is Paid: nothing is owed on it. The outcome of the payment above is not known, so
  the desk still holds its attempt: the retry below asks the server for that payment's receipt,
  and nothing is posted a second time." (the same owed nothing, with an attempt still open).
- The server's sentence for another payment on a paid order is the core's "The order is paid in
  full." and is shown as sent; the stub carries the same sentence for the same state, so the two
  voices agree.
