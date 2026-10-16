//! Custody of the FB Console session secret: one value under a service and an
//! account, behind a trait so the sign in code never names a platform.
//!
//! AUTH-SHELL-CONTRACT r6, "Keychain": service `ai.futurebuild.hh.<profile>`,
//! account `console-session:<project id>`, value the session secret and
//! nothing else, one entry per profile and project, sign out deletes it. The
//! secret is used only on the Rust side; the webview holds no command that
//! reaches it.
//!
//! **Fail closed.** If the platform keychain cannot be reached, every
//! operation answers `KeychainUnavailable`. There is no fallback to a file,
//! to web storage or to memory, and the error carries a code only: no
//! platform message, no service, no account, no value.

use std::{collections::HashMap, sync::Mutex};

/// The one way custody fails. A single code on purpose: a caller cannot
/// tell "no daemon" from "locked" from "denied", and none of them may be
/// worked around.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum CustodyError {
    KeychainUnavailable,
}

impl CustodyError {
    /// The contract's error word.
    pub fn code(self) -> &'static str {
        match self {
            CustodyError::KeychainUnavailable => "keychain_unavailable",
        }
    }
}

const SERVICE_PREFIX: &str = "ai.futurebuild.hh.";
const ACCOUNT_PREFIX: &str = "console-session:";

/// Where one secret lives. Built only by `console_session`, from the profile's
/// identifier and the configured project id, so no caller invents an address
/// and the webview never supplies one.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct Address {
    service: String,
    account: String,
}

/// An address that does not match the contract's shape. It carries nothing.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct BadAddress;

impl Address {
    /// `profile_identifier` is the profile model's identifier
    /// (`ai.futurebuild.hh.field`); `project_id` is the FB Console project id
    /// from the profile's auth configuration (1 to 36 of letters, digits,
    /// underscore and hyphen, the id shape Console accepts).
    pub fn console_session(profile_identifier: &str, project_id: &str) -> Result<Self, BadAddress> {
        let profile_ok = profile_identifier
            .strip_prefix(SERVICE_PREFIX)
            .is_some_and(|p| {
                !p.is_empty()
                    && p.bytes()
                        .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit())
            });
        let project_ok = (1..=36).contains(&project_id.len())
            && project_id
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-');
        if !profile_ok || !project_ok {
            return Err(BadAddress);
        }
        Ok(Self {
            service: profile_identifier.to_string(),
            account: format!("{ACCOUNT_PREFIX}{project_id}"),
        })
    }

    pub fn service(&self) -> &str {
        &self.service
    }

    pub fn account(&self) -> &str {
        &self.account
    }
}

/// Get, set and delete one secret. `get` of an absent entry is `Ok(None)` and
/// `delete` of an absent entry is `Ok(())`: only an unreachable keychain is
/// an error.
pub trait Custody: Send + Sync {
    fn get(&self, address: &Address) -> Result<Option<String>, CustodyError>;
    fn set(&self, address: &Address, value: &str) -> Result<(), CustodyError>;
    fn delete(&self, address: &Address) -> Result<(), CustodyError>;
}

/// The desktop Linux implementation: the freedesktop Secret Service through
/// the keyring crate's pure Rust path (zbus, no C headers).
#[cfg(target_os = "linux")]
pub struct KeyringCustody;

#[cfg(target_os = "linux")]
impl KeyringCustody {
    fn entry(address: &Address) -> Result<keyring::Entry, CustodyError> {
        keyring::Entry::new(address.service(), address.account())
            .map_err(|_| CustodyError::KeychainUnavailable)
    }
}

#[cfg(target_os = "linux")]
impl Custody for KeyringCustody {
    fn get(&self, address: &Address) -> Result<Option<String>, CustodyError> {
        match Self::entry(address)?.get_password() {
            Ok(v) => Ok(Some(v)),
            Err(keyring::Error::NoEntry) => Ok(None),
            Err(_) => Err(CustodyError::KeychainUnavailable),
        }
    }

