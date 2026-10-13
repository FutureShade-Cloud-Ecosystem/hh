use tauri_build::{AppManifest, Attributes};

// Every command the webview may call is declared here, so Tauri applies an
// ACL to it and the capability must allow it by name. An undeclared app
// command would be open to every window. Work declares the offline store's
// two commands and nothing else: no keychain command (the webview never
// reaches the custody slot; the auth commands are wired after the sign in
// item merges), no probe scaffolding.
const COMMANDS: &[&str] = &["run_statement", "run_atomic"];

fn main() {
    println!("cargo:rerun-if-changed=capabilities");
    tauri_build::try_build(
        Attributes::new()
            .app_manifest(AppManifest::new().commands(COMMANDS))
            .capabilities_path_pattern("./capabilities/*.json"),
    )
    .expect("failed to run tauri-build");
}
