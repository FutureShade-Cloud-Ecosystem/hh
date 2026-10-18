//! The profile model of the HH shells (plan B.3, decision row 1519): one
//! Tauri 2 workspace, three thin profiles that differ in identifier, the
//! micro app set, permissions, brand, the offline scope sets, window defaults
//! and the update channel.
//!
//! The Field and Work profiles are complete. Pro is a declared skeleton:
//! identifier, product name, apps and channel, nothing else, until its own
//! item lands (its crate is still a README).
//!
//! The micro apps and their offline scope sets are never typed by hand twice:
//! `scripts/generate-field-profiles.ts` derives them from manifest.yaml into
//! the checked in `profiles/{field,work,pro}.json` this module embeds. The
//! drift is checked from both sides: `bun scripts/generate-field-profiles.ts
//! --check` and `bun test scripts/field-profiles.test.ts` on the manifest
//! side, and the tests here (the Field identifier is compared against the
//! Tauri config's own identifier).

use std::{collections::BTreeMap, sync::OnceLock};

/// Which release channel a profile updates from. The updater's endpoint is
/// built from this (see hh-field's README); the channel is a profile fact,
/// never a webview choice.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum UpdateChannel {
    Field,
    Work,
    Pro,
}

impl UpdateChannel {
    pub fn as_str(self) -> &'static str {
        match self {
            UpdateChannel::Field => "field",
            UpdateChannel::Work => "work",
            UpdateChannel::Pro => "pro",
        }
    }
}

/// The conflict policies a scope can carry. Closed set: the manifest's own
/// vocabulary (context section 4.5), so an unknown value in the manifest is a
/// deserialize error here, not a silent default.
#[derive(Clone, Copy, Debug, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ConflictPolicy {
    ServerWins,
    LastWriteWinsByField,
    Manual,
}

/// How far offline a micro app goes (the manifest's offline modes).
#[derive(Clone, Copy, Debug, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum OfflineMode {
    Full,
    ReadCachePlusOutbox,
    ReadCacheOnly,
    None,
}

/// One micro app's offline block, copied verbatim from the manifest.
#[derive(Clone, Debug, PartialEq, serde::Deserialize)]
pub struct OfflineBlock {
    pub mode: OfflineMode,
    pub scopes: Vec<String>,
    pub conflict_policy: BTreeMap<String, ConflictPolicy>,
}

/// One micro app the profile loads.
#[derive(Clone, Debug, PartialEq, serde::Deserialize)]
pub struct MicroApp {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub roles: Vec<String>,
    #[serde(default)]
    pub devices: Vec<String>,
    #[serde(default)]
    pub offline: Option<OfflineBlock>,
}

/// The device permissions a profile grants. Camera and fine location are
/// Field only (plan B.3): this model is what the shells read; the enforcement
/// is each profile's own Tauri capability file and Android manifest, never a
/// shared open grant.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Permissions {
    pub camera: bool,
    pub fine_location: bool,
}

/// The main window's defaults on a desktop that has no device size of its
/// own. A phone or tablet sizes the window itself; these are the dev and
/// fallback values.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct WindowDefaults {
    pub title: &'static str,
    pub width: f64,
    pub height: f64,
}

/// Where a profile signs in: the FB Console API, its HH project and the core
/// (AUTH-SHELL-CONTRACT r6, "Commands are fenced"). These three values are
/// fixed in the Rust profile and read at build time; they never come from the
/// webview or from a command argument. `HH_CONSOLE_ENDPOINT` defaults to the
/// Console API host; `HH_CONSOLE_PROJECT_ID` and `HH_CORE_URL` have no default
/// (an unconfigured build refuses to sign in, `unavailable`, rather than
/// guess). All three must be https; nothing here is a secret.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AuthConfig {
    pub console_endpoint: String,
    pub project_id: String,
    pub core_url: String,
    /// Only `for_test` sets it: a loopback http URL for the in process mock server.
    allow_loopback_http: bool,
}

/// A configuration that is unusable. It names no value.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct BadConfig;

