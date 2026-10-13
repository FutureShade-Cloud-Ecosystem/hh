//! The profile model of the HH shells (plan B.3, decision row 1519): one
//! Tauri 2 workspace, three thin profiles that differ in identifier, the
//! micro app set, permissions, brand, the offline scope sets, window defaults
//! and the update channel.
//!
//! The Field profile is complete. Work and Pro are declared skeletons:
//! identifier, product name, apps and channel, nothing else, until their own
//! items land (their crates are still READMEs).
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
        }
    })
}

/// The Work profile, declared skeleton (its crate is still a README).
pub fn work() -> &'static Skeleton {
    static WORK: OnceLock<Skeleton> = OnceLock::new();
    WORK.get_or_init(|| {
        let file = parse(WORK_JSON, "hh-work");
        Skeleton {
            identifier: "ai.futurebuild.hh.work",
            product_name: "HH Work",
            brand: "fb-tokens",
            channel: UpdateChannel::Work,
            apps: file.apps,
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
    fn work_and_pro_are_declared_skeletons() {
        let work = work();
        assert_eq!(work.identifier, "ai.futurebuild.hh.work");
        assert_eq!(work.product_name, "HH Work");
        assert_eq!(work.channel, UpdateChannel::Work);
        let work_ids: Vec<&str> = work.apps.iter().map(|a| a.id.as_str()).collect();
        assert!(work_ids.contains(&"counter-till"));
        assert!(work_ids.contains(&"money-desk"));
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
