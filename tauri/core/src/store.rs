//! The Rust side of the offline store's SqlExecutor seam
//! (`web/packages/offline-store/src/sql.ts`): one fixed SQLite database file
//! named per profile, driven through the shell's `run_statement` and
//! `run_atomic` commands under the capability allow list. The webview never
//! names a path and never holds an open SQL grant: with the probe's
//! `plugin:sql` the connection string could escape the app dir (probe 4
//! review, round 1, P2 2), so the store's engine lives here instead, behind
//! commands that take only SQL and parameters.
//!
//! The seam's promise is kept exactly: every write is a single statement or
//! an `atomic` list, and an `atomic` list applies fully or not at all with no
//! other statement of this store running between them (one connection behind
//! a mutex, one transaction per batch). The connection cannot reach any other
//! file: the attached database limit is zero, which SQLite enforces for every
//! spelling of `ATTACH` and for `VACUUM INTO` (see `lock_down`).

use std::{
    collections::BTreeMap,
    path::Path,
    sync::{Arc, Mutex},
};

/// The seam's SqlValue: a string, a number or null (`sql.ts`).
#[derive(Clone, Debug, PartialEq, serde::Serialize, serde::Deserialize)]
#[serde(untagged)]
pub enum SqlValue {
    Text(String),
    Integer(i64),
    Real(f64),
    Null,
}

impl From<SqlValue> for rusqlite::types::Value {
    fn from(value: SqlValue) -> Self {
        match value {
            SqlValue::Text(s) => rusqlite::types::Value::Text(s),
            SqlValue::Integer(i) => rusqlite::types::Value::Integer(i),
            SqlValue::Real(f) => rusqlite::types::Value::Real(f),
            SqlValue::Null => rusqlite::types::Value::Null,
        }
    }
}

fn into_seam(value: rusqlite::types::Value, column: &str) -> Result<SqlValue, String> {
    match value {
        rusqlite::types::Value::Null => Ok(SqlValue::Null),
        rusqlite::types::Value::Integer(i) => Ok(SqlValue::Integer(i)),
        rusqlite::types::Value::Real(f) => Ok(SqlValue::Real(f)),
        rusqlite::types::Value::Text(s) => Ok(SqlValue::Text(s)),
        rusqlite::types::Value::Blob(_) => Err(format!(
            "column {column} holds a blob: the seam carries text, numbers and null only"
        )),
    }
}

/// One row, keyed by column name (the seam's SqlRow). A repeated column name
/// keeps the later column, the way a JavaScript object would.
pub type SqlRow = BTreeMap<String, SqlValue>;

/// One statement (the seam's SqlStatement): SQL plus its parameters.
#[derive(Clone, Debug, serde::Serialize, serde::Deserialize)]
pub struct SqlStatement {
    pub sql: String,
    #[serde(default)]
    pub params: Vec<SqlValue>,
}

/// A clearer message for the plain forms of the statements that reach outside
/// the fixed database file. It is a courtesy and NOT the security boundary: a
/// leading comment hides the word, and `VACUUM INTO` is never matched. The
/// boundary is `lock_down`, which sets the attached database limit to zero on
/// the connection itself. The store's own statements never use these (its
/// pragma use is the `pragma_table_info` table valued function, which stays
/// allowed).
fn check_statement(sql: &str) -> Result<(), String> {
    let first = sql
        .trim_start()
        .split(|c: char| c.is_whitespace() || c == '(')
        .next()
        .unwrap_or("")
        .to_ascii_lowercase();
    if first == "attach" || first == "detach" {
        Err(format!(
            "statement refused: {first} escapes the profile's fixed database file"
        ))
    } else {
        Ok(())
    }
}

/// Close the connection's ways out of its file. SQLITE_LIMIT_ATTACHED at zero
/// makes SQLite itself refuse `ATTACH` however it is spelled (comments,
/// newlines) and `VACUUM INTO`, which writes a database file at a named path
/// and which no authorizer action covers. A limit also holds for every
/// statement the connection ever prepares, with no parsing of ours in the way.
fn lock_down(conn: &rusqlite::Connection) {
    conn.set_limit(rusqlite::limits::Limit::SQLITE_LIMIT_ATTACHED, 0);
}

/// The store: one SQLite connection behind a mutex, so a batch holds the
/// connection for its whole transaction and nothing interleaves.
#[derive(Clone)]
pub struct Store {
    conn: Arc<Mutex<rusqlite::Connection>>,
}