fn clean_base_url(value: &str, allow_loopback_http: bool) -> bool {
    let rest = if let Some(rest) = value.strip_prefix("https://") {
        rest
    } else if allow_loopback_http && value.starts_with("http://127.0.0.1:") {
        &value["http://".len()..]
    } else {
        return false;
    };
    let host_and_path = rest.trim_end_matches('/');
    let host = host_and_path.split('/').next().unwrap_or("");
    !host.is_empty()
        && !host.contains('@')
        && !host_and_path.contains(['?', '#', ' ', '\\'])
        && host_and_path.bytes().all(|b| b.is_ascii_graphic())
}

impl AuthConfig {
    /// The build's configuration, unvalidated (`validate` is the gate).
    pub fn from_build() -> Self {
        Self {
            console_endpoint: option_env!("HH_CONSOLE_ENDPOINT")
                .unwrap_or("https://api.futurebuild.ai/v1")
                .to_string(),
            project_id: option_env!("HH_CONSOLE_PROJECT_ID")
                .unwrap_or("")
                .to_string(),
            core_url: option_env!("HH_CORE_URL").unwrap_or("").to_string(),
            allow_loopback_http: false,
        }
    }

    /// A validated configuration from three values.
    pub fn new(
        console_endpoint: &str,
        project_id: &str,
        core_url: &str,
    ) -> Result<Self, BadConfig> {
        let cfg = Self {
            console_endpoint: console_endpoint.to_string(),
            project_id: project_id.to_string(),
            core_url: core_url.to_string(),
            allow_loopback_http: false,
        };
        cfg.validate()?;
        Ok(cfg)
    }

    /// Tests only: allows `http://127.0.0.1:<port>` so the in process mock
    /// server can stand in for Console and the core.
    #[cfg(test)]
    pub fn for_test(console_endpoint: &str, project_id: &str, core_url: &str) -> Self {
        Self {
            console_endpoint: console_endpoint.to_string(),
            project_id: project_id.to_string(),
            core_url: core_url.to_string(),
            allow_loopback_http: true,
        }
    }

    /// https URLs without credentials, query or fragment, and a project id of
    /// the shape Console accepts (1 to 36 of letters, digits, underscore and
    /// hyphen).
    pub fn validate(&self) -> Result<(), BadConfig> {
        let project_ok = (1..=36).contains(&self.project_id.len())
            && self
                .project_id
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-');
        if project_ok
            && clean_base_url(&self.console_endpoint, self.allow_loopback_http)
            && clean_base_url(&self.core_url, self.allow_loopback_http)
        {
            Ok(())
        } else {
            Err(BadConfig)
        }
    }
}

/// A complete profile.
#[derive(Debug)]
pub struct Profile {
    pub identifier: &'static str,
    pub product_name: &'static str,
    pub brand: &'static str,
    pub channel: UpdateChannel,
    pub apps: Vec<MicroApp>,
    pub permissions: Permissions,
    pub window: WindowDefaults,
    /// Where this profile signs in (fixed here, never from the webview).
    pub auth: AuthConfig,
}

impl Profile {
    /// The union of the profile's offline scopes across its micro apps,
    /// sorted, deduplicated: the scope set the offline store pulls (plan B.3).
    pub fn offline_scopes(&self) -> Vec<&str> {
        let mut scopes: Vec<&str> = self
            .apps
            .iter()
            .filter_map(|a| a.offline.as_ref())
            .flat_map(|o| o.scopes.iter().map(String::as_str))
            .collect();
        scopes.sort_unstable();
        scopes.dedup();
        scopes
    }
}

/// A declared skeleton: what the profile will be, none of the wiring.
#[derive(Debug)]
pub struct Skeleton {
    pub identifier: &'static str,
    pub product_name: &'static str,
    pub brand: &'static str,
    pub channel: UpdateChannel,
    pub apps: Vec<MicroApp>,
}

#[derive(serde::Deserialize)]
struct GeneratedFile {
    #[allow(dead_code)]
    shape: u32,
    #[allow(dead_code)]
    app: String,
    shell: String,
    #[allow(dead_code)]
    brand: Option<String>,
    apps: Vec<MicroApp>,
}

