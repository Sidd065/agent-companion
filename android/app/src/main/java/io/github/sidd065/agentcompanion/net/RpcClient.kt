package io.github.sidd065.agentcompanion.net

import android.util.Log
import io.github.sidd065.agentcompanion.BuildConfig
import io.github.sidd065.agentcompanion.core.AppJson
import io.github.sidd065.agentcompanion.core.DeviceKeys
import io.github.sidd065.agentcompanion.core.HostConfig
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import kotlinx.serialization.json.put
import okhttp3.OkHttpClient
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicLong

class RpcException(message: String) : Exception(message)

data class ServerEvent(val name: String, val data: JsonElement)

sealed interface ConnState {
    data object Idle : ConnState
    data class Connecting(val attempt: Int, val lastError: String?) : ConnState
    data class Connected(val hello: JsonObject) : ConnState
    data class Failed(val error: String) : ConnState
}

/**
 * JSON-RPC to an agentc daemon over an end-to-end encrypted [SecureSocket],
 * with automatic reconnects.
 */
class RpcClient(
    private val host: HostConfig,
    private val http: OkHttpClient,
    private val scope: CoroutineScope,
    private val deviceName: String,
    private val keyPairProvider: (HostConfig) -> Noise.KeyPair = DeviceKeys::keyPair,
) {
    private val _state = MutableStateFlow<ConnState>(ConnState.Idle)
    val state: StateFlow<ConnState> = _state

    private val _events = MutableSharedFlow<ServerEvent>(extraBufferCapacity = 4096, onBufferOverflow = BufferOverflow.SUSPEND)
    val events: SharedFlow<ServerEvent> = _events

    private val pending = ConcurrentHashMap<Long, CompletableDeferred<JsonElement>>()
    private val nextId = AtomicLong(0)
    @Volatile private var ws: SecureSocket? = null
    private var loop: Job? = null
    private val wake = MutableStateFlow(0)
    @Volatile private var running = false


    fun start() {
        if (running) return
        running = true
        loop = scope.launch { connectLoop() }
    }

    fun stop() {
        running = false
        loop?.cancel()
        loop = null
        ws?.close()
        ws = null
        failPending("disconnected")
        _state.value = ConnState.Idle
    }

    /** Reconnect immediately (e.g. after a network change). */
    fun reconnectNow() {
        val s = _state.value
        if (s is ConnState.Connected) {
            // Probe: a dead socket will fail quickly and trigger the loop.
            ws?.sendText("""{"id":0,"method":"ping"}""")
        }
        wake.value++
    }

    private suspend fun connectLoop() {
        var attempt = 0
        var lastErr: String? = null
        while (scope.isActive && running) {
            attempt++
            _state.value = ConnState.Connecting(attempt, lastErr)
            val keys = runCatching { keyPairProvider(host) }.getOrElse {
                _state.value = ConnState.Failed("This phone's key for ${host.label} is unavailable (${it.message}). Pair again.")
                return
            }
            val closed = CompletableDeferred<String?>()
            val opened = CompletableDeferred<JsonObject>()
            val hello = buildJsonObject {
                put("v", 1); put("purpose", "rpc"); put("name", deviceName); put("platform", "android"); put("app", BuildConfig.VERSION_NAME)
            }
            val socket = SecureSocket(http, host.url, DeviceKeys.unb64url(host.hostKey), keys, hello, object : SecureSocket.Listener {
                override fun onOpen(socket: SecureSocket, reply: JsonObject) {
                    opened.complete(reply)
                }

                override fun onText(text: String) = handleMessage(text)

                override fun onClosed(error: String?) {
                    opened.completeExceptionally(RpcException(error ?: "closed"))
                    closed.complete(error ?: "closed")
                }
            })
            ws = socket
            socket.connect()
            val ok = runCatching { withTimeout(30_000) { opened.await() } }
            if (ok.isSuccess) {
                attempt = 0
                lastErr = null
                val hello2 = runCatching { callRaw("hello", buildJsonObject { put("device", deviceName); put("app", "android") }, 30_000) }
                if (hello2.isSuccess) {
                    _state.value = ConnState.Connected(hello2.getOrThrow().jsonObject)
                    lastErr = closed.await()
                } else {
                    lastErr = hello2.exceptionOrNull()?.message
                    socket.cancel()
                }
            } else {
                lastErr = ok.exceptionOrNull()?.message ?: "connection failed"
                socket.cancel()
            }
            if (lastErr?.contains("isn't paired") == true || lastErr?.contains("isn't the paired host") == true) {
                // Not retryable: the user has to pair again.
                ws = null
                failPending(lastErr)
                _state.value = ConnState.Failed(lastErr)
                return
            }
            ws = null
            failPending(lastErr ?: "disconnected")
            if (!running) break
            Log.i("RpcClient", "${host.label}: disconnected: $lastErr")
            _state.value = ConnState.Connecting(attempt + 1, lastErr)
            val backoff = (1000L shl minOf(attempt, 5)).coerceAtMost(30_000)
            val w = wake.value
            withTimeoutOrNull(backoff) { wake.first { it != w } }
        }
    }

    private fun failPending(msg: String) {
        val all = pending.values.toList()
        pending.clear()
        all.forEach { it.completeExceptionally(RpcException(msg)) }
    }

    private fun handleMessage(text: String) {
        val obj = runCatching { AppJson.parseToJsonElement(text).jsonObject }.getOrNull() ?: return
        val ev = obj["event"]
        if (ev != null && ev !is JsonNull) {
            val name = ev.jsonPrimitive.content
            if (!_events.tryEmit(ServerEvent(name, obj["data"] ?: JsonNull))) {
                scope.launch { _events.emit(ServerEvent(name, obj["data"] ?: JsonNull)) }
            }
            return
        }
        val id = obj["id"]?.jsonPrimitive?.longOrNull ?: return
        val d = pending.remove(id) ?: return
        val err = obj["error"]
        if (err != null && err !is JsonNull) {
            val msg = runCatching { err.jsonObject["message"]?.jsonPrimitive?.content }.getOrNull() ?: err.toString()
            d.completeExceptionally(RpcException(msg))
        } else {
            d.complete(obj["result"] ?: JsonNull)
        }
    }

    private suspend fun callRaw(method: String, params: JsonElement?, timeoutMs: Long): JsonElement {
        val socket = ws ?: throw RpcException("not connected")
        val id = nextId.incrementAndGet()
        val d = CompletableDeferred<JsonElement>()
        pending[id] = d
        val msg = buildJsonObject {
            put("id", id)
            put("method", method)
            put("params", params ?: JsonObject(emptyMap()))
        }
        if (!socket.sendText(msg.toString())) {
            pending.remove(id)
            throw RpcException("not connected")
        }
        return try {
            withTimeout(timeoutMs) { d.await() }
        } finally {
            pending.remove(id)
        }
    }

    /** Waits for a connection (up to [connectWaitMs]) then performs the call. */
    suspend fun call(method: String, params: JsonElement? = null, timeoutMs: Long = 120_000, connectWaitMs: Long = 15_000): JsonElement {
        if (_state.value !is ConnState.Connected) {
            if (!running) start()
            reconnectNow()
            withTimeoutOrNull(connectWaitMs) { _state.first { it is ConnState.Connected } }
                ?: throw RpcException(
                    (state.value as? ConnState.Connecting)?.lastError?.let { "Not connected: $it" }
                        ?: (state.value as? ConnState.Failed)?.error ?: "Not connected to ${host.label}",
                )
        }
        return callRaw(method, params, timeoutMs)
    }

    val isConnected: Boolean get() = _state.value is ConnState.Connected
}
