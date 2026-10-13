//! The four sign in operations behind the shell's `auth_*` commands, over a
//! custody and the Console client. Everything the contract promises about
//! them is decided here, where it can be tested against a mock Console and
//! core; the Tauri commands in the profile crates are thin wrappers.
//!
//! - **sign in** checks the keychain first (no Console session is created
//!   that cannot be kept), creates the session, stores the secret, reads the
//!   account and returns the user only; any failure after the session exists
//!   rolls it back (entry deleted, session deleted at Console, best effort).
//! - **resume** reads the entry and asks Console for the account. A missing
//!   entry or a Console refusal is `no_session`; an outage is `offline` or
//!   `unavailable` and never `no_session`, so an outage cannot lock anyone.
//!   It never deletes the entry.
//! - **exchange** mints a FRESH Console JWT for every attempt, posts it to the
//!   core, and retries once, with another fresh JWT, on a core 401 only.
//! - **sign out** clears the keychain entry FIRST, then tells Console to end
//!   the session within a short window, so an unreachable or hanging Console
//!   cannot keep the entry alive. Sign in and sign out never overlap (one
//!   lock), so a sign out that returned leaves no entry behind.
//!
//! Nothing here logs or prints, and every error is an `AuthCode`.

use std::{sync::Arc, time::Duration};

use crate::console::{
    org_ok, AccountUser, AuthCode, AuthConfig, ConsoleClient, ExchangeAnswer, SessionSecret,
};
use crate::custody::{Address, Custody, CustodyError};

/// How long sign out waits for Console after the keychain is already clear.
const SIGN_OUT_BEST_EFFORT: Duration = Duration::from_secs(5);

pub struct AuthService {
    console: ConsoleClient,
    custody: Arc<dyn Custody>,
    address: Address,
    best_effort: Duration,
    /// Serialises sign in and sign out, so a sign out that returned can never
    /// be followed by a sign in storing a secret it did not see.
    writes: tokio::sync::Mutex<()>,
}

/// A custody call on a blocking thread (a keyring unlock prompt must never
/// sit on an async worker).
async fn blocking<T: Send + 'static>(
    custody: &Arc<dyn Custody>,
    f: impl FnOnce(&dyn Custody) -> Result<T, CustodyError> + Send + 'static,
) -> Result<T, AuthCode> {
    let custody = custody.clone();
    tokio::task::spawn_blocking(move || f(custody.as_ref()))
        .await
        .map_err(|_| AuthCode::KeychainUnavailable)?
        .map_err(AuthCode::from)
}

fn secret_from(raw: Option<String>) -> Result<SessionSecret, AuthCode> {
    // An entry that is not a usable secret is no session, not a crash.
    raw.and_then(SessionSecret::new).ok_or(AuthCode::NoSession)
}

impl AuthService {
    /// `profile_identifier` is the profile's own identifier (the keychain
    /// service); the project id comes from the configuration. Neither comes
    /// from the webview.
    pub fn new(profile_identifier: &str, config: &AuthConfig, custody: Arc<dyn Custody>) -> Result<Self, AuthCode> {
        let console = ConsoleClient::new(config)?;
        let address = Address::console_session(profile_identifier, console.project_id())
            .map_err(|_| AuthCode::Unavailable)?;
        Ok(Self { console, custody, address, best_effort: SIGN_OUT_BEST_EFFORT, writes: tokio::sync::Mutex::new(()) })
    }

    /// Tests use a short window.
    pub fn with_best_effort(mut self, window: Duration) -> Self {
        self.best_effort = window;
        self
    }

    async fn get(&self) -> Result<Option<String>, AuthCode> {
        let address = self.address.clone();
        blocking(&self.custody, move |c| c.get(&address)).await
    }

    async fn set(&self, value: String) -> Result<(), AuthCode> {
        let address = self.address.clone();
        blocking(&self.custody, move |c| c.set(&address, &value)).await
    }

    async fn delete(&self) -> Result<(), AuthCode> {
        let address = self.address.clone();
        blocking(&self.custody, move |c| c.delete(&address)).await
    }