impl Store {
    /// The profile's fixed file. The caller owns the naming (hh-field names
    /// it per profile in the app config dir); the webview never does.
    pub fn open_path(path: &Path) -> Result<Self, String> {
        let conn = rusqlite::Connection::open(path).map_err(|e| e.to_string())?;
        lock_down(&conn);
        Ok(Self { conn: Arc::new(Mutex::new(conn)) })
    }

    /// An in memory store: the tests use it, nothing else does.
    pub fn open_in_memory() -> Result<Self, String> {
        let conn = rusqlite::Connection::open_in_memory().map_err(|e| e.to_string())?;
        lock_down(&conn);
        Ok(Self { conn: Arc::new(Mutex::new(conn)) })
    }

    /// The seam's `run` and `all` in one command: runs one statement and
    /// returns its rows (empty when the statement returns none). The TS
    /// adapter maps `run` by ignoring the rows and `all` by taking them.
    pub fn run(&self, sql: &str, params: Vec<SqlValue>) -> Result<Vec<SqlRow>, String> {
        check_statement(sql)?;
        let conn = self.conn.lock().map_err(|_| "the store's connection is poisoned".to_string())?;
        let mut stmt = conn.prepare(sql).map_err(|e| e.to_string())?;
        let columns: Vec<String> = stmt.column_names().into_iter().map(String::from).collect();
        let mut rows = stmt
            .query(rusqlite::params_from_iter(params.into_iter().map(rusqlite::types::Value::from)))
            .map_err(|e| e.to_string())?;
        let mut out = Vec::new();
        while let Some(row) = rows.next().map_err(|e| e.to_string())? {
            let mut record = SqlRow::new();
            for (index, column) in columns.iter().enumerate() {
                let value: rusqlite::types::Value = row.get(index).map_err(|e| e.to_string())?;
                record.insert(column.clone(), into_seam(value, column)?);
            }
            out.push(record);
        }
        Ok(out)
    }

    /// The seam's `atomic`: every statement applies or none does, and no
    /// other statement of this store runs between them. The mutex is held
    /// across the whole transaction, so the promise is structural.
    pub fn atomic(&self, statements: Vec<SqlStatement>) -> Result<(), String> {
        for s in &statements {
            check_statement(&s.sql)?;
        }
        let mut conn = self.conn.lock().map_err(|_| "the store's connection is poisoned".to_string())?;
        let tx = conn.transaction().map_err(|e| e.to_string())?;
        let outcome = Self::run_batch(&tx, statements);
        match outcome {
            Ok(()) => tx.commit().map_err(|e| e.to_string()),
            Err(e) => {
                let _ = tx.rollback();
                Err(e)
            }
        }
    }

    fn run_batch(tx: &rusqlite::Transaction, statements: Vec<SqlStatement>) -> Result<(), String> {
        for s in statements {
            // A statement that returns rows is a caller mistake: the seam's
            // atomic carries writes, and execute refuses the rest.
            tx.execute(
                &s.sql,
                rusqlite::params_from_iter(s.params.into_iter().map(rusqlite::types::Value::from)),
            )
            .map_err(|e| e.to_string())?;
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn store() -> Store {
        Store::open_in_memory().expect("an in memory store opens")
    }

    #[test]
    fn run_creates_inserts_and_reads_back() {
        let store = store();
        store
            .run("CREATE TABLE t (id INTEGER PRIMARY KEY, note TEXT NOT NULL)", vec![])
            .expect("create runs");
        let rows = store
            .run("INSERT INTO t (note) VALUES ($1)", vec![SqlValue::Text("row-1".into())])
            .expect("insert runs");
        assert!(rows.is_empty(), "an insert returns no rows");
        let rows = store
            .run("SELECT id, note FROM t ORDER BY id", vec![])
            .expect("select runs");
        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].get("note"), Some(&SqlValue::Text("row-1".into())));
        assert_eq!(rows[0].get("id"), Some(&SqlValue::Integer(1)));
    }

    #[test]
    fn run_binds_the_seams_value_kinds() {
        let store = store();
        store.run("CREATE TABLE v (t TEXT, i INTEGER, f REAL, n TEXT)", vec![]).expect("create");
        store
            .run(
                "INSERT INTO v (t, i, f, n) VALUES ($1, $2, $3, $4)",
                vec![
                    SqlValue::Text("s".into()),
                    SqlValue::Integer(7),
                    SqlValue::Real(1.5),
                    SqlValue::Null,
                ],
            )
            .expect("insert");
        let rows = store.run("SELECT t, i, f, n FROM v", vec![]).expect("select");
        assert_eq!(rows[0].get("t"), Some(&SqlValue::Text("s".into())));
        assert_eq!(rows[0].get("i"), Some(&SqlValue::Integer(7)));
        assert_eq!(rows[0].get("f"), Some(&SqlValue::Real(1.5)));
        assert_eq!(rows[0].get("n"), Some(&SqlValue::Null));
    }

