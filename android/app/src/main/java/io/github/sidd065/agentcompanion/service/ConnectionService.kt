package io.github.sidd065.agentcompanion.service

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.RemoteInput
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import androidx.lifecycle.LifecycleService
import androidx.lifecycle.lifecycleScope
import io.github.sidd065.agentcompanion.MainActivity
import io.github.sidd065.agentcompanion.R
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.ConnState
import io.github.sidd065.agentcompanion.net.params
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.launch
import kotlinx.serialization.json.put

/**
 * Foreground service that keeps the WebSocket(s) to the user's machines open so
 * agent notifications arrive while the app is closed (there's no push service
 * when push isn't set up).
 */
class ConnectionService : LifecycleService() {
    companion object {
        const val ONGOING_ID = 1
        fun intent(ctx: Context) = Intent(ctx, ConnectionService::class.java)
    }

    override fun onCreate() {
        super.onCreate()
        startAsForeground("Connecting…")
        val g = graph
        lifecycleScope.launch {
            @OptIn(ExperimentalCoroutinesApi::class)
            g.connections.flatMapLatest { conns ->
                if (conns.isEmpty()) flowOf("No machines configured")
                else combine(conns.map { c -> combine(c.rpc.state, c.sessions) { st, ss -> Triple(c, st, ss) } }) { arr ->
                    arr.joinToString(" · ") { (c, st, ss) ->
                        val running = ss.count { it.busy }
                        val waiting = ss.count { it.needsInput }
                        when (st) {
                            is ConnState.Connected -> buildString {
                                append(c.config.label)
                                if (running > 0) append(" · $running running")
                                if (waiting > 0) append(" · $waiting need you")
                                if (running == 0 && waiting == 0) append(" · connected")
                            }
                            is ConnState.Connecting -> "${c.config.label} · reconnecting"
                            is ConnState.Failed -> "${c.config.label} · ${st.error}"
                            ConnState.Idle -> "${c.config.label} · idle"
                        }
                    }
                }
            }.collect { text -> updateOngoing(text) }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        super.onStartCommand(intent, flags, startId)
        return START_STICKY
    }

    private fun ongoing(text: String) = NotificationCompat.Builder(this, Notifier.CH_CONNECTION)
        .setSmallIcon(R.drawable.ic_stat_agent)
        .setContentTitle("Agent Companion")
        .setContentText(text)
        .setOngoing(true)
        .setSilent(true)
        .setPriority(NotificationCompat.PRIORITY_MIN)
        .setContentIntent(
            PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE),
        )
        .build()

    private fun startAsForeground(text: String) {
        val type = if (Build.VERSION.SDK_INT >= 34) ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE else 0
        ServiceCompat.startForeground(this, ONGOING_ID, ongoing(text), type)
    }

    private fun updateOngoing(text: String) {
        runCatching {
            androidx.core.app.NotificationManagerCompat.from(this).notify(ONGOING_ID, ongoing(text))
        }
    }
}

/** Handles notification actions (approve/deny, reply, approve plan). */
class ActionReceiver : BroadcastReceiver() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    override fun onReceive(context: Context, intent: Intent) {
        val g = context.graph
        val hostId = intent.getStringExtra(Notifier.EXTRA_HOST)
        val sessionId = intent.getStringExtra(Notifier.EXTRA_SESSION) ?: return
        val notifId = intent.getIntExtra(Notifier.EXTRA_NOTIF_ID, 0)
        val action = intent.getStringExtra(Notifier.EXTRA_ACTION)
        val conn = g.connection(hostId) ?: return
        val pending = goAsync()
        scope.launch {
            try {
                when (action) {
                    "perm" -> {
                        conn.rpc.call("session.permission", params {
                            put("id", sessionId)
                            put("reqId", intent.getStringExtra(Notifier.EXTRA_REQ))
                            put("optionId", intent.getStringExtra(Notifier.EXTRA_OPTION))
                        })
                        g.notifier.cancel(notifId)
                    }
                    "reply" -> {
                        val text = RemoteInput.getResultsFromIntent(intent)?.getCharSequence(Notifier.KEY_REPLY)?.toString()
                        val req = intent.getStringExtra(Notifier.EXTRA_REQ)
                        val kind = intent.getStringExtra(Notifier.EXTRA_KIND)
                        if (!text.isNullOrBlank()) {
                            if (req != null && (kind == "question" || kind == "plan")) {
                                // A pending Claude question/plan: answer it (a plan reply sends it back with feedback).
                                conn.rpc.call("session.permission", params {
                                    put("id", sessionId); put("reqId", req); put("optionId", if (kind == "question") "answer" else "deny"); put("message", text)
                                })
                            } else {
                                conn.rpc.call("session.prompt", params { put("id", sessionId); put("text", text) })
                            }
                            g.notifier.replied(notifId, text)
                        }
                    }
                    "approve_plan" -> {
                        val req = intent.getStringExtra(Notifier.EXTRA_REQ)
                        val opt = intent.getStringExtra(Notifier.EXTRA_OPTION)
                        if (req != null && opt != null) {
                            conn.rpc.call("session.permission", params { put("id", sessionId); put("reqId", req); put("optionId", opt) })
                        } else {
                            conn.rpc.call("session.prompt", params { put("id", sessionId); put("text", "The plan looks good. Proceed with the implementation.") })
                        }
                        g.notifier.replied(notifId, "Plan approved")
                    }
                }
            } catch (e: Exception) {
                g.notifier.replied(notifId, "Failed: ${e.message}")
            } finally {
                pending.finish()
            }
        }
    }
}

/** Restarts the background connection after a reboot or app update. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val s = context.graph.prefs.value
        // Push (FCM) needs no service; only the opt-in persistent connection does.
        if (s.notificationsEnabled && s.backgroundConnection && s.hosts.isNotEmpty()) {
            runCatching { ContextCompat.startForegroundService(context, ConnectionService.intent(context)) }
        }
    }
}
