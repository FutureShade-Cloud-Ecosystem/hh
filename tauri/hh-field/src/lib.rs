use tauri::{WebviewUrl, WebviewWindowBuilder};

const KEYCHAIN_SERVICE: &str = "ai.futurebuild.hh.field.probe4";

#[tauri::command]
fn keychain_set(name: String, value: String) -> Result<(), String> {
    hh_shell_core::keychain::set(KEYCHAIN_SERVICE, &name, &value)
}

#[tauri::command]
fn keychain_get(name: String) -> Result<String, String> {
    hh_shell_core::keychain::get(KEYCHAIN_SERVICE, &name)
}

#[tauri::command]
fn probe_report(app: tauri::AppHandle, json: String) {
    println!("HH_PROBE4 {json}");
    // A headless run sets HH_PROBE4_EXIT inline so the process ends by itself.
    if std::env::var_os("HH_PROBE4_EXIT").is_some() {
        app.exit(0);
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_sql::Builder::default().build())
        .invoke_handler(tauri::generate_handler![keychain_set, keychain_get, probe_report])
        .setup(|app| {
            WebviewWindowBuilder::new(app, "main", WebviewUrl::default())
                .title("HH Field")
                .initialization_script(include_str!("../probe.js"))
                .build()?;
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running HH Field");
}
