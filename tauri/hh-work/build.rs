use tauri_build::{AppManifest, Attributes};

// Every command the webview may call is declared here, so Tauri applies an
// ACL to it and the capability must allow it by name. An undeclared app
// command would be open to every window. Work declares the offline store's
// two commands and the four sign in commands (the webview never reaches the
// custody slot itself): no keychain command, no probe scaffolding.
const COMMANDS: &[&str] = &[
    "run_statement",
    "run_atomic",
    "auth_sign_in",
    "auth_resume",
    "auth_exchange",
    "auth_sign_out",
];

fn main() {
    println!("cargo:rerun-if-changed=capabilities");
    tauri_build::try_build(
        Attributes::new()
            .app_manifest(AppManifest::new().commands(COMMANDS))
            .capabilities_path_pattern("./capabilities/*.json"),
    )
    .expect("failed to run tauri-build");
}