    fn set(&self, address: &Address, value: &str) -> Result<(), CustodyError> {
        Self::entry(address)?
            .set_password(value)
            .map_err(|_| CustodyError::KeychainUnavailable)
    }

    fn delete(&self, address: &Address) -> Result<(), CustodyError> {
        match Self::entry(address)?.delete_credential() {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(_) => Err(CustodyError::KeychainUnavailable),
        }
    }
}

/// The failure of a platform bridge. It carries nothing: a Java exception
/// message or a JNI error code could name a service or an account, and the
/// caller needs only "it failed".
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct BridgeFailure;

/// The few calls a platform keystore must answer, on plain strings: the
/// Android Keystore bridge implements this over JNI, and the host tests
/// implement it with a fake. `get` of an absent entry is `Ok(None)`; `delete`
/// of an absent entry is `Ok(())`; anything else wrong is `BridgeFailure`.
pub trait KeystoreBridge: Send + Sync {
    fn put(&self, service: &str, account: &str, value: &str) -> Result<(), BridgeFailure>;
    fn get(&self, service: &str, account: &str) -> Result<Option<String>, BridgeFailure>;
    fn delete(&self, service: &str, account: &str) -> Result<(), BridgeFailure>;
}

/// A custody over a keystore bridge. It adds the address discipline (only the
/// contract's service and account reach the bridge) and the fail closed
/// mapping; there is no other storage behind it.
pub struct BridgedCustody<B: KeystoreBridge>(B);

impl<B: KeystoreBridge> BridgedCustody<B> {
    pub fn new(bridge: B) -> Self {
        Self(bridge)
    }

    #[cfg(test)]
    fn bridge(&self) -> &B {
        &self.0
    }
}

impl<B: KeystoreBridge> Custody for BridgedCustody<B> {
    fn get(&self, address: &Address) -> Result<Option<String>, CustodyError> {
        self.0
            .get(address.service(), address.account())
            .map_err(|_| CustodyError::KeychainUnavailable)
    }

    fn set(&self, address: &Address, value: &str) -> Result<(), CustodyError> {
        if value.is_empty() {
            return Err(CustodyError::KeychainUnavailable);
        }
        self.0
            .put(address.service(), address.account(), value)
            .map_err(|_| CustodyError::KeychainUnavailable)
    }

    fn delete(&self, address: &Address) -> Result<(), CustodyError> {
        self.0
            .delete(address.service(), address.account())
            .map_err(|_| CustodyError::KeychainUnavailable)
    }
}

/// What a self check run did.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum SelfCheck {
    /// The entry was absent: it was stored and read back, and left in place.
    Stored,
    /// The entry from an earlier run was found with the right value and removed.
    PersistedAndCleared,
}

const SELF_CHECK_VALUE: &str = "hh-custody-self-check-value";

/// The debug leg's proof that a custody really keeps a value: the first run
/// stores a fixed, non secret value and reads it back and leaves it; the next
/// run (after a restart) finds it, verifies it and deletes it. Run against an
/// address no real session uses. Errors name the step only.
pub fn self_check(custody: &dyn Custody, address: &Address) -> Result<SelfCheck, &'static str> {
    match custody.get(address) {
        Err(_) => Err("get"),
        Ok(Some(found)) => {
            let right = found == SELF_CHECK_VALUE;
            custody.delete(address).map_err(|_| "delete")?;
            if !right {
                return Err("value");
            }
            match custody.get(address) {
                Ok(None) => Ok(SelfCheck::PersistedAndCleared),
                Ok(Some(_)) => Err("delete"),
                Err(_) => Err("get"),
            }
        }
        Ok(None) => {
            custody.set(address, SELF_CHECK_VALUE).map_err(|_| "set")?;
            match custody.get(address) {
                Ok(Some(v)) if v == SELF_CHECK_VALUE => Ok(SelfCheck::Stored),
                Ok(_) => Err("value"),
                Err(_) => Err("get"),
            }
        }
    }
}

/// A target with no keychain backend yet: every operation fails closed.
pub struct NoBackend;

