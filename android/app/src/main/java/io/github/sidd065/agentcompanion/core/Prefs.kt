package io.github.sidd065.agentcompanion.core

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.floatPreferencesKey
import androidx.datastore.preferences.core.intPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
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
    val claudeFlags: ClaudeFlags = ClaudeFlags(),
    val codexFlags: CodexFlags = CodexFlags(),
    val customCodexModels: List<CustomModel> = emptyList(),
    val recentCodexModels: List<String> = emptyList(),
    /** Agent preselected for new sessions and terminals. */
    val defaultAgent: String = "agy",
) {
    val activeHost: HostConfig? get() = hosts.firstOrNull { it.id == activeHostId } ?: hosts.firstOrNull()

    /** The saved launch flags for an agent, as the daemon's "flags" object. */
    fun flagsJson(agent: String?): kotlinx.serialization.json.JsonObject = when {
        agent.isNullOrBlank() || agent == "agy" -> agyFlags.daemonJson()
        agent == "claude" -> claudeFlags.daemonJson()
        agent == "codex" -> codexFlags.daemonJson()
        else -> kotlinx.serialization.json.buildJsonObject {}
    }

    fun addDirs(agent: String?): List<String> = when {
        agent.isNullOrBlank() || agent == "agy" -> agyFlags.addDirs
        agent == "claude" -> claudeFlags.addDirs
        agent == "codex" -> codexFlags.addDirs
        else -> emptyList()
    }

    /** Terminal arguments for continuing a conversation of this agent. */
    fun tuiResumeArgs(agent: String?): List<String> = when {
        agent.isNullOrBlank() || agent == "agy" -> agyFlags.tuiResumeArgs()
        agent == "claude" -> claudeFlags.tuiResumeArgs()
        agent == "codex" -> codexFlags.tuiResumeArgs()
        else -> emptyList()
    }

    fun skipPermissions(agent: String?): Boolean = when {
        agent.isNullOrBlank() || agent == "agy" -> agyFlags.skipPermissions
        agent == "claude" -> claudeFlags.skipPermissions
        agent == "codex" -> codexFlags.effectiveSkipPermissions
        else -> false
    }

    fun withRecentCodexModel(slug: String, customName: String? = null, isCustom: Boolean = false): AppSettings {
        val clean = slug.trim()
        if (clean.isEmpty()) return this
        val recents = (listOf(clean) + recentCodexModels.filterNot { it == clean }).take(12)
        val customs = if (isCustom) {
            val entry = CustomModel(slug = clean, name = customName?.trim()?.ifBlank { clean } ?: clean)
            listOf(entry) + customCodexModels.filterNot { it.slug == clean }
        } else {
            customCodexModels
        }
        return copy(recentCodexModels = recents, customCodexModels = customs)
    }

    fun withoutCustomCodexModel(slug: String): AppSettings {
        val clean = slug.trim()
        if (clean.isEmpty()) return this
        return copy(
            customCodexModels = customCodexModels.filterNot { it.slug == clean },
            recentCodexModels = recentCodexModels.filterNot { it == clean },
        )
    }
}

/** DataStore-backed settings exposed as a StateFlow. */
class Prefs(
    private val context: Context,
    scope: CoroutineScope,
    private val store: DataStore<Preferences> = context.dataStore,
) {
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
        val claudeFlags = stringPreferencesKey("claude_flags")
        val codexFlags = stringPreferencesKey("codex_flags")
        val customCodexModels = stringPreferencesKey("custom_codex_models")
        val recentCodexModels = stringPreferencesKey("recent_codex_models")
        val defaultAgent = stringPreferencesKey("default_agent")
    }

    private val hostsSer = ListSerializer(HostConfig.serializer())
    private val seqSer = MapSerializer(String.serializer(), Long.serializer())
    private val customModelsSer = ListSerializer(CustomModel.serializer())
    private val stringListSer = ListSerializer(String.serializer())

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
        claudeFlags = p[K.claudeFlags]?.let { runCatching { AppJson.decodeFromString(ClaudeFlags.serializer(), it) }.getOrNull() } ?: ClaudeFlags(),
        codexFlags = p[K.codexFlags]?.let { runCatching { AppJson.decodeFromString(CodexFlags.serializer(), it) }.getOrNull() } ?: CodexFlags(),
        customCodexModels = p[K.customCodexModels]?.let { runCatching { AppJson.decodeFromString(customModelsSer, it) }.getOrNull() } ?: emptyList(),
        recentCodexModels = p[K.recentCodexModels]?.let { runCatching { AppJson.decodeFromString(stringListSer, it) }.getOrNull() } ?: emptyList(),
        defaultAgent = p[K.defaultAgent] ?: "agy",
    )

    private val _flow = MutableStateFlow(runBlocking { read(store.data.first()) })
    val flow: StateFlow<AppSettings> = _flow

    val value: AppSettings get() = _flow.value

    init {
        scope.launch {
            store.data.collect { _flow.value = read(it) }
        }
    }

    suspend fun update(block: (AppSettings) -> AppSettings) {
        val updated = store.edit { p ->
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
            p[K.claudeFlags] = AppJson.encodeToString(ClaudeFlags.serializer(), n.claudeFlags)
            p[K.codexFlags] = AppJson.encodeToString(CodexFlags.serializer(), n.codexFlags)
            p[K.customCodexModels] = AppJson.encodeToString(customModelsSer, n.customCodexModels)
            p[K.recentCodexModels] = AppJson.encodeToString(stringListSer, n.recentCodexModels)
            p[K.defaultAgent] = n.defaultAgent
        }
        _flow.value = read(updated)
    }
}
