//! The FB Console and core REST client, Rust side only.
//!
//! What it speaks (STATE-NOTE-AUTH r6 sections 1 and 9, AUTH-SHELL-CONTRACT
//! r6), all against the endpoint, project and core URL fixed in the profile's
//! `AuthConfig`:
//!
//! - `POST {endpoint}/account/sessions/email` `{email, password}` creates the
//!   session. Console 2.1.0 answers 201 with an empty body `secret` and the
//!   session in `Set-Cookie` (`a_session_<project>` and a `_legacy` twin), so
//!   the secret is read from the `a_session_<project>` cookie; nothing else
//!   of the response is kept.
//! - `GET {endpoint}/account` (resume), `POST {endpoint}/account/jwts` (a
//!   short lived JWT) and `DELETE {endpoint}/account/sessions/current`
//!   carry the secret as that same cookie, with `X-Appwrite-Project`.
//! - `POST {core}/api/v1/auth/exchange` `{jwt, org}` with no cookie and no
//!   Console header. The JWT is consumed by the call: a `Jwt` is passed by
//!   value and never held past one attempt, because the core accepts a JWT once.
//!
//! No redirect is followed. TLS is rustls with the ring provider and the
//! Mozilla roots compiled in (one trust path on Linux and Android). Every
//! failure is an `AuthCode` and nothing else: no status text, no body, no
//! input is carried, and the secret types print redacted.

use std::{sync::Arc, time::Duration};

use reqwest::{
    header::{ACCEPT, AUTHORIZATION, CONTENT_TYPE, COOKIE, SET_COOKIE},
    redirect::Policy,
    Method, RequestBuilder,
};
use zeroize::Zeroizing;

use crate::custody::CustodyError;
pub use crate::profiles::AuthConfig;

/// The contract's error words. A command error is `{code}` only.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum AuthCode {
    BadCredentials,
    NoSession,
    Offline,
    KeychainUnavailable,
    BadRequest,
    Forbidden,
    Unauthorized,
    Unavailable,
    RateLimited,
}

impl AuthCode {
    pub fn code(self) -> &'static str {
        match self {
            AuthCode::BadCredentials => "bad_credentials",
            AuthCode::NoSession => "no_session",
            AuthCode::Offline => "offline",
            AuthCode::KeychainUnavailable => "keychain_unavailable",
            AuthCode::BadRequest => "bad_request",
            AuthCode::Forbidden => "forbidden",
            AuthCode::Unauthorized => "unauthorized",
            AuthCode::Unavailable => "unavailable",
            AuthCode::RateLimited => "rate_limited",
        }
    }
}

impl serde::Serialize for AuthCode {
    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        use serde::ser::SerializeStruct;
        let mut s = serializer.serialize_struct("AuthError", 1)?;
        s.serialize_field("code", self.code())?;
        s.end()
    }
}

impl From<CustodyError> for AuthCode {
    fn from(_: CustodyError) -> Self {
        AuthCode::KeychainUnavailable
    }
}

macro_rules! secret_string {
    ($(#[$m:meta])* $name:ident, $max:expr) => {
        $(#[$m])*
        pub struct $name(Zeroizing<String>);

        impl $name {
            /// `None` unless the value is 1 to the maximum length of the characters
            /// a cookie value or a JWT uses (letters, digits and `._~+/=%-`).
            pub fn new(value: String) -> Option<Self> {
                let ok = !value.is_empty()
                    && value.len() <= $max
                    && value.bytes().all(|b| b.is_ascii_alphanumeric() || b"._~+/=%-".contains(&b));
                ok.then(|| Self(Zeroizing::new(value)))
            }

            pub fn expose(&self) -> &str {
                &self.0
            }
        }

        impl std::fmt::Debug for $name {
            fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                write!(f, concat!(stringify!($name), "(..)"))
            }
        }
    };
}

secret_string!(
    /// The FB Console session secret: the value of the `a_session_<project>` cookie.
    SessionSecret,
    4096
);
secret_string!(
    /// An FB Console JWT, good for one exchange attempt.
    Jwt,
    8192
);
secret_string!(
    /// The core's short session token, handed to the webview and nowhere else.
    CoreToken,
    8192
);

impl serde::Serialize for CoreToken {
    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_str(self.expose())
    }
}

/// What `auth_sign_in` and `auth_resume` return (inside `{user}`).
#[derive(Clone, Debug, PartialEq, Eq, serde::Serialize)]
pub struct AccountUser {
    pub user_id: String,
    pub email: String,
    pub name: String,
}

/// The core's user block, in the core's snake case.
#[derive(Clone, Debug, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct ExchangeUser {
    pub user_id: String,
    pub email: String,
    pub display_name: String,
    pub role_code: String,
    pub role_name: String,
    pub tier: i64,
    pub scope: String,
}