impl Custody for NoBackend {
    fn get(&self, _: &Address) -> Result<Option<String>, CustodyError> {
        Err(CustodyError::KeychainUnavailable)
    }
    fn set(&self, _: &Address, _: &str) -> Result<(), CustodyError> {
        Err(CustodyError::KeychainUnavailable)
    }
    fn delete(&self, _: &Address) -> Result<(), CustodyError> {
        Err(CustodyError::KeychainUnavailable)
    }
}

/// The custody this build's target uses: the Secret Service on desktop Linux,
/// the Android Keystore on Android, and nothing (fail closed) elsewhere.
pub fn platform_custody() -> Box<dyn Custody> {
    #[cfg(target_os = "android")]
    {
        Box::new(BridgedCustody::new(crate::android_keystore::JniKeystore))
    }
    #[cfg(target_os = "linux")]
    {
        Box::new(KeyringCustody)
    }
    #[cfg(not(any(target_os = "linux", target_os = "android")))]
    {
        Box::new(NoBackend)
    }
}

/// An in memory fake for tests (and nothing else: a shipping build never
/// constructs one). `unavailable()` is the fail closed case.
pub struct MemoryCustody {
    entries: Mutex<HashMap<Address, String>>,
    available: bool,
    writes_fail: bool,
}

impl MemoryCustody {
    pub fn new() -> Self {
        Self {
            entries: Mutex::new(HashMap::new()),
            available: true,
            writes_fail: false,
        }
    }

    pub fn unavailable() -> Self {
        Self {
            entries: Mutex::new(HashMap::new()),
            available: false,
            writes_fail: false,
        }
    }

    /// Reads and deletes work, `set` fails closed (a keychain that locked
    /// between the read and the write).
    pub fn writes_fail() -> Self {
        Self {
            entries: Mutex::new(HashMap::new()),
            available: true,
            writes_fail: true,
        }
    }

    /// How many entries are held (tests assert a failed write stored nothing).
    pub fn snapshot_len(&self) -> usize {
        self.entries.lock().expect("custody fake lock").len()
    }

    fn check(&self) -> Result<(), CustodyError> {
        if self.available {
            Ok(())
        } else {
            Err(CustodyError::KeychainUnavailable)
        }
    }
}

impl Default for MemoryCustody {
    fn default() -> Self {
        Self::new()
    }
}

impl Custody for MemoryCustody {
    fn get(&self, address: &Address) -> Result<Option<String>, CustodyError> {
        self.check()?;
        Ok(self
            .entries
            .lock()
            .expect("custody fake lock")
            .get(address)
            .cloned())
    }

    fn set(&self, address: &Address, value: &str) -> Result<(), CustodyError> {
        self.check()?;
        if self.writes_fail {
            return Err(CustodyError::KeychainUnavailable);
        }
        self.entries
            .lock()
            .expect("custody fake lock")
            .insert(address.clone(), value.to_string());
        Ok(())
    }

