//! PROBE SCAFFOLDING: the probe 4 keychain slot, compiled only with the
//! `probe` feature and removed with the probe. The real custody of the FB
//! Console session secret is `custody.rs` (service and account per the
//! contract, Rust side only); a normal build carries none of this.

/// The probe's one slot. The probe harness names a slot, never a free string.
#[derive(Clone, Copy, Debug, PartialEq, Eq, serde::Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Slot {
    Probe,
}

impl Slot {
    pub fn name(self) -> &'static str {
        match self {
            Slot::Probe => "probe",
        }
    }
}

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

#[cfg(target_os = "linux")]
pub fn delete(service: &str, name: &str) -> Result<(), String> {
    entry(service, name)?.delete_credential().map_err(|e| e.to_string())
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
    fn the_probe_names_one_slot_and_nothing_else() {
        assert_eq!(serde_json::from_str::<Slot>("\"probe\"").unwrap(), Slot::Probe);
        assert!(serde_json::from_str::<Slot>("\"session_token\"").is_err());
        assert!(serde_json::from_str::<Slot>("\"anything_else\"").is_err());
    }
}
