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
// tokens or keys). Config values that land with their own items (the store
// file, the update endpoint) are appended by those items.
fn profile_line() -> String {
    let p = profiles::field();
    serde_json::json!({
        "identifier": p.identifier,
        "product": p.product_name,
        "brand": p.brand,
        "channel": p.channel.as_str(),
        "window": { "title": p.window.title, "width": p.window.width, "height": p.window.height },
        "permissions": { "camera": p.permissions.camera, "fine_location": p.permissions.fine_location },
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
    let builder = tauri::Builder::default();
    #[cfg(feature = "probe")]
    let builder = builder
        .plugin(tauri_plugin_sql::Builder::default().build())
        .invoke_handler(tauri::generate_handler![keychain_set, keychain_get, keychain_delete, probe_report]);
    #[cfg(not(feature = "probe"))]
    let builder = builder.invoke_handler(tauri::generate_handler![keychain_set, keychain_get]);
    builder
        .setup(|app| {
            let profile = profiles::field();
            println!("HH_PROFILE {}", profile_line());
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