    fn delete(&self, address: &Address) -> Result<(), CustodyError> {
        self.check()?;
        self.entries
            .lock()
            .expect("custody fake lock")
            .remove(address);
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn addr() -> Address {
        Address::console_session("ai.futurebuild.hh.field", "proj-1").expect("a valid address")
    }

    #[test]
    fn the_address_is_the_contracts_service_and_account() {
        let a = addr();
        assert_eq!(a.service(), "ai.futurebuild.hh.field");
        assert_eq!(a.account(), "console-session:proj-1");
    }

    #[test]
    fn an_address_refuses_a_foreign_service_and_a_malformed_project_id() {
        assert!(Address::console_session("com.example.other", "proj-1").is_err());
        assert!(Address::console_session("ai.futurebuild.hh.", "proj-1").is_err());
        for bad in ["", "a b", "a:b", "a/b", "a\0b", &"x".repeat(37)] {
            assert!(
                Address::console_session("ai.futurebuild.hh.field", bad).is_err(),
                "{bad:?}"
            );
        }
    }

    #[test]
    fn set_then_get_round_trips_and_overwrites() {
        let c = MemoryCustody::new();
        assert_eq!(c.get(&addr()), Ok(None));
        c.set(&addr(), "one").unwrap();
        assert_eq!(c.get(&addr()), Ok(Some("one".to_string())));
        c.set(&addr(), "two").unwrap();
        assert_eq!(c.get(&addr()), Ok(Some("two".to_string())));
    }

    #[test]
    fn delete_removes_the_entry_and_an_absent_entry_is_not_an_error() {
        let c = MemoryCustody::new();
        c.delete(&addr()).unwrap();
        c.set(&addr(), "one").unwrap();
        c.delete(&addr()).unwrap();
        assert_eq!(c.get(&addr()), Ok(None));
    }

    #[test]
    fn entries_are_per_profile_and_per_project() {
        let c = MemoryCustody::new();
        let work = Address::console_session("ai.futurebuild.hh.work", "proj-1").unwrap();
        let other = Address::console_session("ai.futurebuild.hh.field", "proj-2").unwrap();
        c.set(&addr(), "field").unwrap();
        assert_eq!(c.get(&work), Ok(None));
        assert_eq!(c.get(&other), Ok(None));
    }

    #[test]
    fn an_unavailable_keychain_fails_closed_on_every_operation() {
        let c = MemoryCustody::unavailable();
        assert_eq!(c.get(&addr()), Err(CustodyError::KeychainUnavailable));
        assert_eq!(c.set(&addr(), "x"), Err(CustodyError::KeychainUnavailable));
        assert_eq!(c.delete(&addr()), Err(CustodyError::KeychainUnavailable));
        // A failed write stores nothing behind the caller's back.
        assert_eq!(c.snapshot_len(), 0);
    }

    // ---- the platform bridge seam (Android Keystore) -----------------------

    use std::sync::Mutex;

    /// Records what the bridge was asked and answers from a map; `fail` makes
    /// every call fail the way a broken JNI call or a Keystore error would.
    #[derive(Default)]
    struct FakeBridge {
        calls: Mutex<Vec<String>>,
        store: Mutex<std::collections::HashMap<(String, String), String>>,
        fail: bool,
    }

    impl KeystoreBridge for FakeBridge {
        fn put(&self, service: &str, account: &str, value: &str) -> Result<(), BridgeFailure> {
            self.calls
                .lock()
                .unwrap()
                .push(format!("put {service} {account}"));
            if self.fail {
                return Err(BridgeFailure);
            }
            self.store
                .lock()
                .unwrap()
                .insert((service.into(), account.into()), value.into());
            Ok(())
        }
        fn get(&self, service: &str, account: &str) -> Result<Option<String>, BridgeFailure> {
            self.calls
                .lock()
                .unwrap()
                .push(format!("get {service} {account}"));
            if self.fail {
                return Err(BridgeFailure);
            }
            Ok(self
                .store
                .lock()
                .unwrap()
                .get(&(service.into(), account.into()))
                .cloned())
        }
        fn delete(&self, service: &str, account: &str) -> Result<(), BridgeFailure> {
            self.calls
                .lock()
                .unwrap()
                .push(format!("delete {service} {account}"));
            if self.fail {
                return Err(BridgeFailure);
            }
            self.store
                .lock()
                .unwrap()
                .remove(&(service.into(), account.into()));
            Ok(())
        }
    }

    #[test]
    fn bridged_custody_passes_the_contracts_service_and_account_and_nothing_else() {
        let c = BridgedCustody::new(FakeBridge::default());
        c.set(&addr(), "v1").unwrap();
        assert_eq!(c.get(&addr()), Ok(Some("v1".to_string())));
        c.delete(&addr()).unwrap();
        assert_eq!(c.get(&addr()), Ok(None));
        let calls = c.bridge().calls.lock().unwrap().clone();
        assert_eq!(
            calls,
            [
                "put ai.futurebuild.hh.field console-session:proj-1",
                "get ai.futurebuild.hh.field console-session:proj-1",
                "delete ai.futurebuild.hh.field console-session:proj-1",
                "get ai.futurebuild.hh.field console-session:proj-1",
            ]
        );
    }

    #[test]
    fn a_failing_bridge_is_keychain_unavailable_on_every_operation_with_no_fallback() {
        let c = BridgedCustody::new(FakeBridge {
            fail: true,
            ..Default::default()
        });
        assert_eq!(c.get(&addr()), Err(CustodyError::KeychainUnavailable));
        assert_eq!(c.set(&addr(), "v1"), Err(CustodyError::KeychainUnavailable));
        assert_eq!(c.delete(&addr()), Err(CustodyError::KeychainUnavailable));
        // Nothing was kept anywhere else.
        assert!(c.bridge().store.lock().unwrap().is_empty());
    }

    #[test]
    fn the_bridged_custody_refuses_an_empty_secret() {
        // An empty value is never a session secret; it must not be stored as one.
        let c = BridgedCustody::new(FakeBridge::default());
        assert_eq!(c.set(&addr(), ""), Err(CustodyError::KeychainUnavailable));
        assert!(
            c.bridge().calls.lock().unwrap().is_empty(),
            "it never reached the Keystore"
        );
    }

    // ---- the self check the Android debug leg runs --------------------------

    #[test]
    fn the_self_check_stores_then_proves_persistence_then_cleans_up() {
        let c = MemoryCustody::new();
        let a = Address::console_session("ai.futurebuild.hh.field", "custody-proof").unwrap();
        // First run: nothing there, so it stores the value and reads it back.
        assert_eq!(self_check(&c, &a), Ok(SelfCheck::Stored));
        assert_eq!(
            c.snapshot_len(),
            1,
            "the entry stays so the next launch can prove persistence"
        );
        // Second run (a restart): it finds the value, verifies it and deletes it.
        assert_eq!(self_check(&c, &a), Ok(SelfCheck::PersistedAndCleared));
        assert_eq!(c.snapshot_len(), 0);
        assert_eq!(self_check(&c, &a), Ok(SelfCheck::Stored));
    }

    #[test]
    fn the_self_check_reports_a_wrong_value_and_an_unavailable_keychain_by_step() {
        let c = MemoryCustody::unavailable();
        let a = Address::console_session("ai.futurebuild.hh.field", "custody-proof").unwrap();
        assert_eq!(self_check(&c, &a), Err("get"));
        let c = MemoryCustody::writes_fail();
        assert_eq!(self_check(&c, &a), Err("set"));
        let c = MemoryCustody::new();
        c.set(&a, "some other value").unwrap();
        assert_eq!(self_check(&c, &a), Err("value"));
        assert_eq!(
            c.snapshot_len(),
            0,
            "a wrong value is removed so the next run starts clean"
        );
    }

    /// The real Secret Service on a desktop Linux session (not run by default:
    /// CI has no keyring daemon). Run with `cargo test -p hh-shell-core --lib
    /// -- --ignored real_secret_service`. It uses the self check's proof
    /// address (a fixed non secret value, removed at the end).
    #[cfg(target_os = "linux")]
    #[test]
    #[ignore = "needs a running Secret Service"]
    fn real_secret_service_round_trips_and_deletes() {
        let c = KeyringCustody;
        let a = Address::console_session("ai.futurebuild.hh.field", "custody-proof").unwrap();
        c.delete(&a).unwrap();
        assert_eq!(self_check(&c, &a), Ok(SelfCheck::Stored));
        assert_eq!(c.get(&a), Ok(Some(SELF_CHECK_VALUE.to_string())));
        assert_eq!(self_check(&c, &a), Ok(SelfCheck::PersistedAndCleared));
        assert_eq!(c.get(&a), Ok(None));
        c.delete(&a).unwrap();
    }

    #[test]
    fn the_error_carries_a_code_and_no_input() {
        assert_eq!(
            CustodyError::KeychainUnavailable.code(),
            "keychain_unavailable"
        );
        assert_eq!(
            format!("{:?}", CustodyError::KeychainUnavailable),
            "KeychainUnavailable"
        );
    }
}