/// The core's exchange answer, as the webview receives it.
#[derive(Debug, serde::Serialize)]
pub struct ExchangeAnswer {
    pub token: CoreToken,
    #[serde(rename = "expiresAtMs")]
    pub expires_at_ms: i64,
    pub user: ExchangeUser,
}

const MAX_BODY: usize = 64 * 1024;
const DEFAULT_TIMEOUT: Duration = Duration::from_secs(20);
const MAX_EMAIL: usize = 320;
const MAX_PASSWORD: usize = 256;
const EXCHANGE_PATH: &str = "/api/v1/auth/exchange";
const SIGN_OUT_PATH: &str = "/api/v1/auth/sign-out";

/// The core's org slug shape (`orgSlugPattern` in the core's authz middleware).
pub fn org_ok(org: &str) -> bool {
    let b = org.as_bytes();
    (1..=32).contains(&b.len())
        && (b[0].is_ascii_lowercase() || b[0].is_ascii_digit())
        && b[1..]
            .iter()
            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || b"._-".contains(c))
}

fn build_http(timeout: Duration) -> Result<reqwest::Client, AuthCode> {
    let provider = Arc::new(rustls::crypto::ring::default_provider());
    let mut roots = rustls::RootCertStore::empty();
    roots.extend(webpki_roots::TLS_SERVER_ROOTS.iter().cloned());
    let mut tls = rustls::ClientConfig::builder_with_provider(provider)
        .with_safe_default_protocol_versions()
        .map_err(|_| AuthCode::Unavailable)?
        .with_root_certificates(roots)
        .with_no_client_auth();
    tls.alpn_protocols = vec![b"http/1.1".to_vec()];
    reqwest::Client::builder()
        .tls_backend_preconfigured(tls)
        .redirect(Policy::none())
        .timeout(timeout)
        .connect_timeout(timeout.min(Duration::from_secs(10)))
        .no_proxy()
        .user_agent(concat!("HH-Shell/", env!("CARGO_PKG_VERSION")))
        .build()
        .map_err(|_| AuthCode::Unavailable)
}

/// A transport failure: no route or refused connection is `offline`; a
/// timeout or anything else is `unavailable`.
fn transport(e: &reqwest::Error) -> AuthCode {
    if e.is_timeout() {
        AuthCode::Unavailable
    } else if e.is_connect() {
        AuthCode::Offline
    } else {
        AuthCode::Unavailable
    }
}

/// The body, refused past the cap without buffering the rest.
async fn read_body(mut response: reqwest::Response) -> Result<Vec<u8>, AuthCode> {
    if response
        .content_length()
        .is_some_and(|n| n > MAX_BODY as u64)
    {
        return Err(AuthCode::Unavailable);
    }
    let mut out = Vec::new();
    while let Some(chunk) = response.chunk().await.map_err(|_| AuthCode::Unavailable)? {
        if out.len() + chunk.len() > MAX_BODY {
            return Err(AuthCode::Unavailable);
        }
        out.extend_from_slice(&chunk);
    }
    Ok(out)
}

fn json_object(bytes: &[u8]) -> Result<serde_json::Map<String, serde_json::Value>, AuthCode> {
    match serde_json::from_slice::<serde_json::Value>(bytes) {
        Ok(serde_json::Value::Object(m)) => Ok(m),
        _ => Err(AuthCode::Unavailable),
    }
}

fn non_empty_str(
    m: &serde_json::Map<String, serde_json::Value>,
    key: &str,
) -> Result<String, AuthCode> {
    match m.get(key).and_then(|v| v.as_str()) {
        Some(s) if !s.is_empty() => Ok(s.to_string()),
        _ => Err(AuthCode::Unavailable),
    }
}

/// A Console refusal of a call made with a session: only a 401 says the
/// session is gone, blocked or never existed (`no_session`). A 403 or 404 can
/// come from a proxy, a WAF or a redeploy, so it reads as `unavailable`, never
/// as a verdict that would end a person's session.
fn console_refusal(status: u16) -> AuthCode {
    match status {
        401 => AuthCode::NoSession,
        429 => AuthCode::RateLimited,
        _ => AuthCode::Unavailable,
    }
}

fn core_refusal(status: u16) -> AuthCode {
    match status {
        400 => AuthCode::BadRequest,
        401 => AuthCode::Unauthorized,
        403 => AuthCode::Forbidden,
        429 => AuthCode::RateLimited,
        _ => AuthCode::Unavailable,
    }
}

/// The client for one profile's FB Console project and core.
#[derive(Clone)]
pub struct ConsoleClient {
    http: reqwest::Client,
    endpoint: String,
    project_id: String,
    core_base: String,
    exchange_url: String,
}

