package io.github.sidd065.agentcompanion.service

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.RemoteInput
import androidx.core.content.ContextCompat
import io.github.sidd065.agentcompanion.MainActivity
import io.github.sidd065.agentcompanion.R
import io.github.sidd065.agentcompanion.core.AgentNotification
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.HostConnection
import kotlinx.coroutines.launch

/** Posts Android notifications for daemon events. */
class Notifier(private val ctx: Context) {
    companion object {
        const val CH_SESSIONS = "sessions"
        const val CH_PERMISSIONS = "permissions"
        const val CH_QUESTIONS = "questions"
        const val CH_TERMINALS = "terminals"
        const val CH_CONNECTION = "connection"
        const val KEY_REPLY = "reply"
        const val EXTRA_HOST = "host"
        const val EXTRA_SESSION = "session"
        const val EXTRA_TERM = "term"
        const val EXTRA_REQ = "req"
        const val EXTRA_OPTION = "option"
        const val EXTRA_NOTIF_ID = "notif_id"
        const val EXTRA_ACTION = "action"
        const val EXTRA_KIND = "kind"
    }

    private val nm = NotificationManagerCompat.from(ctx)
    // The same event can arrive over the WebSocket and through FCM: show it once.
    private val seen = object : LinkedHashMap<String, Boolean>(64, 0.75f, false) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, Boolean>?) = size > 200
    }

    init {
        val mgr = ctx.getSystemService(NotificationManager::class.java)
        mgr.createNotificationChannels(
            listOf(
                NotificationChannel(CH_SESSIONS, "Session updates", NotificationManager.IMPORTANCE_DEFAULT).apply {
                    description = "An agent finished a turn or hit an error"
                },
                NotificationChannel(CH_PERMISSIONS, "Permission requests", NotificationManager.IMPORTANCE_HIGH).apply {
                    description = "An agent is waiting for you to approve a tool call"
                },
                NotificationChannel(CH_QUESTIONS, "Questions & plans", NotificationManager.IMPORTANCE_HIGH).apply {
                    description = "An agent asked a question or wants a plan reviewed"
                },
                NotificationChannel(CH_TERMINALS, "Terminal sessions", NotificationManager.IMPORTANCE_DEFAULT).apply {
                    description = "An agent running in a shared terminal finished or is waiting"
                },
                NotificationChannel(CH_CONNECTION, "Background connection", NotificationManager.IMPORTANCE_MIN).apply {
                    description = "Keeps an encrypted connection to your computers open"
                    setShowBadge(false)
                },
            ),
        )
    }

    fun watch(conn: HostConnection) {
        conn.scope.launch {
            conn.notifications.collect { n -> post(conn, n) }
        }
    }

    private fun allowed(): Boolean =
        android.os.Build.VERSION.SDK_INT < 33 ||
            ContextCompat.checkSelfPermission(ctx, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED

    fun notifId(hostId: String, key: String): Int = (hostId + "/" + key).hashCode()

    fun cancelFor(hostId: String, sessionId: String) {
        nm.cancel(notifId(hostId, "s:$sessionId"))
        nm.cancel(notifId(hostId, "p:$sessionId"))
    }

    fun cancelAside(hostId: String, sessionId: String) = nm.cancel(notifId(hostId, "a:$sessionId"))

    private fun openIntent(hostId: String, sessionId: String?, termId: String?, reqCode: Int, kind: String? = null): PendingIntent {
        val i = Intent(ctx, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP
            putExtra(EXTRA_HOST, hostId)
            sessionId?.let { putExtra(EXTRA_SESSION, it) }
            termId?.let { putExtra(EXTRA_TERM, it) }
            kind?.let { putExtra(EXTRA_KIND, it) }
        }
        return PendingIntent.getActivity(ctx, reqCode, i, PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
    }

    private fun actionIntent(hostId: String, n: AgentNotification, action: String, notifId: Int, option: String? = null, mutable: Boolean = false): PendingIntent {
        val i = Intent(ctx, ActionReceiver::class.java).apply {
            putExtra(EXTRA_ACTION, action)
            putExtra(EXTRA_HOST, hostId)
            putExtra(EXTRA_SESSION, n.sessionId)
            putExtra(EXTRA_REQ, n.reqId)
            putExtra(EXTRA_KIND, n.kind)
            putExtra(EXTRA_OPTION, option)
            putExtra(EXTRA_NOTIF_ID, notifId)
        }
        val flags = PendingIntent.FLAG_UPDATE_CURRENT or (if (mutable) PendingIntent.FLAG_MUTABLE else PendingIntent.FLAG_IMMUTABLE)
        return PendingIntent.getBroadcast(ctx, (notifId.toString() + action + option).hashCode(), i, flags)
    }

    fun post(conn: HostConnection, n0: AgentNotification) {
        val g = ctx.graph
        val dedupKey = conn.config.id + "/" + n0.id.ifEmpty { "${n0.kind}:${n0.sessionId}:${n0.termId}:${n0.seq}" }
        synchronized(seen) {
            if (seen.containsKey(dedupKey)) return
            seen[dedupKey] = true
        }
        if (!g.prefs.value.notificationsEnabled) return
        if (g.foreground.value && n0.sessionId != null && g.visibleSession.value == n0.sessionId) return
        if (!allowed()) return
        // "minimal" push content: fill in what we know locally.
        val known = n0.sessionId?.let { conn.session(it) }
        val n = n0.copy(
            title = n0.title.ifBlank { known?.displayTitle ?: if (n0.termId != null) "Terminal" else "Agent" },
            body = n0.body.ifBlank {
                when (n0.kind) {
                    "permission" -> "A tool call is waiting for your approval"
                    "question" -> "The agent has a question for you"
                    "plan" -> "A plan is ready for review"
                    "error" -> "The session hit an error"
                    "term_done" -> "The agent finished"
                    "term_blocked" -> "The agent is waiting for you"
                    "test" -> "Push notifications are working"
                    "aside" -> "Your side question has an answer"
                    else -> known?.lastText ?: "Turn finished"
                }
            }.let { body ->
                when (n0.kind) {
                    // Agent text is Markdown; commands and terminal output are not.
                    "done", "question", "plan", "error", "aside" -> io.github.sidd065.agentcompanion.ui.components.markdownToPlain(body)
                    // The title already names the session: "<tool> — <session>" → "<tool>".
                    "permission" -> listOfNotNull(known?.title, known?.displayTitle).fold(body) { b, t -> b.removeSuffix(" — $t") }
                    else -> body
                }
            },
        )
        val hostId = conn.config.id
        val hostLabel = conn.config.label
        val multiHost = g.prefs.value.hosts.size > 1
        val (channel, key) = when (n.kind) {
            "permission" -> CH_PERMISSIONS to "p:${n.sessionId}"
            "question", "plan" -> CH_QUESTIONS to "s:${n.sessionId}"
            "term_done", "term_blocked", "term_failed" -> CH_TERMINALS to "t:${n.termId}"
            "test" -> CH_SESSIONS to "test"
            "aside" -> CH_SESSIONS to "a:${n.sessionId}"
            else -> CH_SESSIONS to "s:${n.sessionId}"
        }
        val id = notifId(hostId, key)
        val title = when (n.kind) {
            "permission" -> "Permission needed · ${conn.session(n.sessionId ?: "")?.displayTitle ?: n.title}"
            "question" -> "Question · ${n.title}"
            "plan" -> "Plan ready · ${n.title}"
            "error" -> "Error · ${n.title}"
            "term_blocked" -> "Waiting · ${n.title}"
            "term_failed" -> "Failed · ${n.title}"
            else -> n.title
        }
        val b = NotificationCompat.Builder(ctx, channel)
            .setSmallIcon(R.drawable.ic_stat_agent)
            .setContentTitle(title)
            .setContentText(n.body)
            .setStyle(NotificationCompat.BigTextStyle().bigText(n.body))
            .setAutoCancel(true)
            .setWhen(if (n.ts > 0) n.ts else System.currentTimeMillis())
            .setShowWhen(true)
            .setContentIntent(openIntent(hostId, n.sessionId, n.termId, id, n.kind))
            .setCategory(if (channel == CH_PERMISSIONS) NotificationCompat.CATEGORY_CALL else NotificationCompat.CATEGORY_MESSAGE)
            .setPriority(if (channel == CH_PERMISSIONS || channel == CH_QUESTIONS) NotificationCompat.PRIORITY_HIGH else NotificationCompat.PRIORITY_DEFAULT)
        if (multiHost) b.setSubText(hostLabel)
        if (n.kind == "permission" && n.reqId != null) {
            n.options.take(3).forEach { o ->
                b.addAction(0, o.name.ifBlank { o.optionId }, actionIntent(hostId, n, "perm", id, o.optionId))
            }
        }
        if (n.sessionId != null && n.kind in setOf("done", "question", "plan", "error")) {
            val ri = RemoteInput.Builder(KEY_REPLY).setLabel(if (n.kind == "question") "Your answer" else "Reply to agent").build()
            val replyAction = NotificationCompat.Action.Builder(0, "Reply", actionIntent(hostId, n, "reply", id, mutable = true))
                .addRemoteInput(ri)
                .setAllowGeneratedReplies(false)
                .build()
            b.addAction(replyAction)
            if (n.kind == "plan") {
                // Claude plans are a pending request: approve it with the first "yes" option.
                val opt = n.options.firstOrNull { it.optionId.startsWith("plan_") }?.optionId
                b.addAction(0, "Approve plan", actionIntent(hostId, n, "approve_plan", id, opt))
            }
        }
        runCatching { nm.notify(id, b.build()) }
    }

    fun replied(id: Int, text: String) {
        if (!allowed()) return
        val b = NotificationCompat.Builder(ctx, CH_SESSIONS)
            .setSmallIcon(R.drawable.ic_stat_agent)
            .setContentTitle("Sent")
            .setContentText(text)
            .setTimeoutAfter(4000)
        runCatching { nm.notify(id, b.build()) }
    }

    fun cancel(id: Int) = nm.cancel(id)
}
