use tauri_build::{AppManifest, Attributes};

// Every command the webview may call is declared here, so Tauri applies an
// ACL to it and the capability must allow it by name. An undeclared app
// command would be open to every window.
#[cfg(feature = "probe")]
const COMMANDS: &[&str] = &["keychain_set", "keychain_get", "probe_report"];
#[cfg(not(feature = "probe"))]
const COMMANDS: &[&str] = &["keychain_set", "keychain_get"];

fn main() {
    // The probe capability (open SQLite grant) sits in its own folder and is
    // read only when the probe feature is on, so a normal build never sees it.
    #[cfg(feature = "probe")]
    let pattern = "./capabilities*/*.json";
    #[cfg(not(feature = "probe"))]
    let pattern = "./capabilities/*.json";
    println!("cargo:rerun-if-changed=capabilities");
    println!("cargo:rerun-if-changed=capabilities-probe");
    tauri_build::try_build(
        Attributes::new()
            .app_manifest(AppManifest::new().commands(COMMANDS))
            .capabilities_path_pattern(pattern),
    )
    .expect("failed to run tauri-build");
}
