mod auth;

use tauri::{Manager, WebviewUrl, WebviewWindowBuilder};

use hh_shell_core::profiles;
use hh_shell_core::store::{self, SqlRow, SqlStatement, SqlValue, Store};

// The offline store's fixed database file, named per profile (plan B.3: the
// declared scopes pull into one durable local store). It lives in the app
// config dir; the webview never names a path. Work's file is its own, never
// Field's: the two profiles are different installs with different identifiers.
const STORE_FILE: &str = "work.sqlite3";

fn open_store(app: &tauri::AppHandle) -> Result<Store, String> {
    let dir = app.path().app_config_dir().map_err(|e| e.to_string())?;
    std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    store::Store::open_path(&dir.join(STORE_FILE))
}

// The blocking helper keeps the store's SQLite work on a blocking thread,
// never on the main thread or an async worker.
async fn blocking<T: Send + 'static>(
    f: impl FnOnce() -> Result<T, String> + Send + 'static,
) -> Result<T, String> {
    tauri::async_runtime::spawn_blocking(f)
        .await
        .map_err(|e| e.to_string())?
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
async fn run_atomic(
    statements: Vec<SqlStatement>,
    store: tauri::State<'_, Store>,
) -> Result<(), String> {
    let store = store.inner().clone();
    blocking(move || store.atomic(statements)).await
}

// The built shell's role leg proof (feature `shell-proof`, off by default,
// compiled out of every shipped build): proof.js is injected into every frame
// of the main window, drives the host page through its role leg and reports
// one JSON string here. The command prints the single HH_SHELL_PROOF line
// and ends the run. The webview cannot turn the mode on (the gate is a
// compile time feature, there is no command that enables it) and the command
// grants nothing else: it reads and writes nothing.
#[cfg(feature = "shell-proof")]
#[tauri::command]
fn proof_report(app: tauri::AppHandle, json: String) {
    println!("HH_SHELL_PROOF {json}");
    app.exit(0);
}

// The proof run ends by itself even if the page never reports: after this
// long it prints a timeout line and exits non zero.
#[cfg(feature = "shell-proof")]
const PROOF_WATCHDOG_SECS: u64 = 120;

#[cfg(feature = "shell-proof")]
fn start_proof_watchdog(app: tauri::AppHandle) {
    std::thread::spawn(move || {
        std::thread::sleep(std::time::Duration::from_secs(PROOF_WATCHDOG_SECS));
        println!("HH_SHELL_PROOF {{\"ok\":false,\"error\":\"the page did not report in time\"}}");
        app.exit(1);
    });
}

// One startup line naming the profile the shell is running (the gates and
// device legs read it; nothing here is secret: profile facts only, never
// tokens or keys). The update values are the Work channel's configuration
// from tauri.conf.json (pinned by the tests below).
fn profile_line(plugin_config: &tauri::utils::config::PluginConfig) -> String {
    let p = profiles::work();
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

// `hh-work --print-profile` runs the whole startup (the profile line, the
// store opening, the window building) and then ends the run by itself. It is
// how the desktop gate and a device leg read the profile line without a
// signal; it grants nothing and prints nothing the line does not.
fn print_profile_only(args: impl Iterator<Item = String>) -> bool {
    args.skip(1).any(|a| a == "--print-profile")
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let builder = tauri::Builder::default()
        // The Work channel's updater (see tauri.conf.json plugins.updater and
        // the README): registered, but nothing polls until the shell chrome's
        // update prompt asks, so the placeholder endpoint stays idle.
        .plugin(tauri_plugin_updater::Builder::new().build());
    #[cfg(feature = "shell-proof")]
    let builder = builder.invoke_handler(tauri::generate_handler![
        proof_report,
        run_statement,
        run_atomic,
        auth::auth_sign_in,
        auth::auth_resume,
        auth::auth_exchange,
        auth::auth_sign_out
    ]);
    #[cfg(not(feature = "shell-proof"))]
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
            let profile = profiles::work();
            println!("HH_PROFILE {}", profile_line(&app.config().plugins));
            app.manage(open_store(app.handle()).expect("the profile's store opens"));
            // The sign in commands' state: custody and the Console client, built
            // from the profile's fixed configuration (never the webview's).
            app.manage(auth::AuthState::for_profile());
            #[cfg(feature = "shell-proof")]
            start_proof_watchdog(app.handle().clone());
            let window = WebviewWindowBuilder::new(app, "main", WebviewUrl::default())
                .title(profile.window.title)
                .inner_size(profile.window.width, profile.window.height);
            // The proof driver reaches the front door's frame as well, which
            // answers the demo mint locally (there is no core in this run).
            #[cfg(feature = "shell-proof")]
            let window = window.initialization_script_for_all_frames(include_str!("../proof.js"));
            window.build()?;
            if print_profile_only(std::env::args()) {
                app.handle().exit(0);
            }
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running HH Work");
}

#[cfg(test)]
mod tests {
    use hh_shell_core::profiles;

    const TAURI_CONF: &str = include_str!("../tauri.conf.json");
    const CAPABILITY: &str = include_str!("../capabilities/default.json");
    const CARGO_TOML: &str = include_str!("../Cargo.toml");

    // The ACL manifest and the merged capabilities that tauri-build wrote for
    // THIS build (the same data the binary embeds).
    const BUILT_ACL_MANIFEST: &str = include_str!(concat!(env!("OUT_DIR"), "/acl-manifests.json"));
    const BUILT_CAPABILITIES: &str = include_str!(concat!(env!("OUT_DIR"), "/capabilities.json"));

    fn capability_permissions() -> Vec<String> {
        let cap: serde_json::Value =
            serde_json::from_str(CAPABILITY).expect("the capability parses");
        cap["permissions"]
            .as_array()
            .expect("a permissions list")
            .iter()
            .map(|p| {
                p.as_str()
                    .expect("permission names are strings")
                    .to_string()
            })
            .collect()
    }

    #[test]
    fn the_stores_commands_are_allowed_by_name_and_no_sql_grant_is_open() {
        let perms = capability_permissions();
        assert!(
            perms.iter().any(|p| p == "allow-run-statement"),
            "run_statement is allowed by name"
        );
        assert!(
            perms.iter().any(|p| p == "allow-run-atomic"),
            "run_atomic is allowed by name"
        );
        assert!(
            !CAPABILITY.contains("sql:"),
            "no open SQL grant reaches a real build"
        );
        assert!(
            !CARGO_TOML.contains("tauri-plugin-sql"),
            "the webview SQL plugin is not a dependency"
        );
    }

    #[test]
    fn the_default_capability_is_exactly_the_work_windows_grants() {
        // The shape is pinned: a new grant is a reviewed change to this list.
        let mut perms = capability_permissions();
        perms.sort();
        assert_eq!(
            perms,
            [
                "allow-auth-exchange",
                "allow-auth-resume",
                "allow-auth-sign-in",
                "allow-auth-sign-out",
                "allow-run-atomic",
                "allow-run-statement",
                "core:default",
                "updater:default"
            ]
        );
        let cap: serde_json::Value = serde_json::from_str(CAPABILITY).unwrap();
        assert_eq!(cap["windows"], serde_json::json!(["main"]));
    }

    #[test]
    fn the_default_capability_grants_the_webview_no_keychain_command() {
        // The Console session secret is used only on the Rust side
        // (AUTH-SHELL-CONTRACT r7); a keychain grant would let any script in
        // the window read it back with one invoke.
        assert!(
            !CAPABILITY.contains("keychain"),
            "default.json must hold no keychain grant"
        );
    }

    #[test]
    fn a_work_build_declares_no_keychain_probe_camera_or_location_in_its_acl() {
        for built in [BUILT_ACL_MANIFEST, BUILT_CAPABILITIES] {
            assert!(
                !built.contains("keychain"),
                "a keychain command or grant is in the Work build's ACL"
            );
            assert!(
                !built.contains("probe"),
                "probe scaffolding is in the Work build's ACL"
            );
            let lower = built.to_lowercase();
            for banned in ["camera", "geolocation", "location"] {
                assert!(
                    !lower.contains(banned),
                    "{banned} is Field only and is in the Work build's ACL"
                );
            }
        }
        assert!(
            BUILT_ACL_MANIFEST.contains("run_statement"),
            "the store's commands are declared"
        );
        assert!(
            BUILT_ACL_MANIFEST.contains("run_atomic"),
            "the store's commands are declared"
        );
    }

    #[test]
    fn the_work_crate_has_no_probe_feature_and_one_gated_proof_feature() {
        // Field keeps a probe feature for the probe 4 proof runs; Work never had
        // a probe. Work has exactly one cargo feature, `shell-proof` (the built
        // shell's role leg proof, item 8c), off by default.
        assert!(
            !CARGO_TOML.contains("probe"),
            "Work carries no probe scaffolding"
        );
        let features = CARGO_TOML
            .split("[features]")
            .nth(1)
            .expect("a [features] table");
        let features = features.split("\n[").next().unwrap();
        let declared: Vec<&str> = features
            .lines()
            .map(str::trim)
            .filter(|l| !l.is_empty() && !l.starts_with('#'))
            .collect();
        assert_eq!(
            declared,
            ["default = []", "shell-proof = []"],
            "the feature table is exactly default and shell-proof"
        );
    }

    // The proof mode's source files, read from disk so a missing file is an
    // assertion failure here and not a build break in the default build.
    fn crate_file(rel: &str) -> String {
        std::fs::read_to_string(concat!(env!("CARGO_MANIFEST_DIR"), "/").to_string() + rel)
            .unwrap_or_default()
    }

    #[test]
    fn the_proof_mode_is_compiled_in_only_with_its_feature() {
        let lib = include_str!("lib.rs");
        let production = lib.split("#[cfg(test)]").next().unwrap();
        // every use of the script and the report command sits under the gate
        for needle in [
            "include_str!(\"../proof.js\")",
            "fn proof_report",
            "proof_report,",
        ] {
            let at = production
                .find(needle)
                .unwrap_or_else(|| panic!("{needle} is missing from lib.rs"));
            let before = &production[..at];
            let gate = before
                .rfind("#[cfg(feature = \"shell-proof\")]")
                .unwrap_or_else(|| panic!("no gate before {needle}"));
            assert!(
                at - gate < 400,
                "{needle} is not directly under the shell-proof gate"
            );
        }
        let build = crate_file("build.rs");
        assert!(
            build.contains("#[cfg(feature = \"shell-proof\")]"),
            "build.rs gates the proof command"
        );
        assert!(
            build.contains("\"proof_report\""),
            "build.rs declares the proof command by name"
        );
        assert!(
            build.contains("./capabilities*/*.json") && build.contains("./capabilities/*.json"),
            "build.rs reads the proof capability folder only with the feature"
        );
    }

    #[test]
    fn a_default_build_carries_no_proof_command_grant_or_capability() {
        #[cfg(not(feature = "shell-proof"))]
        for built in [BUILT_ACL_MANIFEST, BUILT_CAPABILITIES] {
            assert!(
                !built.contains("proof"),
                "proof scaffolding is in the default build's ACL"
            );
        }
        assert!(
            !CAPABILITY.contains("proof"),
            "default.json grants no proof command"
        );
        assert!(
            !TAURI_CONF.contains("proof"),
            "tauri.conf.json names no proof mode"
        );
        let cap: serde_json::Value =
            serde_json::from_str(&crate_file("capabilities-proof/proof.json"))
                .expect("the proof capability parses");
        assert_eq!(
            cap["permissions"],
            serde_json::json!(["allow-proof-report"]),
            "the proof capability grants the report command and nothing else"
        );
        assert_eq!(cap["windows"], serde_json::json!(["main"]));
        assert!(cap.get("remote").is_none());
    }

    #[test]
    fn the_proof_script_drives_the_host_through_its_own_page_and_opens_no_remote_origin() {
        let script = crate_file("proof.js");
        assert!(!script.is_empty(), "proof.js exists");
        assert!(
            script.contains("proof_report"),
            "the script reports through the one proof command"
        );
        assert!(
            script.contains("HH_SHELL_PROOF") || !script.contains("println"),
            "the Rust side prints the line, not the script"
        );
        for banned in [
            "eval(",
            "new Function",
            "http://",
            "https://",
            "keychain",
            "auth_sign_in",
            "run_statement",
            "innerHTML",
            "WebSocket",
        ] {
            assert!(!script.contains(banned), "proof.js contains {banned}");
        }
        // it answers the demo mint locally, only for the two demo paths
        assert!(script.contains("/api/v1/demo/session") && script.contains("/api/v1/demo/flows"));
    }

    #[test]
    fn the_proof_mode_ends_the_run_by_itself_with_one_line() {
        let lib = include_str!("lib.rs");
        let production = lib.split("#[cfg(test)]").next().unwrap();
        assert!(
            production.contains("HH_SHELL_PROOF"),
            "one HH_SHELL_PROOF line is printed"
        );
        assert!(production.contains("app.exit("), "the run ends itself");
        assert!(
            production.contains("initialization_script_for_all_frames"),
            "the script reaches the door frame"
        );
        // the webview cannot enable the mode: there is no command that sets it,
        // and the report command only prints and exits
        assert!(
            !production.contains("fn enable_proof"),
            "no command turns the proof mode on"
        );
    }

    #[test]
    fn no_work_file_names_a_field_only_device_permission() {
        for (name, text) in [
            ("tauri.conf.json", TAURI_CONF),
            ("capabilities/default.json", CAPABILITY),
        ] {
            let lower = text.to_lowercase();
            for banned in ["camera", "geolocation", "location", "microphone"] {
                assert!(
                    !lower.contains(banned),
                    "{name} names {banned}, a Field only grant"
                );
            }
        }
        let p = profiles::work();
        assert!(!p.permissions.camera && !p.permissions.fine_location);
    }

    #[test]
    fn the_store_file_is_the_work_profiles_own_fixed_file() {
        assert_eq!(super::STORE_FILE, "work.sqlite3");
        assert!(!super::STORE_FILE.contains('/') && !super::STORE_FILE.contains(".."));
    }

    #[test]
    fn the_work_update_channel_is_configured_with_placeholders() {
        let conf: serde_json::Value =
            serde_json::from_str(TAURI_CONF).expect("tauri.conf.json parses");
        let updater = &conf["plugins"]["updater"];
        let endpoint = updater["endpoints"]
            .as_array()
            .and_then(|e| e.first())
            .and_then(|e| e.as_str())
            .expect("the updater has one configured endpoint");
        assert!(
            endpoint.contains("/hh/work/"),
            "the endpoint names the Work channel: {endpoint}"
        );
        assert!(!endpoint.contains("/hh/field/"));
        for part in ["{{target}}", "{{arch}}", "{{current_version}}"] {
            assert!(endpoint.contains(part), "the endpoint carries {part}");
        }
        let pubkey = updater["pubkey"]
            .as_str()
            .expect("the updater has a pubkey");
        assert_eq!(pubkey, "PLACEHOLDER: the release verify public key comes from FB Vault at the first signed release");
        assert_eq!(
            conf["bundle"]["createUpdaterArtifacts"],
            serde_json::json!(true)
        );
    }

    #[test]
    fn desktop_targets_are_configured_for_all_three_systems() {
        // Linux is built here; Windows and macOS are configured but not built
        // on this workstation. A bundle target list of "all" lets each system
        // build its own formats.
        let conf: serde_json::Value = serde_json::from_str(TAURI_CONF).unwrap();
        assert_eq!(conf["bundle"]["targets"], serde_json::json!("all"));
        assert!(conf["bundle"]["windows"]["webviewInstallMode"].is_object());
        assert!(conf["bundle"]["macOS"]["minimumSystemVersion"].is_string());
    }

    #[test]
    fn the_csp_is_strict_and_the_window_is_built_by_the_shell() {
        let conf: serde_json::Value = serde_json::from_str(TAURI_CONF).unwrap();
        let csp = conf["app"]["security"]["csp"]
            .as_str()
            .expect("a CSP is set");
        for directive in [
            "default-src 'self'",
            "script-src 'self'",
            "object-src 'none'",
            "frame-src 'self'",
            "frame-ancestors 'self'",
        ] {
            assert!(csp.contains(directive), "the CSP holds {directive}");
        }
        assert!(
            !csp.contains("frame-ancestors 'none'"),
            "the host frames its own desk apps, so 'none' is gone"
        );
        assert!(
            !csp.contains("child-src"),
            "frame-src alone carries the frames"
        );
        assert!(!csp.contains("unsafe-eval"), "no eval");
        assert!(
            !csp.contains("script-src 'self' 'unsafe-inline'"),
            "no inline script"
        );
        assert_eq!(conf["app"]["withGlobalTauri"], serde_json::json!(false));
        assert_eq!(conf["app"]["windows"], serde_json::json!([]));
    }

    #[test]
    fn print_profile_is_the_only_argument_that_ends_the_run_after_setup() {
        let args = |v: &[&str]| {
            v.iter()
                .map(|s| s.to_string())
                .collect::<Vec<_>>()
                .into_iter()
        };
        assert!(super::print_profile_only(args(&[
            "hh-work",
            "--print-profile"
        ])));
        assert!(!super::print_profile_only(args(&["hh-work"])));
        assert!(!super::print_profile_only(args(&[
            "hh-work",
            "--print-profile-and-more"
        ])));
        assert!(!super::print_profile_only(args(&[
            "hh-work",
            "print-profile"
        ])));
    }

    #[test]
    fn the_startup_line_names_the_work_profile_and_its_offline_blocks() {
        let mut plugins = std::collections::HashMap::new();
        let conf: serde_json::Value = serde_json::from_str(TAURI_CONF).unwrap();
        plugins.insert("updater".to_string(), conf["plugins"]["updater"].clone());
        let line = super::profile_line(&tauri::utils::config::PluginConfig(plugins));
        let v: serde_json::Value = serde_json::from_str(&line).expect("the line is JSON");
        assert_eq!(v["identifier"], "ai.futurebuild.hh.work");
        assert_eq!(v["product"], "HH Work");
        assert_eq!(v["channel"], "work");
        assert_eq!(
            v["permissions"],
            serde_json::json!({ "camera": false, "fine_location": false })
        );
        assert_eq!(v["store"]["file"], "work.sqlite3");
        assert!(v["updates"]["endpoint"]
            .as_str()
            .unwrap()
            .contains("/hh/work/"));
        let apps = v["apps"].as_array().unwrap();
        assert_eq!(apps.len(), 8);
        let money = apps.iter().find(|a| a["id"] == "money-desk").unwrap();
        assert_eq!(money["offline"]["mode"], "none");
        let till = apps.iter().find(|a| a["id"] == "counter-till").unwrap();
        assert_eq!(till["offline"]["mode"], "read_cache_only");
        for secret in ["session_token", "secret", "password", "private"] {
            assert!(
                !line.to_lowercase().contains(secret),
                "a secret-shaped field ({secret}) is in the line"
            );
        }
    }

    // ---- the four auth commands (mirrors Field's item 7 tests) ---------------

    const AUTH_COMMANDS: [&str; 4] = [
        "auth_sign_in",
        "auth_resume",
        "auth_exchange",
        "auth_sign_out",
    ];
    const AUTH_PERMISSIONS: [&str; 4] = [
        "allow-auth-sign-in",
        "allow-auth-resume",
        "allow-auth-exchange",
        "allow-auth-sign-out",
    ];
    const AUTH_SOURCE: &str = include_str!("auth.rs");

    #[test]
    fn the_auth_commands_are_allowed_by_name_to_the_bundled_local_origin_only() {
        let cap: serde_json::Value =
            serde_json::from_str(CAPABILITY).expect("the capability parses");
        let perms: Vec<&str> = cap["permissions"]
            .as_array()
            .unwrap()
            .iter()
            .map(|p| p.as_str().unwrap())
            .collect();
        for name in AUTH_PERMISSIONS {
            assert!(perms.contains(&name), "{name} is allowed by name");
        }
        assert!(
            perms.iter().all(|p| !p.contains('*')),
            "no wildcard permission"
        );
        assert!(
            cap.get("remote").is_none(),
            "the default capability names no remote URL"
        );
        assert!(cap
            .get("local")
            .is_none_or(|l| l == &serde_json::json!(true)));
        assert_eq!(
            cap["windows"],
            serde_json::json!(["main"]),
            "the main window only"
        );
        assert!(cap.get("webviews").is_none(), "no extra webview is granted");
        assert!(!TAURI_CONF.contains("dangerousRemoteDomainIpcAccess"));
        assert!(
            !CAPABILITY.contains("keychain"),
            "the auth grants add no keychain command"
        );
    }

    #[test]
    fn the_auth_commands_are_declared_in_the_built_acl_manifest() {
        for name in AUTH_COMMANDS {
            assert!(
                BUILT_ACL_MANIFEST.contains(name),
                "{name} is declared in the ACL manifest"
            );
        }
        for name in AUTH_PERMISSIONS {
            assert!(
                BUILT_CAPABILITIES.contains(name),
                "{name} is granted in the merged capabilities"
            );
        }
        assert!(!BUILT_ACL_MANIFEST.contains("keychain"));
    }

    #[test]
    fn no_auth_command_takes_an_endpoint_a_project_or_a_core_url() {
        let production = AUTH_SOURCE.split("#[cfg(test)]").next().unwrap();
        let mut commands = 0;
        for chunk in production.split("#[tauri::command]").skip(1) {
            commands += 1;
            let signature = chunk.split("{").next().unwrap().to_ascii_lowercase();
            for banned in [
                "endpoint", "project", "core_url", "url", "jwt", "secret", "token",
            ] {
                assert!(
                    !signature.contains(banned),
                    "a command signature mentions {banned}: {signature}"
                );
            }
        }
        assert_eq!(commands, 4, "exactly the four contract commands");
    }

    #[test]
    fn the_auth_module_prints_and_logs_nothing() {
        let production = AUTH_SOURCE.split("#[cfg(test)]").next().unwrap();
        for banned in [
            "println!",
            "eprintln!",
            "print!(",
            "dbg!",
            "log::",
            "tracing::",
            "info!(",
            "warn!(",
            "error!(",
        ] {
            assert!(!production.contains(banned), "auth.rs contains {banned}");
        }
    }

    #[test]
    fn the_keychain_service_is_the_work_identifier_and_the_slot_is_per_project() {
        // AUTH-SHELL-CONTRACT r7: service ai.futurebuild.hh.<profile>, account
        // console-session:<project id>. Work is desktop custody only.
        let address =
            hh_shell_core::custody::Address::console_session(profiles::work().identifier, "proj-1")
                .expect("the Work identifier forms a custody address");
        assert_eq!(address.service(), "ai.futurebuild.hh.work");
        assert_eq!(address.account(), "console-session:proj-1");
        assert!(
            !AUTH_SOURCE.contains("profiles::field"),
            "Work never reads Field's profile"
        );
    }

    #[test]
    fn a_build_without_its_sign_in_configuration_refuses_to_sign_in() {
        // No HH_CONSOLE_PROJECT_ID or HH_CORE_URL at build time: the managed
        // state is empty and every command answers `unavailable`.
        if profiles::work().auth.validate().is_err() {
            let state = super::auth::AuthState::for_profile();
            assert!(
                matches!(
                    state.service(),
                    Err(hh_shell_core::console::AuthCode::Unavailable)
                ),
                "an unconfigured build must hold no auth service and answer unavailable"
            );
        }
    }

    #[test]
    fn the_csp_connect_src_is_unchanged_by_sign_in() {
        // Sign in runs on the Rust side (Console and the core are called from
        // Rust), so the webview's connect-src needs no network origin, exactly
        // as in Field's config.
        let conf: serde_json::Value = serde_json::from_str(TAURI_CONF).unwrap();
        let csp = conf["app"]["security"]["csp"].as_str().unwrap();
        let directive = |name: &str| -> Vec<&str> {
            csp.split(';')
                .map(str::trim)
                .find(|d| d.starts_with(name))
                .unwrap_or_else(|| panic!("missing {name} directive, {csp}"))
                .split_whitespace()
                .skip(1)
                .collect()
        };
        assert_eq!(
            directive("connect-src"),
            vec!["'self'", "ipc:", "http://ipc.localhost", "https://*.futurebuild.ai"],
            "{csp}"
        );
        assert_eq!(directive("default-src"), vec!["'self'"], "{csp}");
        assert_eq!(directive("script-src"), vec!["'self'"], "{csp}");
        assert_eq!(directive("style-src"), vec!["'self'", "'unsafe-inline'"], "{csp}");
        assert_eq!(directive("img-src"), vec!["'self'", "data:"], "{csp}");
        assert_eq!(directive("font-src"), vec!["'self'"], "{csp}");
        assert_eq!(directive("base-uri"), vec!["'self'"], "{csp}");
        assert_eq!(directive("form-action"), vec!["'self'"], "{csp}");
        assert_eq!(directive("frame-src"), vec!["'self'"], "{csp}");
        assert_eq!(directive("frame-ancestors"), vec!["'self'"], "{csp}");
        assert_eq!(directive("object-src"), vec!["'none'"], "{csp}");
    }
}
