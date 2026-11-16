package io.github.sidd065.agentcompanion.core

import android.net.Uri
import io.github.sidd065.agentcompanion.AppGraph
import io.github.sidd065.agentcompanion.BuildConfig
import io.github.sidd065.agentcompanion.net.Noise
import io.github.sidd065.agentcompanion.net.RpcException
import io.github.sidd065.agentcompanion.net.SecureSocket
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import java.security.MessageDigest

/** A parsed pairing link / QR code. */
data class PairLink(val base: String, val pairId: String, val hostKey: String, val secret: String, val name: String) {
    val hostKeyBytes: ByteArray get() = DeviceKeys.unb64url(hostKey)
    val fingerprint: String get() = Pairing.fingerprint(hostKeyBytes)
    /** The daemon's host id: base64url(sha256(key)[:12]) (same as the daemon). */
    val hostId: String get() = Pairing.keyId(hostKeyBytes)
}

object Pairing {
    /**
     * Accepts `https://<host>/p/<pairId>#k=<key>&s=<secret>&n=<name>` (the QR
     * code) and `agentc://pair?u=<base>&p=<pairId>&k=…&s=…&n=…` (deep link).
     */
    fun parse(text: String): PairLink? = runCatching {
        val u = Uri.parse(text.trim())
        val params: (String) -> String?
        val base: String
        val pairId: String
        if (u.scheme == "agentc") {
            params = { u.getQueryParameter(it) }
            base = params("u") ?: return null
            pairId = params("p") ?: return null
        } else if (u.scheme == "https" || u.scheme == "http") {
            val frag = Uri.parse("x://x?" + (u.encodedFragment ?: return null))
            params = { frag.getQueryParameter(it) }
            val segs = u.pathSegments
            if (segs.size != 2 || segs[0] != "p") return null
            base = "${u.scheme}://${u.encodedAuthority}"
            pairId = segs[1]
        } else return null
        val k = params("k") ?: return null
        val s = params("s") ?: return null
        if (DeviceKeys.unb64url(k).size != 32 || DeviceKeys.unb64url(s).size != 32) return null
        PairLink(base.trimEnd('/'), pairId, k, s, params("n").orEmpty())
    }.getOrNull()

    fun keyId(pub: ByteArray): String {
        val sum = MessageDigest.getInstance("SHA-256").digest(pub)
        return android.util.Base64.encodeToString(sum.copyOf(12), android.util.Base64.URL_SAFE or android.util.Base64.NO_WRAP or android.util.Base64.NO_PADDING)
    }

    fun fingerprint(pub: ByteArray): String {
        val hex = MessageDigest.getInstance("SHA-256").digest(pub).copyOf(8).joinToString("") { "%02x".format(it) }
        return "${hex.substring(0, 4)}-${hex.substring(4, 8)}-${hex.substring(8, 12)}-${hex.substring(12, 16)}"
    }

    sealed interface Result {
        data class Paired(val host: HostConfig) : Result
        data class Updated(val host: HostConfig) : Result
    }

    /**
     * Pairs with the host behind [link]. If this phone is already paired with
     * that host (same identity key) only its address is updated.
     */
    suspend fun pair(g: AppGraph, link: PairLink): Result {
        val existing = g.prefs.value.hosts.firstOrNull { it.hostKey == link.hostKey }
        if (existing != null) {
            val updated = existing.copy(url = link.base, name = existing.name.ifBlank { link.name })
            g.prefs.update { s -> s.copy(hosts = s.hosts.map { if (it.id == existing.id) updated else it }) }
            return Result.Updated(updated)
        }
        val (wrapped, pub) = DeviceKeys.create()
        val keys = Noise.KeyPair(DeviceKeys.unwrap(wrapped), DeviceKeys.unb64(pub))
        val done = CompletableDeferred<JsonObject>()
        val hello = buildJsonObject {
            put("v", 1); put("purpose", "rpc"); put("name", g.deviceName); put("platform", "android"); put("app", BuildConfig.VERSION_NAME)
        }
        val socket = SecureSocket(g.http, link.base, link.hostKeyBytes, keys, hello, object : SecureSocket.Listener {
            override fun onOpen(socket: SecureSocket, reply: JsonObject) {
                done.complete(reply)
            }

            override fun onClosed(error: String?) {
                done.completeExceptionally(RpcException(error ?: "The host closed the connection"))
            }
        }, mode = SecureSocket.MODE_PAIR, pairId = link.pairId, psk = DeviceKeys.unb64url(link.secret))
        socket.connect()
        val reply = try {
            withTimeout(45_000) { done.await() }
        } finally {
            socket.close()
        }
        val host = HostConfig(
            id = reply["hostId"]?.jsonPrimitive?.content ?: link.hostId,
            name = reply["host"]?.jsonPrimitive?.content ?: link.name,
            url = link.base,
            hostKey = link.hostKey,
            devicePub = pub,
            devicePrivWrapped = wrapped,
            deviceId = reply["deviceId"]?.jsonPrimitive?.content.orEmpty(),
            fingerprint = link.fingerprint,
            pairedAt = System.currentTimeMillis(),
        )
        g.prefs.update { s -> s.copy(hosts = s.hosts.filterNot { it.id == host.id } + host, activeHostId = s.activeHostId ?: host.id) }
        return Result.Paired(host)
    }
}