    #[test]
    fn the_store_reads_pragma_table_info_like_the_store_package_does() {
        let store = store();
        store.run("CREATE TABLE outbox (id TEXT PRIMARY KEY)", vec![]).expect("create");
        let rows = store
            .run("SELECT name FROM pragma_table_info('outbox')", vec![])
            .expect("the table valued pragma works");
        assert_eq!(rows[0].get("name"), Some(&SqlValue::Text("id".into())));
    }

    #[test]
    fn atomic_applies_every_statement_or_none() {
        let store = store();
        store
            .run("CREATE TABLE a (id INTEGER PRIMARY KEY, note TEXT NOT NULL)", vec![])
            .expect("create");
        let good = SqlStatement {
            sql: "INSERT INTO a (note) VALUES ($1)".into(),
            params: vec![SqlValue::Text("kept".into())],
        };
        let bad = SqlStatement {
            sql: "INSERT INTO a (no_such_column) VALUES (1)".into(),
            params: vec![],
        };
        let err = store.atomic(vec![good, bad]).expect_err("the batch fails");
        assert!(!err.is_empty());
        let rows = store.run("SELECT note FROM a", vec![]).expect("select");
        assert!(rows.is_empty(), "the failed batch left nothing behind");
    }

    #[test]
    fn atomic_holds_together_and_orders_the_batch() {
        let store = store();
        store.run("CREATE TABLE b (id INTEGER PRIMARY KEY, note TEXT NOT NULL)", vec![]).expect("create");
        let statements: Vec<SqlStatement> = (0..5)
            .map(|i| SqlStatement {
                sql: "INSERT INTO b (note) VALUES ($1)".into(),
                params: vec![SqlValue::Text(format!("row-{i}"))],
            })
            .collect();
        store.atomic(statements).expect("the batch applies");
        let rows = store.run("SELECT note FROM b ORDER BY id", vec![]).expect("select");
        let notes: Vec<&SqlValue> = rows.iter().map(|r| r.get("note").unwrap()).collect();
        assert_eq!(
            notes,
            vec![
                &SqlValue::Text("row-0".into()),
                &SqlValue::Text("row-1".into()),
                &SqlValue::Text("row-2".into()),
                &SqlValue::Text("row-3".into()),
                &SqlValue::Text("row-4".into()),
            ]
        );
    }

    #[test]
    fn attach_and_detach_are_refused() {
        let store = store();
        for sql in [
            "ATTACH DATABASE '/tmp/escape.db' AS esc",
            "attach database '/tmp/escape.db' as esc",
            "  DETACH esc",
        ] {
            let err = store.run(sql, vec![]).expect_err("the statement is refused");
            assert!(err.contains("refused"), "{sql}: {err}");
        }
        let err = store
            .atomic(vec![SqlStatement {
                sql: "ATTACH DATABASE '/tmp/escape.db' AS esc".into(),
                params: vec![],
            }])
            .expect_err("attach is refused in a batch too");
        assert!(err.contains("refused"));
    }

