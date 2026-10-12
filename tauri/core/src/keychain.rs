//! Keychain custody: one value under a service and a name.
//!
//! Linux desktop goes through the freedesktop Secret Service (pure Rust,
//! no C headers). Other targets return an error that names the gap, so the
//! caller never mistakes a missing backend for a stored value.

#[cfg(target_os = "linux")]
fn entry(service: &str, name: &str) -> Result<keyring::Entry, String> {
    keyring::Entry::new(service, name).map_err(|e| e.to_string())
}

#[cfg(target_os = "linux")]
pub fn set(service: &str, name: &str, value: &str) -> Result<(), String> {
    entry(service, name)?.set_password(value).map_err(|e| e.to_string())
}

#[cfg(target_os = "linux")]
pub fn get(service: &str, name: &str) -> Result<String, String> {
    entry(service, name)?.get_password().map_err(|e| e.to_string())
}

#[cfg(not(target_os = "linux"))]
pub fn set(_service: &str, _name: &str, _value: &str) -> Result<(), String> {
    Err("no keychain backend on this target yet".into())
}

#[cfg(not(target_os = "linux"))]
pub fn get(_service: &str, _name: &str) -> Result<String, String> {
    Err("no keychain backend on this target yet".into())
}
