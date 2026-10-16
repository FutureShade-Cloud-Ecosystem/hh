//! The four sign in commands (AUTH-SHELL-CONTRACT r6, "Rust commands").
//!
//! Thin wrappers over `hh_shell_core::auth::AuthService`, which holds the
//! flows and their tests. The commands take only what the person types
//! (email, password) or chooses (the org slug): the Console endpoint, the
//! project id and the core URL are fixed in the profile's `AuthConfig`, and
//! nothing here prints or logs. An error is `{code}` only.
//!
//! The webview receives the account, or the core's short token; the session
//! secret and the Console JWT never leave Rust.

use std::sync::Arc;

use hh_shell_core::{
    auth::AuthService,
    console::{AccountUser, AuthCode, ExchangeAnswer},
    custody, profiles,
};
use tauri::Emitter;

/// The event `auth_sign_out` emits so every window drops its token.
pub const SIGNED_OUT_EVENT: &str = "auth:signed-out";

/// The managed state. `None` when the build's auth configuration is missing
/// or invalid (no project id, no core URL, a non https URL): every command
/// then answers `unavailable` instead of guessing.
pub struct AuthState(Option<Arc<AuthService>>);

impl AuthState {
    pub fn for_profile() -> Self {
        let profile = profiles::work();
        let custody: Arc<dyn custody::Custody> = Arc::from(custody::platform_custody());
        Self(
            AuthService::new(profile.identifier, &profile.auth, custody)
                .ok()
                .map(Arc::new),
        )
    }

    pub(crate) fn service(&self) -> Result<Arc<AuthService>, AuthCode> {
        self.0.clone().ok_or(AuthCode::Unavailable)
    }
}

#[derive(serde::Serialize)]
pub struct UserAnswer {
    user: AccountUser,
}

/// The `{}` sign out answer.
#[derive(serde::Serialize)]
pub struct Empty {}

#[tauri::command]
pub async fn auth_sign_in(
    email: String,
    password: String,
    state: tauri::State<'_, AuthState>,
) -> Result<UserAnswer, AuthCode> {
    let service = state.service()?;
    service
        .sign_in(&email, &password)
        .await
        .map(|user| UserAnswer { user })
}

#[tauri::command]
pub async fn auth_resume(state: tauri::State<'_, AuthState>) -> Result<UserAnswer, AuthCode> {
    let service = state.service()?;
    service.resume().await.map(|user| UserAnswer { user })
}

#[tauri::command]
pub async fn auth_exchange(
    org: String,
    state: tauri::State<'_, AuthState>,
) -> Result<ExchangeAnswer, AuthCode> {
    let service = state.service()?;
    service.exchange(&org).await
}

#[tauri::command]
pub async fn auth_sign_out(
    app: tauri::AppHandle,
    state: tauri::State<'_, AuthState>,
) -> Result<Empty, AuthCode> {
    let result = match state.service() {
        Ok(service) => service.sign_out().await,
        Err(code) => Err(code),
    };
    // The webview drops its token whether or not the keychain answered.
    let _ = app.emit(SIGNED_OUT_EVENT, ());
    result.map(|()| Empty {})
}
