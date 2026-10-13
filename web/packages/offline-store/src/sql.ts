// The one seam between the store and a SQL engine. The Tauri shells plug the
// SQL plugin in behind it, the OPFS backend plugs a wa-sqlite worker in, and
// the tests plug node:sqlite in. Every write the store makes is a single
// statement or an `atomic` list, never a transaction held open across calls:
// a pooled plugin connection cannot promise that a BEGIN on one call is still
// the same connection on the next.

export type SqlValue = string | number | null;

export interface SqlStatement {
  sql: string;
  params?: SqlValue[];
}

export type SqlRow = Record<string, SqlValue>;

export interface SqlExecutor {
  /** Run one statement that returns no rows. */
  run(sql: string, params?: SqlValue[]): Promise<void>;
  /** Run one statement and return its rows keyed by column name. */
  all<T = SqlRow>(sql: string, params?: SqlValue[]): Promise<T[]>;
  /**
   * Run the statements in order as one unit: all of them apply or none do,
   * and no other statement of this executor runs between them.
   */
  atomic(statements: SqlStatement[]): Promise<void>;
}

/** An executor that owns a resource to release (a file handle, a worker). */
export interface ClosableSqlExecutor extends SqlExecutor {
  close(): Promise<void> | void;
}

/**
 * Serialise async work: each call starts after the previous one settled,
 * whether it resolved or rejected. Engines with awaiting steps (wa-sqlite)
 * use this so an `atomic` list can never interleave with another call.
 */
export function serialQueue(): <T>(work: () => Promise<T>) => Promise<T> {
  let tail: Promise<unknown> = Promise.resolve();
  return <T>(work: () => Promise<T>): Promise<T> => {
    const result = tail.then(work, work);
    tail = result.catch(() => undefined);
    return result;
  };
}
