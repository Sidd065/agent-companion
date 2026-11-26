package io.github.sidd065.agentcompanion.service

import android.util.Log
import com.google.android.gms.tasks.Task
import com.google.firebase.messaging.FirebaseMessaging
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage
import io.github.sidd065.agentcompanion.AppGraph
import io.github.sidd065.agentcompanion.core.AgentNotification
import io.github.sidd065.agentcompanion.core.AppJson
import io.github.sidd065.agentcompanion.core.PermOption
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.params
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.put
import kotlin.coroutines.resume

/** Receives Firebase Cloud Messaging data messages sent by agentc daemons. */
class PushService : FirebaseMessagingService() {
    override fun onNewToken(token: String) {
        graph.push.onNewToken(token)
    }

    override fun onMessageReceived(message: RemoteMessage) {
        val d = message.data
        val g = graph
        if (d["kind"] == "host_url") {
            // The host's tunnel restarted with a new address: follow it silently.
            val hostId = d["hostKey"] ?: return
            val url = d["url"]?.takeIf { it.startsWith("https://") || it.startsWith("http://") } ?: return
            kotlinx.coroutines.runBlocking {
                g.prefs.update { s -> s.copy(hosts = s.hosts.map { if (it.id == hostId && it.url != url) it.copy(url = url) else it }) }
            }
            Log.i("PushService", "host $hostId moved to $url")
            return
        }
        val conn = g.connection(d["hostKey"]) ?: run {
            Log.w("PushService", "push for unknown host ${d["hostKey"]}")
            return
        }
        val n = AgentNotification(
            id = d["id"].orEmpty(),
            seq = d["seq"]?.toLongOrNull() ?: 0,
            sessionId = d["sessionId"],
            termId = d["termId"],
            kind = d["kind"].orEmpty(),
            title = d["title"].orEmpty(),
            body = d["body"].orEmpty(),
            reqId = d["reqId"],
            options = d["options"]?.let { runCatching { AppJson.decodeFromString(ListSerializer(PermOption.serializer()), it) }.getOrNull() } ?: emptyList(),
            ts = d["ts"]?.toLongOrNull() ?: System.currentTimeMillis(),
        )
        g.notifier.post(conn, n)
    }
}

suspend fun <T> Task<T>.awaitResult(): T? = suspendCancellableCoroutine { cont ->
    addOnCompleteListener { t -> cont.resume(if (t.isSuccessful) t.result else null) }
}

/** Registers this install's FCM token with every connected daemon. */
class PushRegistrar(private val graph: AppGraph) {
    private val _token = MutableStateFlow<String?>(null)
    val token: StateFlow<String?> = _token
    val lastError = MutableStateFlow<String?>(null)
    val status = MutableStateFlow<Map<String, JsonObject>>(emptyMap())

    suspend fun fetchToken(): String? {
        _token.value?.let { return it }
        val t = withTimeoutOrNull(20_000) { runCatching { FirebaseMessaging.getInstance().token.awaitResult() }.getOrNull() }
        if (t == null) lastError.value = "Couldn't get a Firebase token (Google Play services unavailable?)"
        _token.value = t
        return t
    }

    fun onNewToken(t: String) {
        _token.value = t
        graph.scope.launch { graph.connections.value.forEach { register(it) } }
    }

    suspend fun register(conn: HostConnection) {
        val events = graph.prefs.value.enabledPushEvents()
        if (!graph.prefs.value.notificationsEnabled || events.isEmpty()) {
            unregister(conn)
            return
        }
        val t = fetchToken() ?: return
        runCatching {
            val res = conn.rpc.call("push.register", params {
                put("token", t)
                put("hostKey", conn.config.id)
                put("device", graph.deviceName)
                put("events", kotlinx.serialization.json.JsonArray(events.map { kotlinx.serialization.json.JsonPrimitive(it) }))
            })
            status.value = status.value + (conn.config.id to (res as JsonObject))
        }.onFailure { lastError.value = it.message }
    }

    suspend fun unregister(conn: HostConnection) {
        val t = _token.value ?: return
        runCatching { conn.rpc.call("push.unregister", params { put("token", t) }) }
    }

    suspend fun test(conn: HostConnection): Result<Unit> = runCatching {
        val t = fetchToken() ?: error(lastError.value ?: "no FCM token")
        conn.rpc.call("push.test", params { put("token", t); put("hostKey", conn.config.id) })
        Unit
    }
}