impl ConsoleClient {
    /// Validates the configuration (https, no credentials, a project id) and
    /// builds the client. A build with no project id or core URL fails here.
    pub fn new(config: &AuthConfig) -> Result<Self, AuthCode> {
        config.validate().map_err(|_| AuthCode::Unavailable)?;
        Ok(Self {
            http: build_http(DEFAULT_TIMEOUT)?,
            endpoint: config.console_endpoint.trim_end_matches('/').to_string(),
            project_id: config.project_id.clone(),
            core_base: config.core_url.trim_end_matches('/').to_string(),
            exchange_url: format!("{}{}", config.core_url.trim_end_matches('/'), EXCHANGE_PATH),
        })
    }

    /// The same client with another total timeout (tests use a short one).
    pub fn with_timeout(mut self, timeout: Duration) -> Self {
        self.http = build_http(timeout).expect("a client builds");
        self
    }

    /// The Console project id (the custody address is built from it).
    pub fn project_id(&self) -> &str {
        &self.project_id
    }

    fn console(&self, method: Method, path: &str) -> RequestBuilder {
        self.http
            .request(method, format!("{}{}", self.endpoint, path))
            .header("X-Appwrite-Project", &self.project_id)
            .header(ACCEPT, "application/json")
    }

    fn with_session(&self, request: RequestBuilder, secret: &SessionSecret) -> RequestBuilder {
        request.header(
            COOKIE,
            format!("a_session_{}={}", self.project_id, secret.expose()),
        )
    }

    fn with_json(request: RequestBuilder, body: String) -> RequestBuilder {
        request.header(CONTENT_TYPE, "application/json").body(body)
    }

    async fn send(request: RequestBuilder) -> Result<reqwest::Response, AuthCode> {
        request.send().await.map_err(|e| transport(&e))
    }

    /// Email and password sign in. The session secret is read from the
    /// `a_session_<project>` cookie; a response without a usable one is
    /// `unavailable`, never a session.
    pub async fn create_session(
        &self,
        email: &str,
        password: &str,
    ) -> Result<SessionSecret, AuthCode> {
        if email.is_empty()
            || email.len() > MAX_EMAIL
            || password.is_empty()
            || password.len() > MAX_PASSWORD
        {
            return Err(AuthCode::BadCredentials);
        }
        let body =
            Zeroizing::new(serde_json::json!({ "email": email, "password": password }).to_string());
        let request = Self::with_json(
            self.console(Method::POST, "/account/sessions/email"),
            body.to_string(),
        );
        let response = Self::send(request).await?;
        let status = response.status().as_u16();
        match status {
            200..=299 => {}
            429 => return Err(AuthCode::RateLimited),
            300..=399 | 500..=599 => return Err(AuthCode::Unavailable),
            _ => return Err(AuthCode::BadCredentials),
        }
        let wanted = format!("a_session_{}", self.project_id);
        let secret = response
            .headers()
            .get_all(SET_COOKIE)
            .iter()
            .filter_map(|v| v.to_str().ok())
            .filter_map(|line| line.split(';').next())
            .filter_map(|pair| pair.split_once('='))
            .find(|(name, _)| name.trim() == wanted)
            .and_then(|(_, value)| SessionSecret::new(value.trim().to_string()));
        secret.ok_or(AuthCode::Unavailable)
    }

    /// The account under the session: the resume check and the sign in answer.
    pub async fn account(&self, secret: &SessionSecret) -> Result<AccountUser, AuthCode> {
        let response =
            Self::send(self.with_session(self.console(Method::GET, "/account"), secret)).await?;
        let status = response.status().as_u16();
        if !(200..=299).contains(&status) {
            return Err(console_refusal(status));
        }
        let m = json_object(&read_body(response).await?)?;
        let name = m
            .get("name")
            .and_then(|v| v.as_str())
            .ok_or(AuthCode::Unavailable)?
            .to_string();
        Ok(AccountUser {
            user_id: non_empty_str(&m, "$id")?,
            email: non_empty_str(&m, "email")?,
            name,
        })
    }

    /// Mint a fresh Console JWT. Every exchange attempt calls this again.
    pub async fn mint_jwt(&self, secret: &SessionSecret) -> Result<Jwt, AuthCode> {
        let request = Self::with_json(
            self.with_session(self.console(Method::POST, "/account/jwts"), secret),
            "{}".into(),
        );
        let response = Self::send(request).await?;
        let status = response.status().as_u16();
        if !(200..=299).contains(&status) {
            return Err(console_refusal(status));
        }
        let m = json_object(&read_body(response).await?)?;
        let jwt = m
            .get("jwt")
            .and_then(|v| v.as_str())
            .ok_or(AuthCode::Unavailable)?;
        Jwt::new(jwt.to_string()).ok_or(AuthCode::Unavailable)
    }