    fn escape_dir(tag: &str) -> std::path::PathBuf {
        let dir = std::env::temp_dir().join(format!("hh-store-escape-{tag}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).expect("a scratch directory");
        dir
    }

    /// Every spelling of an escape the word check cannot see, plus the plain
    /// ones: all go through the exact `Store::run` and `Store::atomic` paths
    /// the commands call, on a file store, and none may create a file.
    #[test]
    fn no_form_of_attach_or_vacuum_into_escapes_the_fixed_file() {
        let dir = escape_dir("p1");
        let store = Store::open_path(&dir.join("store.sqlite3")).expect("the file store opens");
        store.run("CREATE TABLE secret (note TEXT)", vec![]).expect("create");
        store.run("INSERT INTO secret (note) VALUES ('row')", vec![]).expect("insert");
        let target = |name: &str| dir.join(name).display().to_string();
        let forms = [
            format!("ATTACH DATABASE '{}' AS esc", target("plain.db")),
            format!("/* c */ ATTACH DATABASE '{}' AS esc", target("comment.db")),
            format!("-- c\nATTACH DATABASE '{}' AS esc", target("newline.db")),
            format!("/* c */ -- d\n ATTACH '{}' AS esc", target("mixed.db")),
            format!("VACUUM INTO '{}'", target("vacuum.db")),
            format!("/* c */ VACUUM INTO '{}'", target("vacuum-comment.db")),
        ];
        for sql in &forms {
            assert!(store.run(sql, vec![]).is_err(), "run must refuse: {sql}");
            let batch = vec![SqlStatement { sql: sql.clone(), params: vec![] }];
            assert!(store.atomic(batch).is_err(), "atomic must refuse: {sql}");
        }
        for name in ["plain.db", "comment.db", "newline.db", "mixed.db", "vacuum.db", "vacuum-comment.db"] {
            assert!(!dir.join(name).exists(), "{name} was created: the store escaped its file");
        }
        let databases = store.run("SELECT name FROM pragma_database_list", vec![]).expect("list");
        assert_eq!(databases.len(), 1, "only main is attached: {databases:?}");
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn an_in_memory_store_refuses_the_same_escapes() {
        let store = store();
        let dir = escape_dir("p1mem");
        let file = dir.join("mem.db").display().to_string();
        for sql in [
            format!("/* c */ ATTACH DATABASE '{file}' AS esc"),
            format!("VACUUM INTO '{file}'"),
        ] {
            assert!(store.run(&sql, vec![]).is_err(), "{sql}");
        }
        assert!(!dir.join("mem.db").exists());
        let _ = std::fs::remove_dir_all(&dir);
    }

    const TRANSACTION_CONTROL: [&str; 12] = [
        "BEGIN",
        "BEGIN IMMEDIATE",
        "COMMIT",
        "END",
        "ROLLBACK",
        "SAVEPOINT s",
        "RELEASE s",
        "/* c */ BEGIN",
        "-- c\nCOMMIT",
        "/* c */ ROLLBACK",
        "/* c */ SAVEPOINT s",
        "-- c\n RELEASE SAVEPOINT s",
    ];

    #[test]
    fn a_commit_inside_an_atomic_cannot_split_the_batch() {
        let store = store();
        store.run("CREATE TABLE a (note TEXT NOT NULL)", vec![]).expect("create");
        let insert = |note: &str| SqlStatement {
            sql: "INSERT INTO a (note) VALUES ($1)".into(),
            params: vec![SqlValue::Text(note.into())],
        };
        let bad = SqlStatement { sql: "INSERT INTO a (no_such_column) VALUES (1)".into(), params: vec![] };
        for control in TRANSACTION_CONTROL {
            let control = SqlStatement { sql: control.into(), params: vec![] };
            let result = store.atomic(vec![insert("one"), control.clone(), insert("two"), bad.clone()]);
            assert!(result.is_err(), "{}: the batch must fail", control.sql);
            let rows = store.run("SELECT note FROM a", vec![]).expect("select");
            assert!(rows.is_empty(), "{}: the first half committed: {rows:?}", control.sql);
        }
    }

    #[test]
    fn transaction_control_through_run_is_refused_and_cannot_wedge_the_store() {
        let store = store();
        store.run("CREATE TABLE w (note TEXT)", vec![]).expect("create");
        for sql in TRANSACTION_CONTROL {
            assert!(store.run(sql, vec![]).is_err(), "run must refuse: {sql}");
            // The store still takes an atomic batch right after.
            store
                .atomic(vec![SqlStatement { sql: "INSERT INTO w (note) VALUES ('ok')".into(), params: vec![] }])
                .unwrap_or_else(|e| panic!("after {sql} the store is wedged: {e}"));
        }
        let rows = store.run("SELECT note FROM w", vec![]).expect("select");
        assert_eq!(rows.len(), TRANSACTION_CONTROL.len());
    }

    #[test]
    fn a_file_store_keeps_its_data_across_connections() {
        let path = std::env::temp_dir().join(format!("hh-field-store-test-{}.sqlite3", std::process::id()));
        let _ = std::fs::remove_file(&path);
        {
            let store = Store::open_path(&path).expect("the file store opens");
            store
                .run("CREATE TABLE t (note TEXT)", vec![])
                .and_then(|_| store.run("INSERT INTO t (note) VALUES ('kept')", vec![]))
                .expect("write");
        }
        let store = Store::open_path(&path).expect("reopen");
        let rows = store.run("SELECT note FROM t", vec![]).expect("read");
        assert_eq!(rows[0].get("note"), Some(&SqlValue::Text("kept".into())));
        let _ = std::fs::remove_file(&path);
    }
}
