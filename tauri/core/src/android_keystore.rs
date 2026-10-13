//! The Android Keystore bridge: JNI calls into `KeystoreCustody.kt` in the
//! generated Android project (tauri/hh-field/gen/android).
//!
//! Where the work happens: the Kotlin class holds an AES-256-GCM key in the
//! Android Keystore (non exportable, hardware backed where the device has it)
//! and keeps only ciphertext in the app's private no backup directory. The key
//! never leaves the Keystore and no plaintext is ever written. This file only
//! marshals strings across JNI and treats ANY failure (a Java exception, a
//! missing class, a JNI error) as `BridgeFailure`; it drops the exception and
//! its message, so nothing a Java layer could say about a service, an account
//! or a value reaches an error or a log. There is no fallback to a file, to
//! preferences or to memory.
//!
//! Compiled for Android only. The trait seam it plugs into (`KeystoreBridge`
//! behind `BridgedCustody`) is compiled and unit tested on every target.

use jni::{
    objects::{JClass, JObject, JString, JValue},
    JNIEnv, JavaVM,
};

use crate::custody::{BridgeFailure, KeystoreBridge};

/// The Kotlin class, by its binary name.
const CLASS_NAME: &str = "ai.futurebuild.hh.field.KeystoreCustody";
const STRINGS3_VOID: &str = "(Landroid/content/Context;Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;)V";
const STRINGS2_VOID: &str = "(Landroid/content/Context;Ljava/lang/String;Ljava/lang/String;)V";
const STRINGS2_STRING: &str = "(Landroid/content/Context;Ljava/lang/String;Ljava/lang/String;)Ljava/lang/String;";

pub struct JniKeystore;

/// Run `f` with an attached environment, the application context and the
/// Kotlin class. The class is loaded through the application's class loader:
/// a native thread's own `FindClass` sees only the system loader.
fn with_class<T>(
    f: impl FnOnce(&mut JNIEnv, &JObject, &JClass) -> jni::errors::Result<T>,
) -> Result<T, BridgeFailure> {
    let ctx = ndk_context::android_context();
    if ctx.vm().is_null() || ctx.context().is_null() {
        return Err(BridgeFailure);
    }
    // SAFETY: tao initialises these pointers with the live JavaVM and a global
    // reference to the application context before any command can run.
    let vm = unsafe { JavaVM::from_raw(ctx.vm().cast()) }.map_err(|_| BridgeFailure)?;
    let mut env = vm.attach_current_thread().map_err(|_| BridgeFailure)?;
    // SAFETY: a global reference owned by tao; this wrapper is never deleted here.
    let context = unsafe { JObject::from_raw(ctx.context().cast()) };

    let result = (|| {
        let loader = env.call_method(&context, "getClassLoader", "()Ljava/lang/ClassLoader;", &[])?.l()?;
        let name = env.new_string(CLASS_NAME)?;
        let class = env
            .call_method(&loader, "loadClass", "(Ljava/lang/String;)Ljava/lang/Class;", &[JValue::Object(&name)])?
            .l()?;
        f(&mut env, &context, &JClass::from(class))
    })();
    if env.exception_check().unwrap_or(true) {
        let _ = env.exception_clear();
    }
    result.map_err(|_| BridgeFailure)
}

fn strings<'a>(env: &mut JNIEnv<'a>, service: &str, account: &str) -> jni::errors::Result<(JString<'a>, JString<'a>)> {
    Ok((env.new_string(service)?, env.new_string(account)?))
}

impl KeystoreBridge for JniKeystore {
    fn put(&self, service: &str, account: &str, value: &str) -> Result<(), BridgeFailure> {
        with_class(|env, context, class| {
            let (service, account) = strings(env, service, account)?;
            let value = env.new_string(value)?;
            env.call_static_method(
                class,
                "put",
                STRINGS3_VOID,
                &[JValue::Object(context), JValue::Object(&service), JValue::Object(&account), JValue::Object(&value)],
            )?;
            Ok(())
        })
    }

    fn get(&self, service: &str, account: &str) -> Result<Option<String>, BridgeFailure> {
        with_class(|env, context, class| {
            let (service, account) = strings(env, service, account)?;
            let found = env
                .call_static_method(
                    class,
                    "get",
                    STRINGS2_STRING,
                    &[JValue::Object(context), JValue::Object(&service), JValue::Object(&account)],
                )?
                .l()?;
            if found.is_null() {
                return Ok(None);
            }
            let text: String = env.get_string(&JString::from(found))?.into();
            Ok(Some(text))
        })
    }

    fn delete(&self, service: &str, account: &str) -> Result<(), BridgeFailure> {
        with_class(|env, context, class| {
            let (service, account) = strings(env, service, account)?;
            env.call_static_method(
                class,
                "delete",
                STRINGS2_VOID,
                &[JValue::Object(context), JValue::Object(&service), JValue::Object(&account)],
            )?;
            Ok(())
        })
    }
}