    pub async fn sign_in(&self, email: &str, password: &str) -> Result<AccountUser, AuthCode> {
        let _turn = self.writes.lock().await;
        // The keychain first: fail closed before Console holds a session we
        // could not keep. The old entry, if any, is ended after the new one is in.
        let old = self.get().await?;
        let secret = self.console.create_session(email, password).await?;
        if let Err(e) = self.set(secret.expose().to_string()).await {
            let _ = self.console.delete_session(&secret).await;
            return Err(e);
        }
        let user = match self.console.account(&secret).await {
            Ok(user) => user,
            Err(e) => {
                let _ = self.delete().await;
                let _ = self.console.delete_session(&secret).await;
                return Err(e);
            }
        };
        if let Some(old) = old.and_then(SessionSecret::new) {
            if old.expose() != secret.expose() {
                let _ = self.console.delete_session(&old).await;
            }
        }
        Ok(user)
    }

    pub async fn resume(&self) -> Result<AccountUser, AuthCode> {
        let secret = secret_from(self.get().await?)?;
        self.console.account(&secret).await
    }

    pub async fn exchange(&self, org: &str) -> Result<ExchangeAnswer, AuthCode> {
        if !org_ok(org) {
            return Err(AuthCode::BadRequest);
        }
        let secret = secret_from(self.get().await?)?;
        let attempt = || async {
            let jwt = self.console.mint_jwt(&secret).await?;
            self.console.core_exchange(jwt, org).await
        };
        match attempt().await {
            Err(AuthCode::Unauthorized) => attempt().await,
            other => other,
        }
    }

