package io.github.sidd065.agentcompanion.core

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import io.github.sidd065.agentcompanion.net.Noise
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Per-host device keys. The X25519 private key is stored wrapped (AES-GCM) by a
 * non-exportable Android Keystore key, so it never sits in app storage in the clear.
 */
object DeviceKeys {
    private const val ALIAS = "agentc-device-keys"

    private fun wrapper(): SecretKey {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (ks.getKey(ALIAS, null) as? SecretKey)?.let { return it }
        val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        gen.init(
            KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return gen.generateKey()
    }

    fun b64(b: ByteArray): String = Base64.encodeToString(b, Base64.NO_WRAP)
    fun unb64(s: String): ByteArray = Base64.decode(s, Base64.NO_WRAP)
    fun unb64url(s: String): ByteArray = Base64.decode(s, Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING)

    /** Generates a new key pair; returns (wrapped private key, public key b64). */
    fun create(): Pair<String, String> {
        val kp = Noise.generateKeyPair()
        return wrap(kp.privateKey) to b64(kp.publicKey)
    }

    fun wrap(priv: ByteArray): String {
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.ENCRYPT_MODE, wrapper())
        return b64(c.iv) + ":" + b64(c.doFinal(priv))
    }

    fun unwrap(wrapped: String): ByteArray {
        val (iv, ct) = wrapped.split(':', limit = 2)
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.DECRYPT_MODE, wrapper(), GCMParameterSpec(128, unb64(iv)))
        return c.doFinal(unb64(ct))
    }

    fun keyPair(h: HostConfig): Noise.KeyPair = Noise.KeyPair(unwrap(h.devicePrivWrapped), unb64(h.devicePub))
}