const FIELD_JSON: &str = include_str!("../profiles/field.json");
const WORK_JSON: &str = include_str!("../profiles/work.json");
const PRO_JSON: &str = include_str!("../profiles/pro.json");

fn parse(json: &str, want_shell: &str) -> GeneratedFile {
    let file: GeneratedFile = serde_json::from_str(json).expect("a generated profile file parses");
    assert_eq!(file.shell, want_shell, "the generated file names its shell");
    file
}

/// The Field profile, complete: identifier, product name, brand, channel, the
/// manifest's hh-field micro apps with their offline blocks, Field's device
/// permissions (camera and fine location, Field only) and the window
/// defaults.
pub fn field() -> &'static Profile {
    static FIELD: OnceLock<Profile> = OnceLock::new();
    FIELD.get_or_init(|| {
        let file = parse(FIELD_JSON, "hh-field");
        Profile {
            identifier: "ai.futurebuild.hh.field",
            product_name: "HH Field",
            brand: "fb-tokens",
            channel: UpdateChannel::Field,
            apps: file.apps,
            permissions: Permissions {
                camera: true,
                fine_location: true,
            },
            window: WindowDefaults {
                title: "HH Field",
                width: 412.0,
                height: 915.0,
            },
            auth: AuthConfig::from_build(),
        }
    })
}

/// The Work profile, complete: identifier, product name, brand, channel, the
/// manifest's hh-work micro apps with their offline blocks, NO camera and NO
/// fine location (those are Field only, plan B.3) and desktop and tablet
/// window defaults (a tablet sizes the window itself).
pub fn work() -> &'static Profile {
    static WORK: OnceLock<Profile> = OnceLock::new();
    WORK.get_or_init(|| {
        let file = parse(WORK_JSON, "hh-work");
        Profile {
            identifier: "ai.futurebuild.hh.work",
            product_name: "HH Work",
            brand: "fb-tokens",
            channel: UpdateChannel::Work,
            apps: file.apps,
            permissions: Permissions {
                camera: false,
                fine_location: false,
            },
            window: WindowDefaults {
                title: "HH Work",
                width: 1280.0,
                height: 800.0,
            },
            auth: AuthConfig::from_build(),
        }
    })
}