    pub async fn sign_out(&self) -> Result<(), AuthCode> {
        let _turn = self.writes.lock().await;
        let secret = self.get().await.ok().flatten().and_then(SessionSecret::new);
        let cleared = self.delete().await;
        if let Some(secret) = secret {
            let _ = tokio::time::timeout(self.best_effort, self.console.delete_session(&secret)).await;
        }
        cleared
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::custody::{Address, Custody, MemoryCustody};
    use crate::mock_http::{closed_port, MockServer, Reply, Seen};
    use std::{sync::Arc, time::{Duration, Instant}};

    const PROJECT: &str = "proj-1";
    const PROFILE: &str = "ai.futurebuild.hh.field";
    const SECRET: &str = "c2VjcmV0LXNlbnRpbmVs";
    const OLD_SECRET: &str = "b2xkLXNlY3JldC1zZW50aW5lbA";
    const PASSWORD: &str = "pw-sentinel-9f3";
    const ACCOUNT_OK: &str = r#"{"$id":"u1","email":"a@b.example","name":"A B","status":true}"#;
    const ANSWER: &str = r#"{"token":"core-token-sentinel","expiresAtMs":99,"user":{"user_id":"u1","email":"a@b.example","display_name":"A B","role_code":"driver","role_name":"Driver","tier":2,"scope":"branch"}}"#;

    fn addr() -> Address {
        Address::console_session(PROFILE, PROJECT).unwrap()
    }

    fn run<F: std::future::Future>(f: F) -> F::Output {
        tokio::runtime::Builder::new_current_thread().enable_all().build().unwrap().block_on(f)
    }

    fn service(server_url: &str, custody: Arc<MemoryCustody>) -> AuthService {
        let cfg = AuthConfig::for_test(&format!("{server_url}/v1"), PROJECT, server_url);
        AuthService::new(PROFILE, &cfg, custody).expect("a valid service")
    }

    fn created() -> Reply {
        Reply::json(201, "{}").header("Set-Cookie", &format!("a_session_{PROJECT}={SECRET}; Path=/; HttpOnly"))
    }

    fn jwt_reply(v: &str) -> Reply {
        Reply::json(201, &format!(r#"{{"jwt":"{v}"}}"#))
    }

    fn cookie(secret: &str) -> String {
        format!("a_session_{PROJECT}={secret}")
    }

    fn paths(seen: &[Seen]) -> Vec<String> {
        seen.iter().map(|s| format!("{} {}", s.method, s.path)).collect()
    }

    // ---- sign in -------------------------------------------------------

    #[test]
    fn sign_in_stores_the_secret_and_returns_the_user_only() {
        let server = MockServer::start(vec![created(), Reply::json(200, ACCOUNT_OK)]);
        let custody = Arc::new(MemoryCustody::new());
        let user = run(service(&server.url(), custody.clone()).sign_in("a@b.example", PASSWORD)).unwrap();
        let seen = server.finish();
        assert_eq!(user.user_id, "u1");
        assert_eq!(user.email, "a@b.example");
        assert_eq!(custody.get(&addr()), Ok(Some(SECRET.to_string())));
        assert_eq!(paths(&seen), ["POST /v1/account/sessions/email", "GET /v1/account"]);
        assert_eq!(seen[1].header("cookie"), Some(cookie(SECRET).as_str()));
        let json = serde_json::to_string(&user).unwrap();
        assert!(!json.contains(SECRET), "the secret never reaches the answer");
    }

    #[test]
    fn sign_in_with_an_unavailable_keychain_fails_closed_before_any_console_call() {
        let server = MockServer::start(vec![created(), Reply::json(200, ACCOUNT_OK)]);
        let custody = Arc::new(MemoryCustody::unavailable());
        let err = run(service(&server.url(), custody).sign_in("a@b.example", PASSWORD)).unwrap_err();
        assert_eq!(err, AuthCode::KeychainUnavailable);
        assert!(server.finish().is_empty(), "no Console session is created that cannot be kept");
    }

    #[test]
    fn sign_in_that_cannot_store_deletes_the_session_it_made() {
        let server = MockServer::start(vec![created(), Reply::status(204)]);
        let custody = Arc::new(MemoryCustody::writes_fail());
        let err = run(service(&server.url(), custody.clone()).sign_in("a@b.example", PASSWORD)).unwrap_err();
        let seen = server.finish();
        assert_eq!(err, AuthCode::KeychainUnavailable);
        assert_eq!(paths(&seen), ["POST /v1/account/sessions/email", "DELETE /v1/account/sessions/current"]);
        assert_eq!(seen[1].header("cookie"), Some(cookie(SECRET).as_str()));
        assert_eq!(custody.snapshot_len(), 0);
    }

    #[test]
    fn a_refused_sign_in_stores_nothing() {
        let server = MockServer::start(vec![Reply::json(401, "{}")]);
        let custody = Arc::new(MemoryCustody::new());
        let err = run(service(&server.url(), custody.clone()).sign_in("a@b.example", PASSWORD)).unwrap_err();
        assert_eq!(err, AuthCode::BadCredentials);
        assert_eq!(custody.snapshot_len(), 0);
    }

    #[test]
    fn a_sign_in_whose_account_read_fails_rolls_back_the_entry_and_the_session() {
        let server = MockServer::start(vec![created(), Reply::status(503), Reply::status(204)]);
        let custody = Arc::new(MemoryCustody::new());
        let err = run(service(&server.url(), custody.clone()).sign_in("a@b.example", PASSWORD)).unwrap_err();
        let seen = server.finish();
        assert_eq!(err, AuthCode::Unavailable);
        assert_eq!(custody.snapshot_len(), 0, "a failed sign in leaves no half signed in state");
        assert_eq!(
            paths(&seen),
            ["POST /v1/account/sessions/email", "GET /v1/account", "DELETE /v1/account/sessions/current"]
        );
    }

    #[test]
    fn signing_in_over_an_old_entry_replaces_it_and_ends_the_old_console_session() {
        let server = MockServer::start(vec![created(), Reply::json(200, ACCOUNT_OK), Reply::status(204)]);
        let custody = Arc::new(MemoryCustody::new());
        custody.set(&addr(), OLD_SECRET).unwrap();
        run(service(&server.url(), custody.clone()).sign_in("a@b.example", PASSWORD)).unwrap();
        let seen = server.finish();
        assert_eq!(custody.get(&addr()), Ok(Some(SECRET.to_string())));
        assert_eq!(seen[2].method, "DELETE");
        assert_eq!(seen[2].header("cookie"), Some(cookie(OLD_SECRET).as_str()));
    }

    // ---- resume --------------------------------------------------------

    #[test]
    fn resume_confirms_the_stored_session_with_console() {
        let server = MockServer::start(vec![Reply::json(200, ACCOUNT_OK)]);
        let custody = Arc::new(MemoryCustody::new());
        custody.set(&addr(), SECRET).unwrap();
        let user = run(service(&server.url(), custody).resume()).unwrap();
        assert_eq!(user.user_id, "u1");
        let seen = server.finish();
        assert_eq!(seen[0].header("cookie"), Some(cookie(SECRET).as_str()));
    }

    #[test]
    fn resume_codes_follow_the_contract_and_an_outage_is_never_no_session() {
        // No entry: no_session without a request.
        let server = MockServer::start(vec![]);
        let custody = Arc::new(MemoryCustody::new());
        assert_eq!(run(service(&server.url(), custody).resume()).unwrap_err(), AuthCode::NoSession);
        assert!(server.finish().is_empty());

        for (status, want) in [
            (401, AuthCode::NoSession),
            (403, AuthCode::Unavailable),
            (404, AuthCode::Unavailable),
            (500, AuthCode::Unavailable),
            (503, AuthCode::Unavailable),
            (429, AuthCode::RateLimited),
        ] {
            let server = MockServer::start(vec![Reply::json(status, "{}")]);
            let custody = Arc::new(MemoryCustody::new());
            custody.set(&addr(), SECRET).unwrap();
            assert_eq!(run(service(&server.url(), custody.clone()).resume()).unwrap_err(), want, "{status}");
            assert_eq!(custody.snapshot_len(), 1, "a resume never deletes the entry");
        }

        // Console cannot be reached: offline.
        let custody = Arc::new(MemoryCustody::new());
        custody.set(&addr(), SECRET).unwrap();
        let svc = service(&format!("http://127.0.0.1:{}", closed_port()), custody);
        assert_eq!(run(svc.resume()).unwrap_err(), AuthCode::Offline);

        // The keychain is unavailable.
        let server = MockServer::start(vec![]);
        let svc = service(&server.url(), Arc::new(MemoryCustody::unavailable()));
        assert_eq!(run(svc.resume()).unwrap_err(), AuthCode::KeychainUnavailable);
    }

    // ---- exchange ------------------------------------------------------

    fn with_entry(server: &MockServer) -> AuthService {
        let custody = Arc::new(MemoryCustody::new());
        custody.set(&addr(), SECRET).unwrap();
        service(&server.url(), custody)
    }

    #[test]
    fn exchange_mints_a_jwt_and_returns_the_cores_answer() {
        let server = MockServer::start(vec![jwt_reply("jwt-a"), Reply::json(200, ANSWER)]);
        let answer = run(with_entry(&server).exchange("gable")).unwrap();
        let seen = server.finish();
        assert_eq!(answer.expires_at_ms, 99);
        assert_eq!(answer.token.expose(), "core-token-sentinel");
        assert_eq!(paths(&seen), ["POST /v1/account/jwts", "POST /api/v1/auth/exchange"]);
        let body: serde_json::Value = serde_json::from_str(&seen[1].body).unwrap();
        assert_eq!(body, serde_json::json!({"jwt": "jwt-a", "org": "gable"}));
    }

    #[test]
    fn a_core_401_is_retried_once_with_a_freshly_minted_jwt() {
        let server = MockServer::start(vec![
            jwt_reply("jwt-a"),
            Reply::json(401, "{}"),
            jwt_reply("jwt-b"),
            Reply::json(200, ANSWER),
        ]);
        let answer = run(with_entry(&server).exchange("gable")).unwrap();
        let seen = server.finish();
        assert_eq!(answer.expires_at_ms, 99);
        assert_eq!(
            paths(&seen),
            ["POST /v1/account/jwts", "POST /api/v1/auth/exchange", "POST /v1/account/jwts", "POST /api/v1/auth/exchange"]
        );
        assert!(seen[1].body.contains("jwt-a") && !seen[1].body.contains("jwt-b"));
        assert!(seen[3].body.contains("jwt-b") && !seen[3].body.contains("jwt-a"), "never the first JWT again");
    }

    #[test]
    fn a_second_core_401_ends_as_unauthorized_after_exactly_two_attempts() {
        let server = MockServer::start(vec![
            jwt_reply("jwt-a"),
            Reply::json(401, "{}"),
            jwt_reply("jwt-b"),
            Reply::json(401, "{}"),
            jwt_reply("jwt-c"),
            Reply::json(200, ANSWER),
        ]);
        assert_eq!(run(with_entry(&server).exchange("gable")).unwrap_err(), AuthCode::Unauthorized);
        assert_eq!(server.finish().len(), 4);
    }

    #[test]
    fn only_a_core_401_is_retried() {
        for (status, want) in [
            (400, AuthCode::BadRequest),
            (403, AuthCode::Forbidden),
            (429, AuthCode::RateLimited),
            (503, AuthCode::Unavailable),
        ] {
            let server = MockServer::start(vec![jwt_reply("jwt-a"), Reply::json(status, "{}"), jwt_reply("jwt-b")]);
            assert_eq!(run(with_entry(&server).exchange("gable")).unwrap_err(), want, "{status}");
            assert_eq!(server.finish().len(), 2, "status {status} is not retried");
        }
    }

    #[test]
    fn a_console_refusal_at_the_mint_is_no_session_and_never_reaches_the_core() {
        for (status, want) in [(401, AuthCode::NoSession), (429, AuthCode::RateLimited), (500, AuthCode::Unavailable)] {
            let server = MockServer::start(vec![Reply::json(status, "{}")]);
            assert_eq!(run(with_entry(&server).exchange("gable")).unwrap_err(), want, "{status}");
            assert_eq!(server.finish().len(), 1);
        }
    }

    #[test]
    fn exchange_without_an_entry_or_with_a_bad_org_makes_no_request() {
        let server = MockServer::start(vec![]);
        let svc = service(&server.url(), Arc::new(MemoryCustody::new()));
        assert_eq!(run(svc.exchange("gable")).unwrap_err(), AuthCode::NoSession);
        let svc = with_entry(&server);
        assert_eq!(run(svc.exchange("Not A Slug")).unwrap_err(), AuthCode::BadRequest);
        let svc = service(&server.url(), Arc::new(MemoryCustody::unavailable()));
        assert_eq!(run(svc.exchange("gable")).unwrap_err(), AuthCode::KeychainUnavailable);
        assert!(server.finish().is_empty());
    }

    #[test]
    fn an_exchange_never_changes_the_stored_entry() {
        let server = MockServer::start(vec![jwt_reply("jwt-a"), Reply::json(403, "{}")]);
        let custody = Arc::new(MemoryCustody::new());
        custody.set(&addr(), SECRET).unwrap();
        let _ = run(service(&server.url(), custody.clone()).exchange("gable"));
        assert_eq!(custody.get(&addr()), Ok(Some(SECRET.to_string())));
    }

    // ---- sign out ------------------------------------------------------

    #[test]
    fn sign_out_deletes_the_console_session_and_clears_the_keychain() {
        let server = MockServer::start(vec![Reply::status(204)]);
        let custody = Arc::new(MemoryCustody::new());
        custody.set(&addr(), SECRET).unwrap();
        run(service(&server.url(), custody.clone()).sign_out()).unwrap();
        let seen = server.finish();
        assert_eq!(custody.get(&addr()), Ok(None));
        assert_eq!(paths(&seen), ["DELETE /v1/account/sessions/current"]);
        assert_eq!(seen[0].header("cookie"), Some(cookie(SECRET).as_str()));
    }

    #[test]
    fn sign_out_clears_the_keychain_even_when_console_is_unreachable_or_hangs_or_refuses() {
        // Unreachable.
        let custody = Arc::new(MemoryCustody::new());
        custody.set(&addr(), SECRET).unwrap();
        let svc = service(&format!("http://127.0.0.1:{}", closed_port()), custody.clone());
        run(svc.sign_out()).unwrap();
        assert_eq!(custody.get(&addr()), Ok(None));

        // Refusing.
        let server = MockServer::start(vec![Reply::json(500, "{}")]);
        let custody = Arc::new(MemoryCustody::new());
        custody.set(&addr(), SECRET).unwrap();
        run(service(&server.url(), custody.clone()).sign_out()).unwrap();
        assert_eq!(custody.get(&addr()), Ok(None));

        // Hanging: bounded by the best effort window, not the client timeout.
        let server = MockServer::start(vec![Reply::hang()]);
        let custody = Arc::new(MemoryCustody::new());
        custody.set(&addr(), SECRET).unwrap();
        let svc = service(&server.url(), custody.clone()).with_best_effort(Duration::from_millis(300));
        let started = Instant::now();
        run(svc.sign_out()).unwrap();
        assert!(started.elapsed() < Duration::from_secs(5));
        assert_eq!(custody.get(&addr()), Ok(None));
    }

    #[test]
    fn sign_out_with_no_entry_is_fine_and_with_an_unavailable_keychain_says_so() {
        let server = MockServer::start(vec![]);
        run(service(&server.url(), Arc::new(MemoryCustody::new())).sign_out()).unwrap();
        assert!(server.finish().is_empty(), "no secret, so no Console call");
        let server = MockServer::start(vec![]);
        let svc = service(&server.url(), Arc::new(MemoryCustody::unavailable()));
        assert_eq!(run(svc.sign_out()).unwrap_err(), AuthCode::KeychainUnavailable);
    }

    // ---- nothing logged, nothing echoed ---------------------------------

    #[test]
    fn no_secret_reaches_an_error_value_on_any_failing_path() {
        let sentinels = [SECRET, OLD_SECRET, PASSWORD, "core-token-sentinel", "jwt-a", "a@b.example"];
        let mut errors: Vec<AuthCode> = Vec::new();
        for status in [400, 401, 403, 429, 500, 503] {
            let server = MockServer::start(vec![Reply::json(status, &format!(r#"{{"message":"{PASSWORD} {SECRET}"}}"#))]);
            errors.push(run(service(&server.url(), Arc::new(MemoryCustody::new())).sign_in("a@b.example", PASSWORD)).unwrap_err());
            let server = MockServer::start(vec![jwt_reply("jwt-a"), Reply::json(status, &format!(r#"{{"error":"{SECRET}"}}"#))]);
            errors.extend(run(with_entry(&server).exchange("gable")).err());
        }
        assert!(errors.len() >= 12);
        for e in errors {
            let text = format!("{e:?} {} {}", e.code(), serde_json::to_string(&e).unwrap());
            for s in sentinels {
                assert!(!text.contains(s), "{text}");
            }
        }
    }

    #[test]
    fn a_sign_out_during_a_sign_in_store_leaves_no_secret_behind() {
        use crate::custody::CustodyError;
        use std::sync::{mpsc, Mutex};

        // A custody whose `set` holds until released, so a sign out can be
        // started while a sign in is between the session and the store.
        struct Gate {
            inner: MemoryCustody,
            entered: Mutex<mpsc::Sender<()>>,
            release: Mutex<mpsc::Receiver<()>>,
        }
        impl Custody for Gate {
            fn get(&self, a: &Address) -> Result<Option<String>, CustodyError> {
                self.inner.get(a)
            }
            fn delete(&self, a: &Address) -> Result<(), CustodyError> {
                self.inner.delete(a)
            }
            fn set(&self, a: &Address, v: &str) -> Result<(), CustodyError> {
                self.entered.lock().unwrap().send(()).unwrap();
                self.release.lock().unwrap().recv().unwrap();
                self.inner.set(a, v)
            }
        }

        let (etx, erx) = mpsc::channel();
        let (rtx, rrx) = mpsc::channel();
        let gate = Arc::new(Gate { inner: MemoryCustody::new(), entered: Mutex::new(etx), release: Mutex::new(rrx) });
        let server = MockServer::start(vec![created(), Reply::json(200, ACCOUNT_OK), Reply::json(204, "")]);
        let cfg = AuthConfig::for_test(&format!("{}/v1", server.url()), PROJECT, &server.url());
        let svc = Arc::new(AuthService::new(PROFILE, &cfg, gate.clone()).unwrap());
        run(async {
            let s_in = svc.clone();
            let signing_in = tokio::spawn(async move { s_in.sign_in("a@b.example", PASSWORD).await });
            tokio::task::spawn_blocking(move || erx.recv().unwrap()).await.unwrap();
            let s_out = svc.clone();
            let signing_out = tokio::spawn(async move { s_out.sign_out().await });
            // Give an unserialised sign out every chance to finish first.
            tokio::time::sleep(Duration::from_millis(300)).await;
            rtx.send(()).unwrap();
            let _ = signing_in.await.unwrap();
            signing_out.await.unwrap().unwrap();
        });
        drop(server);
        assert_eq!(gate.inner.get(&addr()), Ok(None), "the sign out returned, yet a live session secret sits in custody");
    }

    #[test]
    fn the_auth_sources_hold_no_logging_or_printing() {
        // The Rust side never logs the JWT, the session secret or the core
        // token (AUTH-SHELL-CONTRACT r6): nothing in the auth path prints at
        // all, so there is no log line to leak. This scan holds that line for
        // the core's auth modules and for the shell's command module.
        let sources = [
            ("auth.rs", include_str!("auth.rs")),
            ("console.rs", include_str!("console.rs")),
            ("custody.rs", include_str!("custody.rs")),
        ];
        for (name, src) in sources {
            let production = src.split("#[cfg(test)]").next().unwrap();
            for banned in ["println!", "eprintln!", "print!(", "eprint!(", "dbg!", "log::", "tracing::", "info!(", "warn!(", "debug!(", "error!(", "trace!("] {
                assert!(!production.contains(banned), "{name} contains {banned}");
            }
        }
    }
}
