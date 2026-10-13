//! Keychain custody: one value under a service and a fixed slot.
//!
//! The seam the auth cycle signs in against (AUTH-SHELL-CONTRACT r5, the
//! shells brief's item 7): the FB Console session secret lives in the OS
//! keychain and is used only on the Rust side; the webview holds the core's
//! short token in memory, nothing else, never in storage, never in a log.
//! What Console returns stays in Rust.
//!
//! The contract's address shape, kept here so no caller invents one:
//!
//! - service: `ai.futurebuild.hh.<profile>`, which is exactly the profile's
//!   identifier (the shell uses `profiles::field().identifier` for Field);
//! - slot: one of the fixed slots below (today only `session_token`; sign
//!   out deletes it);
//! - account: `console-session:<project id>`, one entry per profile and
//!   project. The account dimension arrives with the sign in item (item 7 of
//!   the shells brief), which is also the only writer; the seam functions
//!   take it then. No sign in code lives here.
//!
//! Linux desktop goes through the freedesktop Secret Service (pure Rust,
//! no C headers). Other targets return an error that names the gap, so the
//! caller never mistakes a missing backend for a stored value.

/// The fixed slots a shell may read or write. The webview names a slot, never
/// a free string, so it cannot enumerate or overwrite other items.
#[derive(Clone, Copy, Debug, PartialEq, Eq, serde::Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Slot {
    /// The core's session token slot: the one real slot of the contract's
    /// custody. The webview's own token stays in memory per the contract and
    /// never passes through the keychain.
    SessionToken,
    // Probe scaffolding: the probe 4 harness writes here. Absent from a normal build.
    #[cfg(feature = "probe")]
    Probe,
}

impl Slot {
    pub fn name(self) -> &'static str {
        match self {
            Slot::SessionToken => "session_token",
            #[cfg(feature = "probe")]
            Slot::Probe => "probe",
        }
    }

    /// Every slot the running build holds. A default build carries exactly
    /// the real slots (the tests pin that); the probe build adds its own.
    #[cfg(feature = "probe")]
    pub const ALL: &'static [Slot] = &[Slot::SessionToken, Slot::Probe];
    #[cfg(not(feature = "probe"))]
    pub const ALL: &'static [Slot] = &[Slot::SessionToken];
}

#[cfg(target_os = "linux")]
fn entry(service: &str, name: &str) -> Result<keyring::Entry, String> {
    keyring::Entry::new(service, name).map_err(|e| e.to_string())
}

#[cfg(target_os = "linux")]
pub fn set(service: &str, name: &str, value: &str) -> Result<(), String> {
    entry(service, name)?
        .set_password(value)
        .map_err(|e| e.to_string())
}

#[cfg(target_os = "linux")]
pub fn get(service: &str, name: &str) -> Result<String, String> {
    entry(service, name)?
        .get_password()
        .map_err(|e| e.to_string())
}

#[cfg(target_os = "linux")]
pub fn delete(service: &str, name: &str) -> Result<(), String> {
    entry(service, name)?
        .delete_credential()
        .map_err(|e| e.to_string())
}

#[cfg(not(target_os = "linux"))]
pub fn set(_service: &str, _name: &str, _value: &str) -> Result<(), String> {
    Err("no keychain backend on this target yet".into())
}

#[cfg(not(target_os = "linux"))]
pub fn get(_service: &str, _name: &str) -> Result<String, String> {
    Err("no keychain backend on this target yet".into())
}

#[cfg(not(target_os = "linux"))]
pub fn delete(_service: &str, _name: &str) -> Result<(), String> {
    Err("no keychain backend on this target yet".into())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_real_slot_set_is_the_session_token_only() {
        let names: Vec<&str> = Slot::ALL.iter().map(|s| s.name()).collect();
        #[cfg(feature = "probe")]
        let expected = ["session_token", "probe"];
        #[cfg(not(feature = "probe"))]
        let expected = ["session_token"];
        assert_eq!(names, expected);
    }

    #[test]
    fn the_webview_names_a_slot_and_nothing_else() {
        // The contract's slot word, and a closed set: an unknown name is a
        // deserialize error at the command boundary, never a free string.
        assert_eq!(
            serde_json::from_str::<Slot>("\"session_token\"").unwrap(),
            Slot::SessionToken
        );
        assert!(serde_json::from_str::<Slot>("\"anything_else\"").is_err());
    }
}
