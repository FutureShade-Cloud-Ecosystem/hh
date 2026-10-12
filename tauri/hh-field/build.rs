fn main() {
    // The probe capability (open SQLite grant) sits in its own folder and is
    // read only when the probe feature is on, so a normal build never sees it.
    #[cfg(feature = "probe")]
    let pattern = "./capabilities*/*.json";
    #[cfg(not(feature = "probe"))]
    let pattern = "./capabilities/*.json";
    println!("cargo:rerun-if-changed=capabilities");
    println!("cargo:rerun-if-changed=capabilities-probe");
    tauri_build::try_build(tauri_build::Attributes::new().capabilities_path_pattern(pattern))
        .expect("failed to run tauri-build");
}
