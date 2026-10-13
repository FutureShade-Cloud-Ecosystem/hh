mod auth;

use tauri::{Manager, WebviewUrl, WebviewWindowBuilder};

#[cfg(feature = "probe")]
use hh_shell_core::keychain::{self, Slot};
use hh_shell_core::profiles;
use hh_shell_core::store::{self, SqlRow, SqlStatement, SqlValue, Store};

// The contract's service name: `ai.futurebuild.hh.<profile>`, which is the
// profile's own identifier. One source: the profile model is the definition
// (the test below pins the two together). Rust side only: the auth commands
// are the writers; the webview holds no keychain command.
#[cfg_attr(not(feature = "probe"), allow(dead_code))]
fn keychain_service() -> &'static str {
    profiles::field().identifier
}
// The offline store's fixed database file, named per profile (plan B.3: the
// declared scopes pull into one durable local store). It lives in the app
// config dir; the webview never names a path.
const STORE_FILE: &str = "field.sqlite3";

fn open_store(app: &tauri::AppHandle) -> Result<Store, String> {
    let dir = app.path().app_config_dir().map_err(|e| e.to_string())?;
    std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    store::Store::open_path(&dir.join(STORE_FILE))
}

// The blocking helper keeps long calls (a keyring unlock prompt, the store's
// SQLite work) on a blocking thread, never on the main thread or an async worker.
async fn blocking<T: Send + 'static>(f: impl FnOnce() -> Result<T, String> + Send + 'static) -> Result<T, String> {
    tauri::async_runtime::spawn_blocking(f).await.map_err(|e| e.to_string())?
}

// PROBE SCAFFOLDING: the webview may not read or write the custody slot in a
// real build (the Console session secret is used only on the Rust side,
// AUTH-SHELL-CONTRACT r6; item 7's auth_* commands are its writers), so these
// two exist only for the probe harness, which drives them from probe.js.
#[cfg(feature = "probe")]
#[tauri::command]
async fn keychain_set(slot: Slot, value: String) -> Result<(), String> {
    blocking(move || keychain::set(keychain_service(), slot.name(), &value)).await
}

#[cfg(feature = "probe")]
#[tauri::command]
async fn keychain_get(slot: Slot) -> Result<String, String> {
    blocking(move || keychain::get(keychain_service(), slot.name())).await
}

// The offline store's executor seam: the store package's SQLite backend
// drives these two commands and nothing else. `run_statement` covers the
// seam's run (ignore the rows) and all (take the rows); `run_atomic` is the
// seam's atomic. The database is the profile's one fixed file (STORE_FILE),
// not a webview chosen path, and ATTACH and DETACH are refused inside the
// core (see hh-shell-core::store).
#[tauri::command]
async fn run_statement(
    sql: String,
    params: Option<Vec<SqlValue>>,
    store: tauri::State<'_, Store>,
) -> Result<Vec<SqlRow>, String> {
    let store = store.inner().clone();
    blocking(move || store.run(&sql, params.unwrap_or_default())).await
}

#[tauri::command]
async fn run_atomic(statements: Vec<SqlStatement>, store: tauri::State<'_, Store>) -> Result<(), String> {
    let store = store.inner().clone();
    blocking(move || store.atomic(statements)).await
}

// PROBE SCAFFOLDING: lets the probe delete its own keychain item. Not part of
// a normal build; the real shell decides its own sign out path.
#[cfg(feature = "probe")]
#[tauri::command]
async fn keychain_delete(slot: Slot) -> Result<(), String> {
    blocking(move || keychain::delete(keychain_service(), slot.name())).await
}

// PROBE SCAFFOLDING, removed when the real shell lands: compiled only with
// the `probe` cargo feature (off by default), so a normal build never carries it.
#[cfg(feature = "probe")]
#[tauri::command]
fn probe_report(app: tauri::AppHandle, json: String) {
    println!("HH_PROBE4 {json}");
    // A headless run sets HH_PROBE4_EXIT inline so the process ends by itself.
    if std::env::var_os("HH_PROBE4_EXIT").is_some() {
        app.exit(0);
    }
}

