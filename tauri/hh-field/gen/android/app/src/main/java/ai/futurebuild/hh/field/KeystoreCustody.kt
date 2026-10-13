package ai.futurebuild.hh.field

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyPermanentlyInvalidatedException
import android.security.keystore.KeyProperties
import java.io.File
import java.io.FileOutputStream
import java.io.IOException
import java.security.KeyStore
import java.security.MessageDigest
import java.security.UnrecoverableKeyException
import javax.crypto.AEADBadTagException
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Custody of the FB Console session secret on Android (AUTH-SHELL-CONTRACT r6).
 *
 * One AES-256-GCM key per service lives in the Android Keystore: non
 * exportable, hardware backed where the device has it, no user authentication
 * (a session secret must be readable in the background). The secret is
 * encrypted with it and ONLY the ciphertext is written, to the app's private
 * no backup directory (excluded from backup and device transfer; the manifest
 * also sets allowBackup=false). No plaintext is ever written, and there is no
 * fallback: if the Keystore or the file cannot be used, the call throws and the
 * Rust side reports keychain_unavailable.
 *
 * Called only from Rust over JNI (src/android_keystore.rs in hh-shell-core);
 * the webview has no path to it. It never logs and its exception messages are
 * never read by the caller.
 *
 * File format v1: [1][12 byte IV][ciphertext and 16 byte tag]. The service and
 * account are the GCM associated data, so a file moved to another entry does
 * not decrypt.
 */
object KeystoreCustody {
    private const val PROVIDER = "AndroidKeyStore"
    private const val ALIAS_PREFIX = "ai.futurebuild.hh.custody."
    private const val TRANSFORMATION = "AES/GCM/NoPadding"
    private const val FORMAT_V1: Byte = 1
    private const val IV_LEN = 12
    private const val TAG_BITS = 128
    private const val MAX_FILE_BYTES = 16 * 1024

    @JvmStatic
    fun put(context: Context, service: String, account: String, value: String) {
        val key = key(service, create = true) ?: throw IOException()
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key)
        cipher.updateAAD(aad(service, account))
        val ciphertext = cipher.doFinal(value.toByteArray(Charsets.UTF_8))
        val iv = cipher.iv
        if (iv.size != IV_LEN) throw IOException()

        val target = fileFor(context, service, account)
        val temp = File(target.parentFile, target.name + ".tmp")
        FileOutputStream(temp).use { out ->
            out.write(byteArrayOf(FORMAT_V1))
            out.write(iv)
            out.write(ciphertext)
            out.fd.sync()
        }
        if (!temp.renameTo(target)) {
            temp.delete()
            throw IOException()
        }
    }

    /**
     * The secret, or null when there is none. An entry that can never be read
     * again (malformed, a key that is gone or invalidated, a failed
     * authentication) is removed and reported as absent, which is the same as
     * no session: the person signs in again. Any other failure throws.
     */
    @JvmStatic
    fun get(context: Context, service: String, account: String): String? {
        val file = fileFor(context, service, account)
        if (!file.exists()) return null
        if (file.length() > MAX_FILE_BYTES) return unusable(file)
        val bytes = file.readBytes()
        if (bytes.size < 1 + IV_LEN + TAG_BITS / 8 || bytes[0] != FORMAT_V1) return unusable(file)
        val key = key(service, create = false) ?: return unusable(file)
        return try {
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(TAG_BITS, bytes, 1, IV_LEN))
            cipher.updateAAD(aad(service, account))
            String(cipher.doFinal(bytes, 1 + IV_LEN, bytes.size - 1 - IV_LEN), Charsets.UTF_8)
        } catch (e: AEADBadTagException) {
            unusable(file)
        } catch (e: KeyPermanentlyInvalidatedException) {
            unusable(file)
        } catch (e: UnrecoverableKeyException) {
            unusable(file)
        }
    }

    /** Remove the entry; an absent one is fine. The shared key stays. */
    @JvmStatic
    fun delete(context: Context, service: String, account: String) {
        val file = fileFor(context, service, account)
        if (file.exists() && !file.delete()) throw IOException()
    }

    private fun unusable(file: File): String? {
        file.delete()
        return null
    }

    private fun aad(service: String, account: String): ByteArray =
        "$service\u0000$account".toByteArray(Charsets.UTF_8)

    private fun fileFor(context: Context, service: String, account: String): File {
        val dir = File(context.noBackupFilesDir, "custody")
        if (!dir.isDirectory && !dir.mkdirs() && !dir.isDirectory) throw IOException()
        val name = MessageDigest.getInstance("SHA-256")
            .digest(aad(service, account))
            .joinToString("") { "%02x".format(it) }
        return File(dir, "$name.bin")
    }

    private fun key(service: String, create: Boolean): SecretKey? {
        val store = KeyStore.getInstance(PROVIDER)
        store.load(null)
        val alias = ALIAS_PREFIX + service
        (store.getKey(alias, null) as? SecretKey)?.let { return it }
        if (!create) return null
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, PROVIDER)
        generator.init(
            KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .setRandomizedEncryptionRequired(true)
                .build()
        )
        return generator.generateKey()
    }
}
