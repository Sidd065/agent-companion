package io.github.sidd065.agentcompanion.net

import io.github.sidd065.agentcompanion.core.AppJson
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString
import java.io.ByteArrayOutputStream
import java.net.URLEncoder

/**
 * An end-to-end encrypted message channel to an agentc daemon over a
 * WebSocket (usually through a Cloudflare quick tunnel, which only ever sees
 * ciphertext). See Noise.kt and the daemon's internal/secure package.
 *
 * Framing after the handshake: every WebSocket message is one Noise transport
 * message whose plaintext is [flags][data]; bit0 = last fragment, bit1 = binary.
 */
class SecureSocket(
    private val http: OkHttpClient,
    private val baseUrl: String,
    private val daemonKey: ByteArray,
    private val deviceKey: Noise.KeyPair,
    private val hello: JsonObject,
    private val listener: Listener,
    private val mode: String = MODE_CONNECT,
    private val pairId: String? = null,
    private val psk: ByteArray? = null,
) {
    interface Listener {
        fun onOpen(socket: SecureSocket, reply: JsonObject) {}
        fun onText(text: String) {}
        fun onBinary(bytes: ByteArray) {}
        /** Called once: [error] is null for a normal close. */
        fun onClosed(error: String?) {}
    }

    private var ws: WebSocket? = null
    private var initiator: Noise.Initiator? = null
    private var session: Noise.Session? = null
    private val sendLock = Any()
    private val inbuf = ByteArrayOutputStream()
    @Volatile private var closed = false
    @Volatile var isOpen = false
        private set

    fun connect() {
        val req = Request.Builder().url(wsUrl(baseUrl, mode, pairId)).build()
        ws = http.newWebSocket(req, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                try {
                    val prologue = (PROLOGUE + if (mode == MODE_PAIR) "pair\n$pairId" else "conn\n").toByteArray()
                    val ini = Noise.Initiator(deviceKey, daemonKey, prologue, psk)
                    initiator = ini
                    webSocket.send(ini.writeMessage1(hello.toString().toByteArray()).toByteString())
                } catch (e: Exception) {
                    fail(webSocket, "Couldn't start a secure connection: ${e.message}")
                }
            }

            override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                val s = session
                if (s == null) {
                    val ini = initiator ?: return fail(webSocket, "Unexpected handshake message")
                    val result = runCatching { ini.readMessage2(bytes.toByteArray()) }
                    val sess = result.getOrElse {
                        return fail(webSocket, "This server isn't the paired host (its identity key doesn't match). Pair again if the host was reinstalled.")
                    }
                    session = sess
                    isOpen = true
                    val reply = runCatching { AppJson.parseToJsonElement(String(sess.responderPayload)).jsonObject }.getOrDefault(JsonObject(emptyMap()))
                    listener.onOpen(this@SecureSocket, reply)
                    return
                }
                val pt = try {
                    s.recv.decrypt(bytes.toByteArray())
                } catch (e: Exception) {
                    return fail(webSocket, "Received a corrupted message")
                }
                if (pt.isEmpty()) return fail(webSocket, "Empty frame")
                inbuf.write(pt, 1, pt.size - 1)
                if (inbuf.size() > MAX_LOGICAL) return fail(webSocket, "Message too large")
                if (pt[0].toInt() and FLAG_FINAL != 0) {
                    val data = inbuf.toByteArray()
                    inbuf.reset()
                    if (pt[0].toInt() and FLAG_BINARY != 0) listener.onBinary(data) else listener.onText(String(data))
                }
            }

            override fun onMessage(webSocket: WebSocket, text: String) = fail(webSocket, "Unexpected plaintext frame")

            override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                webSocket.close(1000, null)
                finish(if (code == 1000 && isOpen) null else closeReason(code, reason))
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                finish(if (code == 1000 && isOpen) null else closeReason(code, reason))
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                finish(failureReason(t, response))
            }
        })
    }

    private fun fail(webSocket: WebSocket, msg: String) {
        webSocket.cancel()
        finish(msg)
    }

    private fun finish(error: String?) {
        if (closed) return
        closed = true
        isOpen = false
        listener.onClosed(error)
    }

    private fun sendFrames(data: ByteArray, binary: Boolean): Boolean {
        val s = session ?: return false
        val w = ws ?: return false
        synchronized(sendLock) {
            var off = 0
            var first = true
            while (first || off < data.size) {
                first = false
                val n = minOf(data.size - off, MAX_FRAGMENT)
                var flags = 0
                if (off + n == data.size) flags = flags or FLAG_FINAL
                if (binary) flags = flags or FLAG_BINARY
                val pt = ByteArray(n + 1)
                pt[0] = flags.toByte()
                System.arraycopy(data, off, pt, 1, n)
                if (!w.send(s.send.encrypt(pt).toByteString())) return false
                off += n
            }
        }
        return true
    }

    fun sendText(text: String): Boolean = sendFrames(text.toByteArray(), false)

    fun sendBinary(bytes: ByteArray): Boolean = sendFrames(bytes, true)

    /** Bytes queued in OkHttp but not yet written (for flow control). */
    fun queueSize(): Long = ws?.queueSize() ?: 0

    fun close() {
        ws?.close(1000, "bye")
        finish(null)
    }

    fun cancel() {
        ws?.cancel()
        finish(null)
    }

    companion object {
        const val MODE_CONNECT = "conn"
        const val MODE_PAIR = "pair"
        private const val PROLOGUE = "agentc/1 "
        private const val FLAG_FINAL = 1
        private const val FLAG_BINARY = 2
        private const val MAX_FRAGMENT = Noise.MAX_MESSAGE - Noise.TAG_LEN - 1
        private const val MAX_LOGICAL = 64 shl 20

        fun wsUrl(base: String, mode: String, pairId: String?): String {
            val b = base.trimEnd('/')
            val scheme = when {
                b.startsWith("https://") -> "wss://" + b.removePrefix("https://")
                b.startsWith("http://") -> "ws://" + b.removePrefix("http://")
                else -> b
            }
            val q = "m=$mode" + if (mode == MODE_PAIR && pairId != null) "&p=" + URLEncoder.encode(pairId, "UTF-8") else ""
            return "$scheme/v1/ws?$q"
        }

        fun closeReason(code: Int, reason: String): String = when (reason) {
            "unpaired" -> "This phone isn't paired with the host anymore. Run `agentc pair` on it and scan the code again."
            "pairing-invalid" -> "This pairing code is invalid, already used or expired. Run `agentc pair` again."
            "handshake-failed" -> "Couldn't set up a secure connection with the host."
            "" -> "Connection closed ($code)"
            else -> reason
        }

        fun failureReason(t: Throwable, response: Response?): String = when (response?.code) {
            404, 530, 502, 1033 -> "The host's address isn't reachable. Its tunnel URL changes when the computer restarts — " +
                "it updates automatically through push, or re-scan the code from `agentc pair`."
            429 -> "Too many failed attempts; try again in a minute."
            null -> when (t) {
                is java.net.UnknownHostException -> "Can't find the host's address (offline, or its tunnel URL changed)."
                is java.net.SocketTimeoutException -> "The host didn't respond in time."
                else -> t.message ?: t.javaClass.simpleName
            }
            else -> "HTTP ${response.code} ${response.message}"
        }
    }
}
