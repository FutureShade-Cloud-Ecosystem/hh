use tauri::{WebviewUrl, WebviewWindowBuilder};

use hh_shell_core::keychain::{self, Slot};
use hh_shell_core::profiles;

const KEYCHAIN_SERVICE: &str = "ai.futurebuild.hh.field";

// The keyring call blocks (a Secret Service unlock prompt can take a while), so
// it runs on a blocking thread and never on the main thread or an async worker.
async fn blocking<T: Send + 'static>(f: impl FnOnce() -> Result<T, String> + Send + 'static) -> Result<T, String> {
    tauri::async_runtime::spawn_blocking(f).await.map_err(|e| e.to_string())?
}

#[tauri::command]
async fn keychain_set(slot: Slot, value: String) -> Result<(), String> {
    blocking(move || keychain::set(KEYCHAIN_SERVICE, slot.name(), &value)).await
}

#[tauri::command]
async fn keychain_get(slot: Slot) -> Result<String, String> {
    blocking(move || keychain::get(KEYCHAIN_SERVICE, slot.name())).await
}

// PROBE SCAFFOLDING: lets the probe delete its own keychain item. Not part of
// a normal build; the real shell decides its own sign out path.
#[cfg(feature = "probe")]
#[tauri::command]
async fn keychain_delete(slot: Slot) -> Result<(), String> {
    blocking(move || keychain::delete(KEYCHAIN_SERVICE, slot.name())).await
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
        .invoke_handler(tauri::generate_handler![keychain_set, keychain_get, keychain_delete, probe_report]);
    #[cfg(not(feature = "probe"))]
    let builder = builder.invoke_handler(tauri::generate_handler![keychain_set, keychain_get]);
    builder
        .setup(|app| {
            let profile = profiles::field();
            println!("HH_PROFILE {}", profile_line(&app.config().plugins));
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
}
