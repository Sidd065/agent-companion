package io.github.sidd065.agentcompanion

import android.app.Application
import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.os.Build
import androidx.core.content.ContextCompat
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import coil3.ImageLoader
import coil3.SingletonImageLoader
import coil3.network.okhttp.OkHttpNetworkFetcherFactory
import io.github.sidd065.agentcompanion.core.HostConfig
import io.github.sidd065.agentcompanion.core.Prefs
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.RemoteImageFetcher
import io.github.sidd065.agentcompanion.service.ConnectionService
import io.github.sidd065.agentcompanion.service.Notifier
import io.github.sidd065.agentcompanion.service.PushRegistrar
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import okhttp3.OkHttpClient
import java.util.concurrent.TimeUnit

class App : Application() {
    lateinit var graph: AppGraph
        private set

    override fun onCreate() {
        super.onCreate()
        graph = AppGraph(this)
        instance = this
        SingletonImageLoader.setSafe { ctx ->
            ImageLoader.Builder(ctx)
                .components {
                    add(RemoteImageFetcher.Factory(graph))
                    add(RemoteImageFetcher.Key())
                    add(OkHttpNetworkFetcherFactory(callFactory = { graph.http }))
                }
                .build()
        }
    }

    companion object {
        lateinit var instance: App
            private set
    }
}

val Context.graph: AppGraph get() = (applicationContext as App).graph

/** Process-wide singletons and connection lifecycle. */
class AppGraph(val app: Application) {
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    val prefs = Prefs(app, scope)
    val deviceName: String = "${Build.MANUFACTURER} ${Build.MODEL}".trim()

    val http: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(12, TimeUnit.SECONDS)
        .readTimeout(120, TimeUnit.SECONDS)
        .writeTimeout(120, TimeUnit.SECONDS)
        .pingInterval(20, TimeUnit.SECONDS)
        .retryOnConnectionFailure(true)
        .build()

    private val conns = HashMap<String, HostConnection>()
    private val _connections = MutableStateFlow<List<HostConnection>>(emptyList())
    val connections: StateFlow<List<HostConnection>> = _connections

    /** Session currently on screen (suppresses its notifications). */
    val visibleSession = MutableStateFlow<String?>(null)
    private val shown = ArrayList<Pair<Any, String>>()

    /**
     * Marks [sessionId] as on screen (no notifications for it) until the
     * returned function is called. Marks stack, so a screen opened on top
     * of the chat (side questions) and the chat being disposed after it
     * don't clear each other.
     */
    fun showSession(sessionId: String): () -> Unit {
        val token = Any()
        synchronized(shown) {
            shown.add(token to sessionId)
            visibleSession.value = sessionId
        }
        return {
            synchronized(shown) {
                shown.removeAll { it.first === token }
                visibleSession.value = shown.lastOrNull()?.second
            }
        }
    }
    val foreground = MutableStateFlow(false)
    val notifier = Notifier(app)
    val push = PushRegistrar(this)
    private var stopJob: Job? = null

    init {
        ProcessLifecycleOwner.get().lifecycle.addObserver(object : DefaultLifecycleObserver {
            override fun onStart(owner: LifecycleOwner) {
                foreground.value = true
            }

            override fun onStop(owner: LifecycleOwner) {
                foreground.value = false
            }
        })
        scope.launch {
            prefs.flow.collect { s -> syncConnections(s.hosts) }
        }
        scope.launch {
            prefs.flow.map { it.notificationsEnabled }.distinctUntilChanged().collect {
                connections.value.forEach { c -> if (c.rpc.isConnected) push.register(c) }
            }
        }
        scope.launch {
            // Notifications normally arrive through Firebase push; the persistent
            // WebSocket service is an opt-in fallback.
            combine(foreground, prefs.flow) { fg, s -> Triple(fg, s.notificationsEnabled && s.backgroundConnection, s.hosts.isNotEmpty()) }
                .distinctUntilChanged()
                .collect { (fg, bg, hasHosts) ->
                    if (hasHosts && bg) {
                        startService()
                        startAll()
                    } else if (fg) {
                        stopJob?.cancel()
                        startAll()
                        stopService()
                    } else {
                        stopService()
                        stopJob?.cancel()
                        stopJob = scope.launch {
                            delay(60_000)
                            conns.values.forEach { it.stop() }
                        }
                    }
                }
        }
        registerNetworkCallback()
    }

    @Synchronized
    private fun syncConnections(hosts: List<HostConfig>) {
        val keep = hosts.associateBy { it.id }
        for ((id, c) in conns.toMap()) {
            val h = keep[id]
            if (h == null || h.url != c.config.url || h.hostKey != c.config.hostKey || h.devicePrivWrapped != c.config.devicePrivWrapped) {
                c.close()
                conns.remove(id)
            }
        }
        for (h in hosts) {
            if (h.id !in conns) {
                val c = HostConnection(h, http, deviceName,
                    onNotifySeq = { hostId, seq -> scope.launch { prefs.update { s -> s.copy(lastNotifSeq = s.lastNotifSeq + (hostId to maxOf(seq, s.lastNotifSeq[hostId] ?: 0))) } } },
                    lastNotifSeq = { prefs.value.lastNotifSeq[h.id] ?: 0L },
                    onConnectedHook = { conn -> push.register(conn) },
                    flagsProvider = { agent -> prefs.value.flagsJson(agent) })
                conns[h.id] = c
                notifier.watch(c)
                if (foreground.value || prefs.value.backgroundConnection) c.start()
            }
        }
        _connections.value = hosts.mapNotNull { conns[it.id] }
    }

    fun connection(hostId: String?): HostConnection? = synchronized(this) {
        val id = hostId ?: prefs.value.activeHost?.id ?: return null
        conns[id]
    }

    fun activeConnection(): HostConnection? = connection(prefs.value.activeHost?.id)

    private fun startAll() = synchronized(this) { conns.values.forEach { it.start() } }

    fun reconnectAll() = synchronized(this) { conns.values.forEach { it.rpc.reconnectNow() } }

    @Volatile private var serviceStarted = false

    private fun startService() {
        if (serviceStarted) return
        serviceStarted = runCatching { ContextCompat.startForegroundService(app, ConnectionService.intent(app)) }.isSuccess
    }

    private fun stopService() {
        if (!serviceStarted) return
        serviceStarted = false
        runCatching { app.stopService(ConnectionService.intent(app)) }
    }

    private fun registerNetworkCallback() {
        val cm = app.getSystemService(ConnectivityManager::class.java) ?: return
        val req = NetworkRequest.Builder().addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET).build()
        runCatching {
            cm.registerNetworkCallback(req, object : ConnectivityManager.NetworkCallback() {
                override fun onAvailable(network: Network) = reconnectAll()
                override fun onLost(network: Network) = reconnectAll()
            })
        }
    }

}