// One startup line naming the profile the shell is running (the gates and
// device legs read it; nothing here is secret: profile facts only, never
// tokens or keys). The update values are the Field channel's configuration
// from tauri.conf.json (pinned by the tests below).
fn profile_line(plugin_config: &tauri::utils::config::PluginConfig) -> String {
    let p = profiles::field();
    let endpoint = plugin_config
        .0
        .get("updater")
        .and_then(|u| u.get("endpoints"))
        .and_then(|e| e.get(0))
        .cloned()
        .unwrap_or(serde_json::Value::Null);
    serde_json::json!({
        "identifier": p.identifier,
        "product": p.product_name,
        "brand": p.brand,
        "channel": p.channel.as_str(),
        "window": { "title": p.window.title, "width": p.window.width, "height": p.window.height },
        "permissions": { "camera": p.permissions.camera, "fine_location": p.permissions.fine_location },
        "updates": { "channel": p.channel.as_str(), "endpoint": endpoint },
        "store": { "file": STORE_FILE },
        "apps": p.apps.iter().map(|a| serde_json::json!({
            "id": a.id,
            "name": a.name,
            "offline": a.offline.as_ref().map(|o| serde_json::json!({
                "mode": o.mode,
                "scopes": o.scopes,
                "conflict_policy": o.conflict_policy,
            })),
        })).collect::<Vec<_>>(),
    })
    .to_string()
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let builder = tauri::Builder::default()
        // The Field channel's updater (see tauri.conf.json plugins.updater and
        // the README): registered, but nothing polls until the shell chrome's
        // update prompt asks, so the placeholder endpoint stays idle.
        .plugin(tauri_plugin_updater::Builder::new().build());
    #[cfg(feature = "probe")]
    let builder = builder
        .plugin(tauri_plugin_sql::Builder::default().build())
        .invoke_handler(tauri::generate_handler![
            keychain_set,
            keychain_get,
            keychain_delete,
            probe_report,
            run_statement,
            run_atomic,
            auth::auth_sign_in,
            auth::auth_resume,
            auth::auth_exchange,
            auth::auth_sign_out
        ]);
    #[cfg(not(feature = "probe"))]
    let builder = builder.invoke_handler(tauri::generate_handler![
        run_statement,
        run_atomic,
        auth::auth_sign_in,
        auth::auth_resume,
        auth::auth_exchange,
        auth::auth_sign_out
    ]);
    builder
        .setup(|app| {
            let profile = profiles::field();
            println!("HH_PROFILE {}", profile_line(&app.config().plugins));
            app.manage(open_store(app.handle()).expect("the profile's store opens"));
            // The sign in commands' state: custody and the Console client, built
            // from the profile's fixed configuration (never the webview's).
            app.manage(auth::AuthState::for_profile());
            let window = WebviewWindowBuilder::new(app, "main", WebviewUrl::default())
                .title(profile.window.title)
                .inner_size(profile.window.width, profile.window.height);
            // PROBE SCAFFOLDING, removed when the real shell lands (see probe.js).
            #[cfg(feature = "probe")]
            let window = window.initialization_script(include_str!("../probe.js"));
            window.build()?;
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running HH Field");
}

#[cfg(test)]
mod tests {
    use super::*;
    // Pins from the probe 4 reviews, plus this item's device permissions. The
    // generated Android tree is source and tracked, so these strings are
    // stable; if Tauri regenerates the project, these tests say what must
    // survive.
    const MANIFEST: &str = include_str!("../gen/android/app/src/main/AndroidManifest.xml");
    const FILE_PATHS: &str = include_str!("../gen/android/app/src/main/res/xml/file_paths.xml");

    #[test]
    fn field_only_device_permissions_are_declared() {
        assert!(
            MANIFEST.contains("<uses-permission android:name=\"android.permission.CAMERA\" />"),
            "the camera permission is missing from the Android manifest"
        );
        assert!(
            MANIFEST.contains(
                "<uses-permission android:name=\"android.permission.ACCESS_FINE_LOCATION\" />"
            ),
            "the fine location permission is missing from the Android manifest"
        );
    }

    #[test]
    fn the_manifest_stays_backup_free_and_rationaled() {
        // Probe 4 review round 1, P3 5: the Keystore custody design depends on
        // the ciphertext never travelling in an auto backup.
        assert!(MANIFEST.contains("android:allowBackup=\"false\""));
        assert!(
            MANIFEST.contains("Field only"),
            "the permission block must carry its rationale as a comment"
        );
    }

    #[test]
    fn the_file_provider_keeps_wrys_capture_root() {
        // Probe 4 review round 2, P2 1: narrowing the FileProvider silently
        // broke the webview's camera capture. Both roots must stay.
        assert!(FILE_PATHS.contains("cache-path"));
        assert!(FILE_PATHS.contains("<external-files-path name=\"captures\" path=\"Pictures/\" />"));
    }

    const TAURI_CONF: &str = include_str!("../tauri.conf.json");

    const CAPABILITY: &str = include_str!("../capabilities/default.json");

    #[test]
    fn the_stores_commands_are_allowed_by_name_and_no_sql_grant_is_open() {
        // Probe 4 review round 1, P2 2: the webview's SQL grant must never be
        // open. The store's engine is the core's fixed file behind two
        // commands, and the capability names exactly those two.
        let cap: serde_json::Value = serde_json::from_str(CAPABILITY).expect("the capability parses");
        let perms: Vec<&str> = cap["permissions"]
            .as_array()
            .expect("a permissions list")
            .iter()
            .map(|p| p.as_str().expect("permission names are strings"))
            .collect();
        assert!(perms.contains(&"allow-run-statement"), "run_statement is allowed by name");
        assert!(perms.contains(&"allow-run-atomic"), "run_atomic is allowed by name");
        assert!(!CAPABILITY.contains("sql:"), "no open SQL grant reaches a real build");
    }

    // The ACL manifest and the merged capabilities that tauri-build wrote for
    // THIS build (the same data the binary embeds, which `strings` on the
    // default binary shows too).
    #[cfg(not(feature = "probe"))]
    const BUILT_ACL_MANIFEST: &str = include_str!(concat!(env!("OUT_DIR"), "/acl-manifests.json"));
    #[cfg(not(feature = "probe"))]
    const BUILT_CAPABILITIES: &str = include_str!(concat!(env!("OUT_DIR"), "/capabilities.json"));

    #[test]
    fn the_default_capability_grants_the_webview_no_keychain_command() {
        // Item 4 review, P2 1: the Console session secret is used only on the
        // Rust side (AUTH-SHELL-CONTRACT r6); the webview's only interface is
        // the auth_* commands of item 7. A keychain grant here would let any
        // script in the window read it back with one invoke.
        assert!(!CAPABILITY.contains("keychain"), "default.json must hold no keychain grant");
    }

    #[cfg(not(feature = "probe"))]
    #[test]
    fn a_default_build_declares_no_keychain_command_in_its_acl_manifest() {
        for built in [BUILT_ACL_MANIFEST, BUILT_CAPABILITIES] {
            assert!(!built.contains("keychain"), "a keychain command or grant is in the default build's ACL");
            assert!(!built.contains("probe"), "probe scaffolding is in the default build's ACL");
        }
        assert!(BUILT_ACL_MANIFEST.contains("run_statement"), "the store's commands are declared");
        assert!(BUILT_ACL_MANIFEST.contains("run_atomic"), "the store's commands are declared");
    }

    #[test]
    fn the_keychain_service_is_the_profiles_identifier() {
        // AUTH-SHELL-CONTRACT r6: the service is ai.futurebuild.hh.<profile>
        // and the account console-session:<project id>, one entry per profile
        // and project. The address is built by the core's custody module from
        // the profile's identifier; the webview names neither part.
        assert_eq!(keychain_service(), profiles::field().identifier);
        assert_eq!(keychain_service(), "ai.futurebuild.hh.field");
        let address = hh_shell_core::custody::Address::console_session(keychain_service(), "proj-1")
            .expect("the Field identifier forms a custody address");
        assert_eq!(address.service(), "ai.futurebuild.hh.field");
        assert_eq!(address.account(), "console-session:proj-1");
    }

    #[test]
    fn the_field_update_channel_is_configured_with_placeholders() {
        let conf: serde_json::Value = serde_json::from_str(TAURI_CONF).expect("tauri.conf.json parses");
        let updater = &conf["plugins"]["updater"];
        let endpoint = updater["endpoints"]
            .as_array()
            .and_then(|e| e.first())
            .and_then(|e| e.as_str())
            .expect("the updater has one configured endpoint");
        assert!(
            endpoint.contains("/hh/field/"),
            "the endpoint names the Field channel: {endpoint}"
        );
        for part in ["{{target}}", "{{arch}}", "{{current_version}}"] {
            assert!(endpoint.contains(part), "the endpoint carries {part}");
        }
        // The verify key in git is a placeholder: the real public key comes
        // from FB Vault with the signing pair the principal holds, never
        // committed. A real key is a long base64 minisign blob; this pin
        // fails the day someone pastes one into a tracked file.
        let pubkey = updater["pubkey"].as_str().expect("the updater has a pubkey");
        assert_eq!(pubkey, "PLACEHOLDER: the release verify public key comes from FB Vault at the first signed release");
        // Release builds produce the signed update artifacts (the CLI asks
        // for the signing key from the build environment, never from git).
        assert_eq!(conf["bundle"]["createUpdaterArtifacts"], serde_json::json!(true));
        assert!(
            MANIFEST.contains("Field only"),
            "the manifest still carries the permission rationale"
        );
    }

    // ---- item 7: the four auth commands -----------------------------------

    const AUTH_COMMANDS: [&str; 4] = ["auth_sign_in", "auth_resume", "auth_exchange", "auth_sign_out"];
    const AUTH_PERMISSIONS: [&str; 4] =
        ["allow-auth-sign-in", "allow-auth-resume", "allow-auth-exchange", "allow-auth-sign-out"];
    const AUTH_SOURCE: &str = include_str!("auth.rs");

    #[test]
    fn the_auth_commands_are_allowed_by_name_to_the_bundled_local_origin_only() {
        let cap: serde_json::Value = serde_json::from_str(CAPABILITY).expect("the capability parses");
        let perms: Vec<&str> = cap["permissions"].as_array().unwrap().iter().map(|p| p.as_str().unwrap()).collect();
        for name in AUTH_PERMISSIONS {
            assert!(perms.contains(&name), "{name} is allowed by name");
        }
        // No wildcard or whole-set grant of the app's commands.
        assert!(perms.iter().all(|p| !p.contains('*')), "no wildcard permission");
        // Local only: a capability with a `remote` block reaches remote URLs;
        // `local: false` would drop the bundled origin. Neither is present.
        assert!(cap.get("remote").is_none(), "the default capability names no remote URL");
        assert!(cap.get("local").is_none_or(|l| l == &serde_json::json!(true)));
        assert_eq!(cap["windows"], serde_json::json!(["main"]), "the main window only");
        assert!(cap.get("webviews").is_none(), "no extra webview is granted");
        // The shell never opens remote domain IPC.
        assert!(!TAURI_CONF.contains("dangerousRemoteDomainIpcAccess"));
        // The probe capability, which is not in a normal build, grants none of them.
        const PROBE_CAPABILITY: &str = include_str!("../capabilities-probe/probe.json");
        assert!(!PROBE_CAPABILITY.contains("auth"), "the probe capability grants no auth command");
    }

    #[cfg(not(feature = "probe"))]
    #[test]
    fn the_auth_commands_are_declared_in_the_built_acl_manifest() {
        for name in AUTH_COMMANDS {
            assert!(BUILT_ACL_MANIFEST.contains(name), "{name} is declared in the ACL manifest");
        }
        for name in AUTH_PERMISSIONS {
            assert!(BUILT_CAPABILITIES.contains(name), "{name} is granted in the merged capabilities");
        }
    }

    #[test]
    fn no_auth_command_takes_an_endpoint_a_project_or_a_core_url() {
        // AUTH-SHELL-CONTRACT r6, "Commands are fenced": those three values
        // are fixed in the Rust profile, never an argument. Read every
        // command signature in the auth module.
        let production = AUTH_SOURCE.split("#[cfg(test)]").next().unwrap();
        let mut commands = 0;
        for chunk in production.split("#[tauri::command]").skip(1) {
            commands += 1;
            let signature = chunk.split("{").next().unwrap().to_ascii_lowercase();
            for banned in ["endpoint", "project", "core_url", "url", "jwt", "secret", "token"] {
                assert!(!signature.contains(banned), "a command signature mentions {banned}: {signature}");
            }
        }
        assert_eq!(commands, 4, "exactly the four contract commands");
    }

    #[test]
    fn the_auth_module_prints_and_logs_nothing() {
        let production = AUTH_SOURCE.split("#[cfg(test)]").next().unwrap();
        for banned in ["println!", "eprintln!", "print!(", "dbg!", "log::", "tracing::", "info!(", "warn!(", "error!("] {
            assert!(!production.contains(banned), "auth.rs contains {banned}");
        }
    }
}