/// The Pro profile, declared skeleton (its crate is still a README).
pub fn pro() -> &'static Skeleton {
    static PRO: OnceLock<Skeleton> = OnceLock::new();
    PRO.get_or_init(|| {
        let file = parse(PRO_JSON, "hh-pro");
        Skeleton {
            identifier: "ai.futurebuild.hh.pro",
            product_name: "HH Pro",
            brand: "hh-pro",
            channel: UpdateChannel::Pro,
            apps: file.apps,
        }
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn field_profile_is_complete() {
        let p = field();
        assert_eq!(p.identifier, "ai.futurebuild.hh.field");
        assert_eq!(p.product_name, "HH Field");
        assert_eq!(p.brand, "fb-tokens");
        assert_eq!(p.channel, UpdateChannel::Field);
        assert_eq!(p.window.title, "HH Field");
        assert_eq!(
            p.permissions,
            Permissions {
                camera: true,
                fine_location: true
            }
        );
    }

    #[test]
    fn field_identifier_matches_the_tauri_config() {
        // The profile model and tauri.conf.json must name the same
        // identifier: the config is what the OS installs, the model is what
        // the Rust side reasons with. include_str keeps the pin working on a
        // fresh clone (gen/schemas stays ignored).
        let config = include_str!("../../hh-field/tauri.conf.json");
        let value: serde_json::Value =
            serde_json::from_str(config).expect("tauri.conf.json parses");
        assert_eq!(
            value["identifier"].as_str(),
            Some(field().identifier),
            "tauri.conf.json identifier and the Field profile drifted"
        );
    }

    #[test]
    fn field_holds_the_manifests_field_apps_with_offline_blocks() {
        let apps = &field().apps;
        let ids: Vec<&str> = apps.iter().map(|a| a.id.as_str()).collect();
        assert_eq!(ids, ["yard-pick", "drive-run", "house-messages"]);
        for app in apps {
            let offline = app
                .offline
                .as_ref()
                .expect("a field app carries its offline block");
            assert!(!offline.scopes.is_empty(), "{} has scopes", app.id);
        }
    }

    #[test]
    fn field_offline_blocks_match_the_plan() {
        let apps = &field().apps;
        let yard = apps.iter().find(|a| a.id == "yard-pick").unwrap();
        let yard = yard.offline.as_ref().unwrap();
        assert_eq!(yard.mode, OfflineMode::Full);
        assert_eq!(
            yard.conflict_policy.get("pick_tasks"),
            Some(&ConflictPolicy::ServerWins)
        );
        assert_eq!(
            yard.conflict_policy.get("shortages"),
            Some(&ConflictPolicy::Manual)
        );
        let drive = apps.iter().find(|a| a.id == "drive-run").unwrap();
        let drive = drive.offline.as_ref().unwrap();
        assert_eq!(
            drive.conflict_policy.get("stops"),
            Some(&ConflictPolicy::ServerWins)
        );
        assert_eq!(
            drive.conflict_policy.get("pod"),
            Some(&ConflictPolicy::Manual)
        );
        let messages = apps.iter().find(|a| a.id == "house-messages").unwrap();
        let messages = messages.offline.as_ref().unwrap();
        assert_eq!(messages.mode, OfflineMode::ReadCachePlusOutbox);
    }

    #[test]
    fn field_scope_union_spans_its_apps() {
        let scopes = field().offline_scopes();
        for scope in [
            "pick_tasks",
            "weights",
            "runs",
            "pod",
            "channels",
            "attachments",
        ] {
            assert!(scopes.contains(&scope), "the union holds {scope}");
        }
        let unique_and_sorted = {
            let mut sorted = scopes.clone();
            sorted.dedup();
            sorted == scopes && scopes.windows(2).all(|w| w[0] <= w[1])
        };
        assert!(unique_and_sorted, "the union is sorted and deduplicated");
    }

    #[test]
    fn work_profile_is_complete() {
        let p = work();
        assert_eq!(p.identifier, "ai.futurebuild.hh.work");
        assert_eq!(p.product_name, "HH Work");
        assert_eq!(p.brand, "fb-tokens");
        assert_eq!(p.channel, UpdateChannel::Work);
        assert_eq!(p.window.title, "HH Work");
        // Desktop and tablet defaults: wider than tall, unlike Field's phone.
        assert!(p.window.width > p.window.height);
        assert!(p.window.width >= 1024.0);
    }

    #[test]
    fn work_signs_in_from_the_build_configuration_like_field() {
        // Both profiles read the same three build time values (names only:
        // HH_CONSOLE_ENDPOINT, HH_CONSOLE_PROJECT_ID, HH_CORE_URL); nothing
        // about sign in is typed into the profile, and an unset build is
        // refused by validate rather than guessed.
        assert_eq!(work().auth, AuthConfig::from_build());
        assert_eq!(work().auth, field().auth);
        if option_env!("HH_CONSOLE_PROJECT_ID").is_none() {
            assert!(
                work().auth.validate().is_err(),
                "an unconfigured build must not validate"
            );
        }
    }

    #[test]
    fn camera_and_fine_location_are_field_only() {
        // Plan B.3: Work and Pro must never copy Field's grants.
        assert_eq!(
            work().permissions,
            Permissions {
                camera: false,
                fine_location: false
            }
        );
        assert_eq!(
            field().permissions,
            Permissions {
                camera: true,
                fine_location: true
            }
        );
    }

    #[test]
    fn work_identifier_matches_the_tauri_config() {
        let config = include_str!("../../hh-work/tauri.conf.json");
        let value: serde_json::Value =
            serde_json::from_str(config).expect("tauri.conf.json parses");
        assert_eq!(
            value["identifier"].as_str(),
            Some(work().identifier),
            "tauri.conf.json identifier and the Work profile drifted"
        );
        assert_eq!(value["productName"].as_str(), Some(work().product_name));
    }

    #[test]
    fn work_holds_the_manifests_work_apps_with_offline_blocks() {
        let ids: Vec<&str> = work().apps.iter().map(|a| a.id.as_str()).collect();
        assert_eq!(
            ids,
            [
                "counter-till",
                "quote-desk",
                "dispatch-board",
                "buying-and-stock",
                "money-desk",
                "manager-home",
                "house-messages",
                "dealer-admin"
            ]
        );
        for app in &work().apps {
            assert!(
                app.offline.is_some(),
                "{} carries its offline block",
                app.id
            );
        }
    }

    #[test]
    fn work_offline_blocks_match_the_plan() {
        let apps = &work().apps;
        let offline = |id: &str| {
            apps.iter()
                .find(|a| a.id == id)
                .unwrap()
                .offline
                .clone()
                .unwrap()
        };
        // The till has no outbox (D5): read cache only.
        assert_eq!(offline("counter-till").mode, OfflineMode::ReadCacheOnly);
        // Money Desk and Dealer Admin are online only.
        assert_eq!(offline("money-desk").mode, OfflineMode::None);
        assert!(offline("money-desk").scopes.is_empty());
        assert_eq!(offline("dealer-admin").mode, OfflineMode::None);
        // Quote drafts carry an outbox, last write wins by field.
        let quote = offline("quote-desk");
        assert_eq!(quote.mode, OfflineMode::ReadCachePlusOutbox);
        assert_eq!(
            quote.conflict_policy.get("quotes"),
            Some(&ConflictPolicy::LastWriteWinsByField)
        );
        assert_eq!(
            offline("buying-and-stock")
                .conflict_policy
                .get("cycle_counts"),
            Some(&ConflictPolicy::Manual)
        );
    }

    #[test]
    fn work_scope_union_spans_its_apps_and_holds_no_money_queue() {
        let scopes = work().offline_scopes();
        for scope in [
            "customers",
            "products",
            "price_matrix",
            "quotes",
            "purchase_orders",
            "approvals",
        ] {
            assert!(scopes.contains(&scope), "the union holds {scope}");
        }
        assert!(
            scopes.windows(2).all(|w| w[0] < w[1]),
            "the union is sorted and deduplicated"
        );
        // No app with an outbox lists a money scope (money never queues).
        for app in &work().apps {
            let o = app.offline.as_ref().unwrap();
            if matches!(o.mode, OfflineMode::ReadCachePlusOutbox | OfflineMode::Full) {
                for money in ["tenders", "payments", "invoices", "ledger"] {
                    assert!(
                        !o.scopes.iter().any(|s| s == money),
                        "{} queues {money}",
                        app.id
                    );
                }
            }
        }
    }

    #[test]
    fn profile_identifiers_and_channels_are_distinct() {
        let ids = [field().identifier, work().identifier, pro().identifier];
        assert_eq!(
            ids.iter().collect::<std::collections::BTreeSet<_>>().len(),
            3
        );
        assert_ne!(field().channel, work().channel);
    }

    #[test]
    fn pro_is_a_declared_skeleton() {
        let pro = pro();
        assert_eq!(pro.identifier, "ai.futurebuild.hh.pro");
        assert_eq!(pro.brand, "hh-pro");
        let pro_ids: Vec<&str> = pro.apps.iter().map(|a| a.id.as_str()).collect();
        assert_eq!(pro_ids, ["pro-procurement", "pro-pay", "pro-field"]);
    }

    #[test]
    fn the_policy_set_is_closed() {
        // An unknown policy word in a generated file must fail the parse, not
        // become a default.
        let err = serde_json::from_str::<ConflictPolicy>("\"winner_takes_all\"");
        assert!(err.is_err());
        assert_eq!(
            serde_json::from_str::<ConflictPolicy>("\"last_write_wins_by_field\"").unwrap(),
            ConflictPolicy::LastWriteWinsByField
        );
    }
}
