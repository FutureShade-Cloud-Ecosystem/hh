// What the customer picker says when the account create threw. Only a request that never
// got an answer is "could not reach the server": a rejected fetch is a TypeError, an abort
// or a timeout is a DOMException by that name. A body that would not parse (the server
// answered success) or a fault in the picker's own code is not a lost signal, so it gets
// the picker's existing words for a failed create.
export function createFailureWords(thrown: unknown): string {
  const name = thrown instanceof Error ? thrown.name : '';
  const lost = thrown instanceof TypeError || name === 'AbortError' || name === 'TimeoutError';
  return lost ? 'Could not reach the server, try again.' : 'Could not create the account.';
}
