package io.github.sidd065.agentcompanion.core

import android.content.Context
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.floatPreferencesKey
import androidx.datastore.preferences.core.intPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.MapSerializer
import kotlinx.serialization.builtins.serializer

private val Context.dataStore by preferencesDataStore(name = "settings")

@Serializable
data class AppSettings(
    val hosts: List<HostConfig> = emptyList(),
    val activeHostId: String? = null,
    val notificationsEnabled: Boolean = true,
    val backgroundConnection: Boolean = false, // WebSocket fallback when push isn't available
    val theme: String = "system", // system|dark|light
    val dynamicColor: Boolean = false,
    val textScale: Float = 1f,
    val codeWrap: Boolean = false,
    val showThoughts: Boolean = true,
    val terminalFontSize: Int = 12,
    val defaultSkipPermissions: Boolean = true,
    val defaultModel: String = "",
    val defaultMode: String = "",
    val delivery: String = "", // ""=agy setting, queue, steer
    val enterSends: Boolean = false,
    val groupBy: String = "date", // date|folder|status|none
    val lastNotifSeq: Map<String, Long> = emptyMap(),
    val agyFlags: AgyFlags = AgyFlags(),
) {
    val activeHost: HostConfig? get() = hosts.firstOrNull { it.id == activeHostId } ?: hosts.firstOrNull()
}

/** DataStore-backed settings exposed as a StateFlow. */
class Prefs(private val context: Context, scope: CoroutineScope) {
    private object K {
        val hosts = stringPreferencesKey("hosts_v2") // paired hosts (v1 = Tailscale-era entries, dropped)
        val hostsV1 = stringPreferencesKey("hosts")
        val active = stringPreferencesKey("active_host")
        val notif = booleanPreferencesKey("notifications")
        val bgConn = booleanPreferencesKey("background_connection")
        val theme = stringPreferencesKey("theme")
        val dynamic = booleanPreferencesKey("dynamic_color")
        val textScale = floatPreferencesKey("text_scale")
        val codeWrap = booleanPreferencesKey("code_wrap")
        val thoughts = booleanPreferencesKey("show_thoughts")
        val termFont = intPreferencesKey("terminal_font")
        val skip = booleanPreferencesKey("default_skip")
        val model = stringPreferencesKey("default_model")
        val mode = stringPreferencesKey("default_mode")
        val delivery = stringPreferencesKey("delivery")
        val enterSends = booleanPreferencesKey("enter_sends")
        val groupBy = stringPreferencesKey("group_by")
        val notifSeq = stringPreferencesKey("notif_seq")
        val flags = stringPreferencesKey("agy_flags")
    }

    private val hostsSer = ListSerializer(HostConfig.serializer())
    private val seqSer = MapSerializer(String.serializer(), Long.serializer())

    private fun read(p: Preferences): AppSettings = AppSettings(
        hosts = p[K.hosts]?.let { runCatching { AppJson.decodeFromString(hostsSer, it) }.getOrNull() } ?: emptyList(),
        activeHostId = p[K.active],
        notificationsEnabled = p[K.notif] ?: true,
        backgroundConnection = p[K.bgConn] ?: false,
        theme = p[K.theme] ?: "system",
        dynamicColor = p[K.dynamic] ?: false,
        textScale = p[K.textScale] ?: 1f,
        codeWrap = p[K.codeWrap] ?: false,
        showThoughts = p[K.thoughts] ?: true,
        terminalFontSize = p[K.termFont] ?: 12,
        defaultSkipPermissions = p[K.skip] ?: true,
        defaultModel = p[K.model] ?: "",
        defaultMode = p[K.mode] ?: "",
        delivery = p[K.delivery] ?: "",
        enterSends = p[K.enterSends] ?: false,
        groupBy = p[K.groupBy] ?: "date",
        lastNotifSeq = p[K.notifSeq]?.let { runCatching { AppJson.decodeFromString(seqSer, it) }.getOrNull() } ?: emptyMap(),
        agyFlags = p[K.flags]?.let { runCatching { AppJson.decodeFromString(AgyFlags.serializer(), it) }.getOrNull() }
            // First run after the flags feature: carry over the older individual defaults.
            ?: AgyFlags(skipPermissions = p[K.skip] ?: true, model = p[K.model] ?: "", mode = p[K.mode] ?: ""),
    )

    val flow: StateFlow<AppSettings> = context.dataStore.data.map { read(it) }
        .stateIn(scope, SharingStarted.Eagerly, runBlocking { read(context.dataStore.data.first()) })

    val value: AppSettings get() = flow.value

    suspend fun update(block: (AppSettings) -> AppSettings) {
        context.dataStore.edit { p ->
            val n = block(read(p))
            p[K.hosts] = AppJson.encodeToString(hostsSer, n.hosts)
            p.remove(K.hostsV1)
            if (n.activeHostId != null) p[K.active] = n.activeHostId else p.remove(K.active)
            p[K.notif] = n.notificationsEnabled
            p[K.bgConn] = n.backgroundConnection
            p[K.theme] = n.theme
            p[K.dynamic] = n.dynamicColor
            p[K.textScale] = n.textScale
            p[K.codeWrap] = n.codeWrap
            p[K.thoughts] = n.showThoughts
            p[K.termFont] = n.terminalFontSize
            p[K.skip] = n.defaultSkipPermissions
            p[K.model] = n.defaultModel
            p[K.mode] = n.defaultMode
            p[K.delivery] = n.delivery
            p[K.enterSends] = n.enterSends
            p[K.groupBy] = n.groupBy
            p[K.notifSeq] = AppJson.encodeToString(seqSer, n.lastNotifSeq)
            p[K.flags] = AppJson.encodeToString(AgyFlags.serializer(), n.agyFlags)
        }
    }
}