    /// Delete the current Console session.
    pub async fn delete_session(&self, secret: &SessionSecret) -> Result<(), AuthCode> {
        let response = Self::send(self.with_session(
            self.console(Method::DELETE, "/account/sessions/current"),
            secret,
        ))
        .await?;
        let status = response.status().as_u16();
        if (200..=299).contains(&status) {
            Ok(())
        } else {
            Err(console_refusal(status))
        }
    }

    /// Revoke one core session token at the core's sign out route (HH-678).
    /// The token travels in the body the core's handler requires (one JSON
    /// object with the field `token` only) and as the bearer header, and is
    /// zeroized when this call ends. The token is consumed by the call: it is
    /// passed by value and never held past one attempt.
    pub async fn core_sign_out(&self, token: CoreToken) -> Result<(), AuthCode> {
        let body = Zeroizing::new(serde_json::json!({ "token": token.expose() }).to_string());
        let request = self
            .http
            .post(format!("{}{}", self.core_base, SIGN_OUT_PATH))
            .header(ACCEPT, "application/json")
            .header(CONTENT_TYPE, "application/json")
            .header(AUTHORIZATION, format!("Bearer {}", token.expose()))
            .body(body.to_string());
        drop(token);
        let response = Self::send(request).await?;
        let status = response.status().as_u16();
        if (200..=299).contains(&status) {
            Ok(())
        } else {
            Err(core_refusal(status))
        }
    }

    /// Present one JWT and the org to the core's exchange route. The JWT is
    /// consumed. The org is checked against the core's slug shape first: a bad
    /// one never leaves the machine.
    pub async fn core_exchange(&self, jwt: Jwt, org: &str) -> Result<ExchangeAnswer, AuthCode> {
        if !org_ok(org) {
            return Err(AuthCode::BadRequest);
        }
        let body =
            Zeroizing::new(serde_json::json!({ "jwt": jwt.expose(), "org": org }).to_string());
        drop(jwt);
        let request = self
            .http
            .post(&self.exchange_url)
            .header(ACCEPT, "application/json")
            .header(CONTENT_TYPE, "application/json")
            .body(body.to_string());
        let response = Self::send(request).await?;
        let status = response.status().as_u16();
        if status != 200 {
            return Err(core_refusal(status));
        }
        let m = json_object(&read_body(response).await?)?;
        let token = CoreToken::new(non_empty_str(&m, "token")?).ok_or(AuthCode::Unavailable)?;
        let expires_at_ms = m
            .get("expiresAtMs")
            .and_then(|v| v.as_i64())
            .ok_or(AuthCode::Unavailable)?;
        let user = m.get("user").cloned().ok_or(AuthCode::Unavailable)?;
        let user: ExchangeUser = serde_json::from_value(user).map_err(|_| AuthCode::Unavailable)?;
        if user.user_id.is_empty() {
            return Err(AuthCode::Unavailable);
        }
        Ok(ExchangeAnswer {
            token,
            expires_at_ms,
            user,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mock_http::{closed_port, MockServer, Reply};
    use std::time::Duration;

    const PROJECT: &str = "proj-1";
    const SECRET: &str = "c2VjcmV0LXZhbHVl";

    fn client(server: &MockServer) -> ConsoleClient {
        let cfg = AuthConfig::for_test(&format!("{}/v1", server.url()), PROJECT, &server.url());
        ConsoleClient::new(&cfg).expect("the test config is valid")
    }

    fn secret() -> SessionSecret {
        SessionSecret::new(SECRET.to_string()).unwrap()
    }

    fn session_created() -> Reply {
        Reply::json(
            201,
            r#"{"$id":"s1","userId":"u1","provider":"email","secret":""}"#,
        )
        .header(
            "Set-Cookie",
            &format!("a_session_{PROJECT}={SECRET}; Path=/; HttpOnly; Secure; SameSite=None"),
        )
        .header(
            "Set-Cookie",
            &format!("a_session_{PROJECT}_legacy={SECRET}; Path=/; HttpOnly"),
        )
    }

    const ACCOUNT_OK: &str =
        r#"{"$id":"u1","email":"a@b.example","name":"A B","status":true,"emailVerification":true}"#;

    fn run<F: std::future::Future>(f: F) -> F::Output {
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap()
            .block_on(f)
    }

    // ---- configuration -------------------------------------------------

    #[test]
    fn the_config_requires_https_a_project_id_and_no_extras() {
        assert!(AuthConfig::new(
            "https://api.example.test/v1",
            "proj-1",
            "https://core.example.test"
        )
        .is_ok());
        for (e, p, c) in [
            (
                "http://api.example.test/v1",
                "proj-1",
                "https://core.example.test",
            ),
            (
                "https://api.example.test/v1",
                "proj-1",
                "http://core.example.test",
            ),
            (
                "https://api.example.test/v1",
                "",
                "https://core.example.test",
            ),
            (
                "https://api.example.test/v1",
                "a b",
                "https://core.example.test",
            ),
            (
                "https://u:p@api.example.test/v1",
                "proj-1",
                "https://core.example.test",
            ),
            (
                "https://api.example.test/v1?x=1",
                "proj-1",
                "https://core.example.test",
            ),
            (
                "https://api.example.test/v1#f",
                "proj-1",
                "https://core.example.test",
            ),
            ("", "proj-1", "https://core.example.test"),
            ("https://api.example.test/v1", "proj-1", ""),
        ] {
            assert!(AuthConfig::new(e, p, c).is_err(), "{e} {p} {c}");
        }
    }

    #[test]
    fn the_field_profile_carries_its_auth_configuration_not_the_webview() {
        let a = &crate::profiles::field().auth;
        // Fixed in the profile: three plain values, read at build time, with the
        // Console endpoint defaulted and the project id and core URL empty
        // (so an unconfigured build refuses to sign in rather than guessing).
        assert!(a.console_endpoint.starts_with("https://"));
        let _ = (&a.project_id, &a.core_url);
    }

    // ---- sign in -------------------------------------------------------

    #[test]
    fn create_session_posts_the_credentials_and_reads_the_secret_from_set_cookie() {
        let server = MockServer::start(vec![session_created()]);
        let got = run(client(&server).create_session("a@b.example", "pw-1"));
        let seen = server.finish();
        assert_eq!(got.unwrap().expose(), SECRET);
        assert_eq!(seen.len(), 1);
        assert_eq!(seen[0].method, "POST");
        assert_eq!(seen[0].path, "/v1/account/sessions/email");
        assert_eq!(seen[0].header("x-appwrite-project"), Some(PROJECT));
        assert_eq!(seen[0].header("content-type"), Some("application/json"));
        assert!(
            seen[0].header("cookie").is_none(),
            "a sign in sends no cookie"
        );
        let body: serde_json::Value = serde_json::from_str(&seen[0].body).unwrap();
        assert_eq!(
            body,
            serde_json::json!({"email": "a@b.example", "password": "pw-1"})
        );
    }

    #[test]
    fn create_session_maps_every_refusal_to_a_contract_code() {
        for (status, want) in [
            (400, AuthCode::BadCredentials),
            (401, AuthCode::BadCredentials),
            (403, AuthCode::BadCredentials),
            (404, AuthCode::BadCredentials),
            (429, AuthCode::RateLimited),
            (500, AuthCode::Unavailable),
            (503, AuthCode::Unavailable),
            (302, AuthCode::Unavailable),
        ] {
            let server = MockServer::start(vec![Reply::json(status, r#"{"message":"x"}"#)]);
            let got = run(client(&server).create_session("a@b.example", "pw-1"));
            assert_eq!(got.unwrap_err(), want, "status {status}");
        }
    }

    #[test]
    fn create_session_without_a_usable_cookie_is_unavailable_not_a_session() {
        // No session cookie, only the legacy twin, an unsafe value, or an empty one.
        for cookies in [
            vec![],
            vec![format!("a_session_{PROJECT}_legacy={SECRET}; Path=/")],
            vec![format!("a_session_{PROJECT}=bad value; Path=/")],
            vec![format!("a_session_{PROJECT}=; Path=/")],
            vec![format!("a_session_other={SECRET}; Path=/")],
        ] {
            let mut reply = Reply::json(201, "{}");
            for c in &cookies {
                reply = reply.header("Set-Cookie", c);
            }
            let server = MockServer::start(vec![reply]);
            let got = run(client(&server).create_session("a@b.example", "pw-1"));
            assert_eq!(got.unwrap_err(), AuthCode::Unavailable, "{cookies:?}");
        }
    }

    #[test]
    fn create_session_refuses_empty_or_oversized_credentials_without_a_request() {
        let server = MockServer::start(vec![session_created()]);
        let c = client(&server);
        assert_eq!(
            run(c.create_session("", "pw")).unwrap_err(),
            AuthCode::BadCredentials
        );
        assert_eq!(
            run(c.create_session("a@b.example", "")).unwrap_err(),
            AuthCode::BadCredentials
        );
        assert_eq!(
            run(c.create_session(&"a".repeat(400), "pw")).unwrap_err(),
            AuthCode::BadCredentials
        );
        assert_eq!(
            run(c.create_session("a@b.example", &"p".repeat(300))).unwrap_err(),
            AuthCode::BadCredentials
        );
        assert!(server.finish().is_empty());
    }

    #[test]
    fn a_console_that_cannot_be_reached_is_offline_and_one_that_hangs_is_unavailable() {
        let cfg = AuthConfig::for_test(
            &format!("http://127.0.0.1:{}/v1", closed_port()),
            PROJECT,
            "http://127.0.0.1:1",
        );
        let c = ConsoleClient::new(&cfg).unwrap();
        assert_eq!(
            run(c.create_session("a@b.example", "pw")).unwrap_err(),
            AuthCode::Offline
        );
        assert_eq!(run(c.account(&secret())).unwrap_err(), AuthCode::Offline);
        assert_eq!(run(c.mint_jwt(&secret())).unwrap_err(), AuthCode::Offline);

        let server = MockServer::start(vec![Reply::hang()]);
        let c = client(&server).with_timeout(Duration::from_millis(300));
        assert_eq!(
            run(c.create_session("a@b.example", "pw")).unwrap_err(),
            AuthCode::Unavailable
        );
    }

    #[test]
    fn a_redirect_is_never_followed() {
        let server = MockServer::start(vec![
            Reply::status(302).header("Location", "http://127.0.0.1:1/elsewhere"),
            Reply::json(200, ACCOUNT_OK),
        ]);
        let got = run(client(&server).account(&secret()));
        let seen = server.finish();
        assert_eq!(got.unwrap_err(), AuthCode::Unavailable);
        assert_eq!(seen.len(), 1, "the redirect target was not requested");
    }

    // ---- resume --------------------------------------------------------

    #[test]
    fn account_sends_the_secret_as_the_session_cookie_and_reads_three_fields() {
        let server = MockServer::start(vec![Reply::json(200, ACCOUNT_OK)]);
        let user = run(client(&server).account(&secret())).unwrap();
        let seen = server.finish();
        assert_eq!(
            user,
            AccountUser {
                user_id: "u1".into(),
                email: "a@b.example".into(),
                name: "A B".into()
            }
        );
        assert_eq!(seen[0].method, "GET");
        assert_eq!(seen[0].path, "/v1/account");
        assert_eq!(
            seen[0].header("cookie"),
            Some(format!("a_session_{PROJECT}={SECRET}").as_str())
        );
        assert_eq!(seen[0].header("x-appwrite-project"), Some(PROJECT));
    }

    #[test]
    fn account_maps_only_a_401_to_no_session_and_every_other_refusal_to_unavailable() {
        for (status, want) in [
            (401, AuthCode::NoSession),
            (403, AuthCode::Unavailable),
            (404, AuthCode::Unavailable),
            (429, AuthCode::RateLimited),
            (500, AuthCode::Unavailable),
            (502, AuthCode::Unavailable),
            (503, AuthCode::Unavailable),
        ] {
            let server = MockServer::start(vec![Reply::json(status, "{}")]);
            assert_eq!(
                run(client(&server).account(&secret())).unwrap_err(),
                want,
                "status {status}"
            );
        }
    }

    #[test]
    fn an_account_answer_short_of_an_id_an_email_and_a_name_is_unavailable() {
        for body in [
            "{}",
            r#"{"$id":"","email":"a@b.example","name":"x"}"#,
            r#"{"$id":"u1","email":3,"name":"x"}"#,
            r#"{"$id":"u1","email":"a@b.example"}"#,
            "not json",
            "[]",
        ] {
            let server = MockServer::start(vec![Reply::json(200, body)]);
            assert_eq!(
                run(client(&server).account(&secret())).unwrap_err(),
                AuthCode::Unavailable,
                "{body}"
            );
        }
    }

    // ---- JWT mint and session delete -----------------------------------

    #[test]
    fn mint_jwt_posts_with_the_cookie_and_returns_the_jwt() {
        let server = MockServer::start(vec![Reply::json(201, r#"{"jwt":"jwt-value-1"}"#)]);
        let jwt = run(client(&server).mint_jwt(&secret())).unwrap();
        let seen = server.finish();
        assert_eq!(jwt.expose(), "jwt-value-1");
        assert_eq!(seen[0].method, "POST");
        assert_eq!(seen[0].path, "/v1/account/jwts");
        assert_eq!(
            seen[0].header("cookie"),
            Some(format!("a_session_{PROJECT}={SECRET}").as_str())
        );
        assert_eq!(seen[0].body, "{}");
    }

    #[test]
    fn mint_jwt_maps_only_a_401_to_no_session() {
        for (status, want) in [
            (401, AuthCode::NoSession),
            (403, AuthCode::Unavailable),
            (404, AuthCode::Unavailable),
            (429, AuthCode::RateLimited),
            (500, AuthCode::Unavailable),
            (503, AuthCode::Unavailable),
        ] {
            let server = MockServer::start(vec![Reply::json(status, "{}")]);
            assert_eq!(
                run(client(&server).mint_jwt(&secret())).unwrap_err(),
                want,
                "status {status}"
            );
        }
        for body in ["{}", r#"{"jwt":""}"#, r#"{"jwt":3}"#, "nope"] {
            let server = MockServer::start(vec![Reply::json(201, body)]);
            assert_eq!(
                run(client(&server).mint_jwt(&secret())).unwrap_err(),
                AuthCode::Unavailable,
                "{body}"
            );
        }
    }

    #[test]
    fn delete_session_deletes_the_current_session_with_the_cookie() {
        let server = MockServer::start(vec![Reply::status(204)]);
        run(client(&server).delete_session(&secret())).unwrap();
        let seen = server.finish();
        assert_eq!(seen[0].method, "DELETE");
        assert_eq!(seen[0].path, "/v1/account/sessions/current");
        assert_eq!(
            seen[0].header("cookie"),
            Some(format!("a_session_{PROJECT}={SECRET}").as_str())
        );

        let server = MockServer::start(vec![Reply::json(401, "{}")]);
        assert_eq!(
            run(client(&server).delete_session(&secret())).unwrap_err(),
            AuthCode::NoSession
        );
    }

    // ---- the core's exchange -------------------------------------------

    const ANSWER: &str = r#"{"token":"core-token-1","expiresAtMs":1234567,"user":{"user_id":"u1","email":"a@b.example","display_name":"A B","role_code":"driver","role_name":"Driver","tier":2,"scope":"branch"}}"#;

    fn jwt(v: &str) -> Jwt {
        Jwt::new(v.to_string()).unwrap()
    }

    #[test]
    fn exchange_posts_the_jwt_and_org_to_the_core_with_no_console_headers() {
        let server = MockServer::start(vec![Reply::json(200, ANSWER)]);
        let answer = run(client(&server).core_exchange(jwt("jwt-1"), "gable")).unwrap();
        let seen = server.finish();
        assert_eq!(answer.expires_at_ms, 1234567);
        assert_eq!(answer.user.role_code, "driver");
        assert_eq!(answer.token.expose(), "core-token-1");
        assert_eq!(seen[0].method, "POST");
        assert_eq!(seen[0].path, "/api/v1/auth/exchange");
        assert!(seen[0].header("cookie").is_none());
        assert!(seen[0].header("x-appwrite-project").is_none());
        assert!(seen[0].header("authorization").is_none());
        let body: serde_json::Value = serde_json::from_str(&seen[0].body).unwrap();
        assert_eq!(body, serde_json::json!({"jwt": "jwt-1", "org": "gable"}));
    }

    #[test]
    fn exchange_maps_the_cores_refusals_to_contract_codes() {
        for (status, want) in [
            (400, AuthCode::BadRequest),
            (401, AuthCode::Unauthorized),
            (403, AuthCode::Forbidden),
            (429, AuthCode::RateLimited),
            (503, AuthCode::Unavailable),
            (500, AuthCode::Unavailable),
            (404, AuthCode::Unavailable),
        ] {
            let server = MockServer::start(vec![Reply::json(status, r#"{"error":"Forbidden"}"#)]);
            assert_eq!(
                run(client(&server).core_exchange(jwt("j"), "gable")).unwrap_err(),
                want,
                "status {status}"
            );
        }
    }

    #[test]
    fn exchange_refuses_a_bad_org_locally_and_a_bad_answer_as_unavailable() {
        let server = MockServer::start(vec![Reply::json(200, ANSWER)]);
        let c = client(&server);
        for org in ["", "Gable", "a b", "-x", &"a".repeat(33), "a/b", "a\"b"] {
            assert_eq!(
                run(c.core_exchange(jwt("j"), org)).unwrap_err(),
                AuthCode::BadRequest,
                "{org:?}"
            );
        }
        assert!(
            server.finish().is_empty(),
            "a bad org never leaves the machine"
        );

        for body in [
            "{}",
            r#"{"token":"","expiresAtMs":1,"user":{}}"#,
            r#"{"token":"t","expiresAtMs":"x","user":{}}"#,
            r#"{"token":"t","expiresAtMs":1,"user":{"user_id":"u"}}"#,
            "nope",
        ] {
            let server = MockServer::start(vec![Reply::json(200, body)]);
            assert_eq!(
                run(client(&server).core_exchange(jwt("j"), "gable")).unwrap_err(),
                AuthCode::Unavailable,
                "{body}"
            );
        }
    }

    #[test]
    fn an_oversized_answer_is_refused_without_buffering_it_all() {
        let big = format!(
            r#"{{"token":"{}","expiresAtMs":1,"user":{{}}}}"#,
            "x".repeat(200_000)
        );
        let server = MockServer::start(vec![Reply::json(200, &big)]);
        assert_eq!(
            run(client(&server).core_exchange(jwt("j"), "gable")).unwrap_err(),
            AuthCode::Unavailable
        );
    }

    /// A real TLS handshake with the compiled in roots (not run by default:
    /// it needs the network). A neutral public host stands in for Console:
    /// the answer to the unknown route is a refusal or an outage word, but a
    /// handshake that failed (an untrusted or unreachable host) reads as
    /// `offline`. Run with `-- --ignored live_tls`.
    #[test]
    #[ignore = "needs the network"]
    fn live_tls_handshake_uses_the_compiled_roots() {
        let cfg =
            AuthConfig::new("https://example.com/v1", "proj-1", "https://example.com").unwrap();
        let got = run(ConsoleClient::new(&cfg).unwrap().account(&secret()));
        assert!(
            !matches!(got, Err(AuthCode::Offline)),
            "the handshake failed: {got:?}"
        );
    }

    // ---- the core's session revocation ----------------------------------

    fn core_token(v: &str) -> CoreToken {
        CoreToken::new(v.to_string()).unwrap()
    }

    #[test]
    fn core_sign_out_posts_the_token_to_the_sign_out_route_as_the_body_and_the_bearer() {
        let server = MockServer::start(vec![Reply::json(200, r#"{"ok":true}"#)]);
        run(client(&server).core_sign_out(core_token("tok-secret-1"))).unwrap();
        let seen = server.finish();
        assert_eq!(seen.len(), 1);
        assert_eq!(seen[0].method, "POST");
        assert_eq!(seen[0].path, "/api/v1/auth/sign-out");
        assert_eq!(seen[0].header("content-type"), Some("application/json"));
        assert_eq!(seen[0].header("authorization"), Some("Bearer tok-secret-1"));
        assert!(seen[0].header("cookie").is_none());
        assert!(seen[0].header("x-appwrite-project").is_none());
        let body: serde_json::Value = serde_json::from_str(&seen[0].body).unwrap();
        assert_eq!(body, serde_json::json!({"token": "tok-secret-1"}));
    }

    #[test]
    fn core_sign_out_maps_the_cores_refusals_to_contract_codes() {
        for (status, want) in [
            (400, AuthCode::BadRequest),
            (401, AuthCode::Unauthorized),
            (403, AuthCode::Forbidden),
            (429, AuthCode::RateLimited),
            (500, AuthCode::Unavailable),
            (503, AuthCode::Unavailable),
            (404, AuthCode::Unavailable),
        ] {
            let server = MockServer::start(vec![Reply::json(status, r#"{"error":"x"}"#)]);
            assert_eq!(
                run(client(&server).core_sign_out(core_token("tok-1"))).unwrap_err(),
                want,
                "status {status}"
            );
        }
    }

    #[test]
    fn an_unreachable_core_is_offline_for_the_sign_out_too() {
        let cfg = AuthConfig::for_test(
            &format!("http://127.0.0.1:{}/v1", closed_port()),
            PROJECT,
            &format!("http://127.0.0.1:{}", closed_port()),
        );
        let c = ConsoleClient::new(&cfg).unwrap();
        assert_eq!(
            run(c.core_sign_out(core_token("tok-1"))).unwrap_err(),
            AuthCode::Offline
        );
    }

    // ---- nothing echoed ------------------------------------------------

    #[test]
    fn errors_and_debug_forms_carry_codes_only_and_secrets_print_redacted() {
        for code in [
            AuthCode::BadCredentials,
            AuthCode::NoSession,
            AuthCode::Offline,
            AuthCode::KeychainUnavailable,
            AuthCode::BadRequest,
            AuthCode::Forbidden,
            AuthCode::Unauthorized,
            AuthCode::Unavailable,
            AuthCode::RateLimited,
        ] {
            assert_eq!(
                serde_json::to_value(code).unwrap(),
                serde_json::json!({ "code": code.code() })
            );
        }
        assert_eq!(AuthCode::KeychainUnavailable.code(), "keychain_unavailable");
        assert_eq!(AuthCode::RateLimited.code(), "rate_limited");
        let s = secret();
        let j = jwt("jwt-secret-value");
        assert!(!format!("{s:?}").contains(SECRET));
        assert!(!format!("{j:?}").contains("jwt-secret-value"));
        let answer = ExchangeAnswer {
            token: CoreToken::new("tok-secret".into()).unwrap(),
            expires_at_ms: 1,
            user: serde_json::from_value::<ExchangeUser>(
                serde_json::from_str::<serde_json::Value>(ANSWER).unwrap()["user"].clone(),
            )
            .unwrap(),
        };
        assert!(!format!("{answer:?}").contains("tok-secret"));
    }

    #[test]
    fn the_failing_paths_never_put_input_in_the_error() {
        // The credentials and the secret appear in no error value or its debug
        // form, whatever the Console answers.
        let server = MockServer::start(vec![Reply::json(
            401,
            r#"{"message":"bad pw-unique-77 a@unique.example"}"#,
        )]);
        let err =
            run(client(&server).create_session("a@unique.example", "pw-unique-77")).unwrap_err();
        for text in [
            format!("{err:?}"),
            err.code().to_string(),
            serde_json::to_string(&err).unwrap(),
        ] {
            assert!(
                !text.contains("pw-unique-77") && !text.contains("unique.example"),
                "{text}"
            );
        }
    }
}
