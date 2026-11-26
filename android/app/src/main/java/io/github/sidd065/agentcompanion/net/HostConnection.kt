package io.github.sidd065.agentcompanion.net

import android.util.Log
import androidx.compose.runtime.mutableStateListOf
import io.github.sidd065.agentcompanion.core.AgentNotification
import io.github.sidd065.agentcompanion.core.Aside
import io.github.sidd065.agentcompanion.core.AgentProfile
import io.github.sidd065.agentcompanion.core.AppJson
import io.github.sidd065.agentcompanion.core.HostConfig
import io.github.sidd065.agentcompanion.core.Item
import io.github.sidd065.agentcompanion.core.Op
import io.github.sidd065.agentcompanion.core.SessionExtras
import io.github.sidd065.agentcompanion.core.SessionSummary
import io.github.sidd065.agentcompanion.BuildConfig
import io.github.sidd065.agentcompanion.core.CustomModel
import io.github.sidd065.agentcompanion.core.DeviceKeys
import io.github.sidd065.agentcompanion.core.Project
import io.github.sidd065.agentcompanion.core.ProviderConfig
import io.github.sidd065.agentcompanion.core.ProviderModelsResult
import io.github.sidd065.agentcompanion.core.ProvidersListResult
import io.github.sidd065.agentcompanion.core.Term
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.serialization.KSerializer
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import kotlinx.serialization.json.put
import okhttp3.OkHttpClient
import java.util.concurrent.ConcurrentHashMap

fun <T> JsonElement.decode(ser: KSerializer<T>): T = AppJson.decodeFromJsonElement(ser, this)

inline fun params(block: kotlinx.serialization.json.JsonObjectBuilder.() -> Unit): JsonObject = buildJsonObject(block)

/** Everything the app knows about one daemon (one machine). */
class HostConnection(
    val config: HostConfig,
    private val http: OkHttpClient,
    deviceName: String,
    private val onNotifySeq: (hostId: String, seq: Long) -> Unit,
    private val lastNotifSeq: () -> Long,
    private val onConnectedHook: suspend (HostConnection) -> Unit = {},
    val flagsProvider: (agent: String) -> JsonObject? = { null },
    private val keyPairProvider: (HostConfig) -> Noise.KeyPair = DeviceKeys::keyPair,
    private val onHostUrl: (hostId: String, url: String) -> Unit = { _, _ -> },
) {
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    val rpc = RpcClient(config, http, scope, deviceName, keyPairProvider)
    private val deviceName = deviceName

    private val _sessions = MutableStateFlow<List<SessionSummary>>(emptyList())
    val sessions: StateFlow<List<SessionSummary>> = _sessions
    val sessionsLoaded = MutableStateFlow(false)
    private val _terms = MutableStateFlow<List<Term>>(emptyList())
    val terms: StateFlow<List<Term>> = _terms
    private val _hello = MutableStateFlow<JsonObject?>(null)
    val hello: StateFlow<JsonObject?> = _hello
    private val _agents = MutableStateFlow<List<AgentProfile>>(emptyList())
    val agents: StateFlow<List<AgentProfile>> = _agents
    private val _projects = MutableStateFlow<List<Project>>(emptyList())
    val projects: StateFlow<List<Project>> = _projects
    private val _providerModels = MutableStateFlow<Map<String, ProviderModelsResult>>(emptyMap())
    val providerModels: StateFlow<Map<String, ProviderModelsResult>> = _providerModels
    private val _notifications = MutableSharedFlow<AgentNotification>(extraBufferCapacity = 64)
    val notifications: SharedFlow<AgentNotification> = _notifications

    private val stores = ConcurrentHashMap<String, SessionStore>()

    init {
        scope.launch { rpc.events.collect { ev -> runCatching { onEvent(ev) }.onFailure { Log.w("Host", "event ${ev.name}: $it") } } }
        scope.launch {
            rpc.state.collect { st ->
                if (st is ConnState.Connected) onConnected(st.hello)
            }
        }
    }

    fun start() = rpc.start()
    fun stop() = rpc.stop()
    fun close() {
        rpc.stop()
        scope.cancel()
    }

    fun store(id: String): SessionStore = stores.getOrPut(id) { SessionStore(id, this) }

    private suspend fun onConnected(hello: JsonObject) {
        _hello.value = hello
        hello["agents"]?.let { a -> runCatching { _agents.value = a.decode(ListSerializer(AgentProfile.serializer())) } }
        hello["sessions"]?.let { s ->
            runCatching { _sessions.value = s.decode(ListSerializer(SessionSummary.serializer())) }
            sessionsLoaded.value = true
        }
        for (st in stores.values) {
            st.summaryFrom(_sessions.value.firstOrNull { it.id == st.id })
            if (st.isAttached) st.subscribe()
            if (st.asidesLoaded) scope.launch { runCatching { st.refreshAsides() } }
        }
        _providerModels.value = emptyMap()
        if (_agents.value.any { it.id == "codex" && it.available }) {
            scope.launch { runCatching { fetchProviderModels("codex", refresh = false) } }
        }
        scope.launch { refreshTerms() }
        scope.launch { runCatching { refreshProjects() } }
        scope.launch { catchUpNotifications() }
        scope.launch { runCatching { onConnectedHook(this@HostConnection) } }
    }

    private suspend fun catchUpNotifications() {
        val last = lastNotifSeq()
        val res = runCatching { rpc.call("notifications.since", params { put("seq", last) }) }.getOrNull() ?: return
        val list = runCatching { res.decode(ListSerializer(AgentNotification.serializer())) }.getOrNull() ?: return
        if (list.isEmpty()) return
        val maxSeq = list.maxOf { it.seq }
        if (last > 0 && maxSeq >= last) {
            val cutoff = System.currentTimeMillis() - 30 * 60_000
            list.filter { it.seq > last && it.ts > cutoff }.forEach { _notifications.emit(it) }
        }
        onNotifySeq(config.id, maxSeq)
    }

    private suspend fun onEvent(ev: ServerEvent) {
        when (ev.name) {
            "session.changed" -> {
                val s = ev.data.decode(SessionSummary.serializer())
                upsertSession(s)
                stores[s.id]?.let { st -> withContext(Dispatchers.Main) { st.summaryFrom(s) } }
            }
            "session.removed" -> {
                val id = ev.data.jsonObject["id"]?.jsonPrimitive?.content ?: return
                _sessions.value = _sessions.value.filterNot { it.id == id }
            }
            "sessions.invalidate" -> refreshSessions(false)
            "session.snapshot", "session.delta", "session.ops", "session.config", "session.usage" -> {
                val obj = ev.data.jsonObject
                val id = obj["id"]?.jsonPrimitive?.content ?: return
                val st = stores[id] ?: return
                val parsed = SessionStore.parse(ev.name, obj)
                withContext(Dispatchers.Main) { st.apply(parsed) }
                parsed.summary?.let { upsertSession(it) }
            }
            "session.aside" -> {
                val obj = ev.data.jsonObject
                val st = stores[obj["id"]?.jsonPrimitive?.content ?: return] ?: return
                val a = obj["aside"]?.decode(Aside.serializer()) ?: return
                withContext(Dispatchers.Main) { st.onAside(a, fromEvent = true) }
            }
            "session.asides" -> {
                val obj = ev.data.jsonObject
                val st = stores[obj["id"]?.jsonPrimitive?.content ?: return] ?: return
                val list = obj["asides"]?.takeIf { it !is JsonNull }?.decode(ListSerializer(Aside.serializer())) ?: emptyList()
                withContext(Dispatchers.Main) { st.setAsides(list) }
            }
            "notify" -> {
                val n = ev.data.decode(AgentNotification.serializer())
                onNotifySeq(config.id, n.seq)
                _notifications.emit(n)
            }
            "host.url" -> {
                val url = ev.data.jsonObject["url"]?.jsonPrimitive?.content
                    ?.takeIf { it.startsWith("https://") || it.startsWith("http://") }
                if (url != null) onHostUrl(config.id, url)
            }
            "terms.changed" -> {
                runCatching { _terms.value = ev.data.decode(ListSerializer(Term.serializer())) }
            }
            "projects.changed" -> {
                runCatching { _projects.value = ev.data.decode(ListSerializer(Project.serializer())) }
            }
            "providers.changed" -> {
                runCatching { _agents.value = ev.data.decode(ListSerializer(AgentProfile.serializer())) }
                _providerModels.value = emptyMap()
                if (_agents.value.any { it.id == "codex" && it.available }) {
                    scope.launch { runCatching { fetchProviderModels("codex", refresh = false) } }
                }
            }
        }
    }

    private fun upsertSession(s: SessionSummary) {
        val cur = _sessions.value
        val i = cur.indexOfFirst { it.id == s.id }
        _sessions.value = if (i >= 0) cur.toMutableList().also { it[i] = s } else listOf(s) + cur
    }

    suspend fun refreshSessions(force: Boolean) {
        val res = rpc.call("sessions.list", params { put("force", force) })
        _sessions.value = res.decode(ListSerializer(SessionSummary.serializer()))
        sessionsLoaded.value = true
    }

    suspend fun refreshTerms() {
        val res = rpc.call("term.list")
        _terms.value = res.decode(ListSerializer(Term.serializer()))
    }

    suspend fun refreshProjects() {
        val res = rpc.call("projects.list")
        _projects.value = res.decode(ListSerializer(Project.serializer()))
    }

    val protocolVersion: Int
        get() = _hello.value?.get("protocolVersion")?.jsonPrimitive?.content?.toIntOrNull() ?: 1

    fun hasCapability(cap: String): Boolean {
        val arr = _hello.value?.get("capabilities") as? kotlinx.serialization.json.JsonArray ?: return false
        return arr.any { it.jsonPrimitive.content == cap }
    }

    val openRouterConfigured: Boolean
        get() = _hello.value?.get("openRouterConfigured")?.jsonPrimitive?.booleanOrNull == true ||
            _providerModels.value["codex"]?.openRouterConfigured == true

    fun agentHasFeature(agent: String?, feature: String): Boolean {
        val id = agent?.ifBlank { "agy" } ?: "agy"
        val profile = _agents.value.firstOrNull { it.id == id }
        if (profile != null && profile.features.isNotEmpty()) {
            return feature in profile.features
        }
        return when (id) {
            "agy" -> feature in setOf("fork", "rewind", "terminal", "modes", "effort")
            "claude" -> feature in setOf("fork", "rewind", "terminal", "steer", "asides", "sideQuestion", "effort", "modes")
            "codex" -> feature in setOf(
                "fork", "rewind", "terminal", "steer", "asides", "sideQuestion",
                "effort", "modes", "openrouter", "customModels", "thinking", "diffs",
                "permissions", "questions", "plans",
            )
            else -> false
        }
    }

    suspend fun fetchProviderModels(provider: String = "codex", refresh: Boolean = false): ProviderModelsResult {
        if (!refresh) {
            _providerModels.value[provider]?.takeIf { it.models.isNotEmpty() || it.customModels.isNotEmpty() }?.let { return it }
        }
        val res = rpc.call(
            "providers.models",
            params { put("provider", provider); if (refresh) put("refresh", true) },
            timeoutMs = 60_000,
        ).decode(ProviderModelsResult.serializer())
        _providerModels.value = _providerModels.value + (provider to res)
        return res
    }

    /**
     * Updates customModels (and optional defaultModel) on the daemon via `providers.upsert`
     * while strictly preserving all existing env secrets (`"***"` or `env = null`).
     */
    suspend fun saveProviderCustomModels(
        providerId: String = "codex",
        customModels: List<CustomModel>,
        defaultModel: String? = null,
    ): ProviderModelsResult {
        val list = rpc.call("providers.list").decode(ProvidersListResult.serializer())
        val existing = list.configs.firstOrNull { it.id == providerId }
        val agentProf = list.agents.firstOrNull { it.id == providerId }
        val cleanedCustoms = customModels
            .map { CustomModel(slug = it.slug.trim(), name = it.name.trim()) }
            .filter { it.slug.isNotEmpty() }
            .distinctBy { it.slug }
        val merged = if (existing != null) {
            existing.copy(
                customModels = cleanedCustoms,
                defaultModel = defaultModel ?: existing.defaultModel,
                // CRITICAL: keep existing.env untouched so "***" secret values are preserved by daemon!
            )
        } else {
            ProviderConfig(
                id = providerId,
                name = agentProf?.name?.ifBlank { "Codex" } ?: "Codex",
                driver = agentProf?.driver?.ifBlank { providerId } ?: providerId,
                enabled = true,
                env = null, // null preserves any prior env in providers.go
                defaultModel = defaultModel.orEmpty(),
                customModels = cleanedCustoms,
                builtin = agentProf?.builtin ?: (providerId == "codex"),
                features = agentProf?.features.orEmpty(),
            )
        }
        val payload = AppJson.encodeToJsonElement(ProviderConfig.serializer(), merged).jsonObject
        rpc.call("providers.upsert", payload)
        _providerModels.value = _providerModels.value - providerId
        return fetchProviderModels(providerId, refresh = false)
    }

    fun session(id: String): SessionSummary? = _sessions.value.firstOrNull { it.id == id }

    // ---- encrypted side channels & file transfer (all inside the secure channel) ----

    /** Opens another encrypted connection (used for terminals). */
    fun openSocket(hello: JsonObject, listener: SecureSocket.Listener): SecureSocket {
        val full = buildJsonObject {
            put("v", 1); put("name", deviceName); put("platform", "android"); put("app", BuildConfig.VERSION_NAME)
            hello.forEach { (k, v) -> put(k, v) }
        }
        return SecureSocket(http, config.url, DeviceKeys.unb64url(config.hostKey), keyPairProvider(config), full, listener).also { it.connect() }
    }

    /** Streams a remote file into [out] in 1 MiB chunks; returns the size. */
    suspend fun download(path: String, out: java.io.OutputStream, onProgress: (done: Long, total: Long) -> Unit = { _, _ -> }): Long =
        readChunks("fs.download", "path", path, out, onProgress)

    suspend fun readFileBytes(path: String, max: Long = 64L shl 20): ByteArray {
        val bos = java.io.ByteArrayOutputStream()
        val size = download(path, bos) { done, _ -> if (done > max) throw RpcException("file is too large to preview") }
        if (size > max) throw RpcException("file is too large to preview")
        return bos.toByteArray()
    }

    suspend fun readAttachment(ref: String): ByteArray {
        val bos = java.io.ByteArrayOutputStream()
        readChunks("attachment.read", "ref", ref, bos) { _, _ -> }
        return bos.toByteArray()
    }

    private suspend fun readChunks(method: String, key: String, value: String, out: java.io.OutputStream, onProgress: (Long, Long) -> Unit): Long =
        ChunkTransfer.readChunks(out = out, onProgress = onProgress) { offset, length ->
            rpc.call(method, params { put(key, value); put("offset", offset); put("length", length) }, timeoutMs = 120_000).jsonObject
        }

    /** Uploads [input] to [path] in chunks (atomic: the file appears when complete). */
    suspend fun upload(path: String, input: java.io.InputStream, overwrite: Boolean, onProgress: (Long) -> Unit = {}): JsonObject {
        val id = rpc.call("fs.uploadStart", params { put("path", path); put("overwrite", overwrite); put("mkdirs", true) })
            .jsonObject["id"]!!.jsonPrimitive.content
        try {
            val buf = ByteArray(512 * 1024)
            var off = 0L
            while (true) {
                var n = 0
                while (n < buf.size) {
                    val r = input.read(buf, n, buf.size - n)
                    if (r < 0) break
                    n += r
                }
                if (n == 0) break
                rpc.call("fs.uploadChunk", params {
                    put("id", id); put("offset", off)
                    put("data", android.util.Base64.encodeToString(buf, 0, n, android.util.Base64.NO_WRAP))
                })
                off += n
                onProgress(off)
                if (n < buf.size) break
            }
            return rpc.call("fs.uploadFinish", params { put("id", id) }).jsonObject
        } catch (e: Exception) {
            runCatching { rpc.call("fs.uploadAbort", params { put("id", id) }) }
            throw e
        }
    }
}

/** The follow-up context agentc adds itself (see asidePrompt in the daemon), for older versions. */
internal fun legacyAsidePrompt(list: List<Aside>, question: String, followUp: String?): String {
    val chain = ArrayList<Aside>()
    var cur = followUp
    while (!cur.isNullOrEmpty() && chain.size < 6) {
        val prev = list.firstOrNull { it.id == cur && it.state == "done" } ?: break
        chain.add(0, prev)
        cur = prev.followUp
    }
    if (chain.isEmpty()) return question
    return buildString {
        append("This continues an earlier side conversation about this session:\n")
        chain.forEach { append("\nQuestion: ").append(it.question).append("\n\nYour answer:\n").append(it.answer).append('\n') }
        append("\nFollow-up question: ").append(question)
    }
}

/** Parsed transcript event. */
data class StoreEvent(
    val kind: String,
    val summary: SessionSummary? = null,
    val extras: SessionExtras? = null,
    val seq: Long = 0,
    val items: List<Item>? = null,
    val ops: List<Op>? = null,
    val used: Long = -1,
    val size: Long = -1,
)

/** Live transcript of one session, mirrored from the daemon. */
class SessionStore(val id: String, private val conn: HostConnection) {
    val items = mutableStateListOf<Item>()
    private val index = HashMap<String, Int>()
    var seq: Long = 0
        private set
    val summary = MutableStateFlow<SessionSummary?>(conn.session(id))
    val extras = MutableStateFlow(SessionExtras())
    val loaded = MutableStateFlow(false)
    private var attachCount = 0
    val isAttached: Boolean get() = attachCount > 0

    fun attach() {
        attachCount++
        if (attachCount == 1) subscribe()
    }

    fun detach() {
        attachCount = (attachCount - 1).coerceAtLeast(0)
        if (attachCount == 0) {
            conn.scope.launch { runCatching { conn.rpc.call("session.unsubscribe", params { put("id", id) }) } }
        }
    }

    fun subscribe() {
        val since = if (loaded.value) seq else 0L
        conn.scope.launch {
            runCatching {
                conn.rpc.call("session.subscribe", params {
                    put("id", id); put("since", since)
                    summary.value?.agent?.takeIf { it.isNotBlank() }
                        ?.let { conn.flagsProvider(it) }
                        ?.takeIf { it.isNotEmpty() }
                        ?.let { put("flags", it) }
                })
            }
                .onFailure { Log.w("SessionStore", "subscribe $id: ${it.message}") }
        }
    }

    fun summaryFrom(s: SessionSummary?) {
        if (s != null) summary.value = s
    }

    private fun reindex() {
        index.clear()
        items.forEachIndexed { i, it -> index[it.id] = i }
    }

    fun apply(ev: StoreEvent) {
        ev.summary?.let { summary.value = it }
        ev.extras?.let { extras.value = it }
        when (ev.kind) {
            "session.snapshot" -> {
                items.clear()
                items.addAll(ev.items.orEmpty())
                reindex()
                seq = ev.seq
                loaded.value = true
            }
            "session.delta", "session.ops" -> {
                var resync = false
                for (op in ev.ops.orEmpty()) {
                    if (op.seq != 0L && op.seq <= seq) continue
                    if (op.seq != 0L) seq = op.seq
                    when (op.op) {
                        "add" -> op.item?.let { item ->
                            val i = index[item.id]
                            if (i != null) items[i] = item else {
                                index[item.id] = items.size
                                items.add(item)
                            }
                        }
                        "set" -> op.item?.let { item ->
                            val i = index[item.id]
                            if (i != null) items[i] = item else {
                                index[item.id] = items.size
                                items.add(item)
                            }
                        }
                        "append" -> {
                            val i = op.id?.let { index[it] }
                            if (i == null) resync = true else {
                                val cur = items[i]
                                items[i] = cur.copy(text = (cur.text ?: "") + (op.text ?: ""), seq = op.seq)
                            }
                        }
                        "remove" -> op.id?.let { rid ->
                            index[rid]?.let { items.removeAt(it); reindex() }
                        }
                        "reset" -> {
                            items.clear()
                            items.addAll(op.items.orEmpty())
                            reindex()
                        }
                    }
                }
                if (ev.kind == "session.delta") {
                    seq = maxOf(seq, ev.seq)
                    loaded.value = true
                }
                if (resync) {
                    loaded.value = false
                    seq = 0
                    subscribe()
                }
            }
            "session.usage" -> {
                summary.value = summary.value?.copy(contextUsed = ev.used, contextSize = ev.size)
            }
        }
    }

    fun itemIndex(itemId: String): Int = index[itemId] ?: -1

    // ---- side questions (/btw) ----

    /** Side questions, oldest first. agentc keeps them; older versions only answer, so they live here. */
    val asides = MutableStateFlow<List<Aside>>(emptyList())
    /** A side answer that arrived while the side questions screen wasn't open. */
    val unseenAside = MutableStateFlow<Aside?>(null)
    /** How many side question screens are open for this session. */
    var asideViewers = 0
    var asidesLoaded = false
        private set
    private val keepsAsides: Boolean
        get() {
            val agent = summary.value?.agent?.ifBlank { "agy" } ?: "agy"
            val prof = conn.agents.value.firstOrNull { it.id == agent }
            return if (prof != null) "asides" in prof.features else agent == "codex"
        }
    private val isClaude: Boolean get() = summary.value?.agent == "claude"
    private val isCodex: Boolean get() = summary.value?.agent == "codex"
    val sideQuestions: Boolean get() = keepsAsides || isClaude || isCodex

    fun onAside(a: Aside, fromEvent: Boolean = false) {
        val cur = asides.value
        val i = cur.indexOfFirst { it.id == a.id }
        val wasPending = i >= 0 && cur[i].state == "pending"
        asides.value = if (i >= 0) cur.toMutableList().also { it[i] = a } else cur + a
        if (fromEvent && a.state != "pending" && (wasPending || i < 0) && asideViewers == 0) unseenAside.value = a
    }

    fun setAsides(list: List<Aside>) {
        asides.value = list
        unseenAside.value = unseenAside.value?.takeIf { u -> list.any { it.id == u.id } }
    }

    suspend fun refreshAsides() {
        if (!keepsAsides) return
        val list = conn.rpc.call("session.asides", params { put("id", id) }).decode(ListSerializer(Aside.serializer()))
        asidesLoaded = true
        withContext(Dispatchers.Main) { setAsides(list) }
    }

    /** Asks a side question; the answer arrives later (see [asides]). */
    suspend fun askAside(question: String, followUp: String? = null) {
        if (keepsAsides) {
            val a = conn.rpc.call("session.aside", params {
                put("id", id); put("question", question)
                if (!followUp.isNullOrEmpty()) put("followUp", followUp)
            }).decode(Aside.serializer())
            withContext(Dispatchers.Main) { onAside(a) }
            return
        }
        // agentc 0.3.0: answer directly; the result stays on this phone.
        val label = summary.value?.agentLabel ?: "this agent"
        if (!isClaude && !isCodex) throw RpcException("Side questions for $label need agentc 0.3.2 or newer on this computer")
        if (isCodex) {
            val a = conn.rpc.call("session.aside", params {
                put("id", id); put("question", question)
                if (!followUp.isNullOrEmpty()) put("followUp", followUp)
            }).decode(Aside.serializer())
            withContext(Dispatchers.Main) { onAside(a) }
            return
        }
        if (asides.value.any { it.state == "pending" }) throw RpcException("$label is still answering the previous side question")
        val a = Aside("local-" + System.nanoTime(), question, ts = System.currentTimeMillis(), followUp = followUp.orEmpty())
        withContext(Dispatchers.Main) { onAside(a) }
        conn.scope.launch {
            val done = runCatching {
                conn.rpc.call("session.control", params {
                    put("id", id); put("subtype", "side_question")
                    put("params", params { put("question", legacyAsidePrompt(asides.value, question, followUp)) })
                }, timeoutMs = 300_000).jsonObject["response"]?.jsonPrimitive?.content?.takeIf { it.isNotBlank() } ?: throw RpcException("$label returned no answer")
            }.fold({ a.copy(state = "done", answer = it, doneTs = System.currentTimeMillis()) },
                { a.copy(state = "failed", error = it.message ?: "failed", doneTs = System.currentTimeMillis()) })
            withContext(Dispatchers.Main) { onAside(done, fromEvent = true) }
        }
    }

    /** Deletes one side question, or every answered one when [asideId] is null. */
    suspend fun deleteAside(asideId: String?) {
        val keep = asides.value.filterNot { if (asideId == null) it.state != "pending" else it.id == asideId }
        if (keepsAsides && asideId?.startsWith("local-") != true) {
            conn.rpc.call("session.asideDelete", params { put("id", id); put("asideId", asideId ?: "") })
        }
        withContext(Dispatchers.Main) { setAsides(keep) }
    }

    companion object {
        private val itemsSer = ListSerializer(Item.serializer())
        private val opsSer = ListSerializer(Op.serializer())

        fun parse(name: String, obj: JsonObject): StoreEvent {
            val sess = obj["session"]?.takeIf { it !is JsonNull }
            val summary = sess?.let { runCatching { it.decode(SessionSummary.serializer()) }.getOrNull() }
            val extras = sess?.let { runCatching { it.decode(SessionExtras.serializer()) }.getOrNull() }
            return when (name) {
                "session.snapshot" -> StoreEvent(name, summary, extras, obj["seq"]?.jsonPrimitive?.longOrNull ?: 0,
                    items = obj["items"]?.takeIf { it !is JsonNull }?.decode(itemsSer) ?: emptyList())
                "session.delta" -> StoreEvent(name, summary, extras, obj["seq"]?.jsonPrimitive?.longOrNull ?: 0,
                    ops = obj["ops"]?.takeIf { it !is JsonNull }?.decode(opsSer) ?: emptyList())
                "session.ops" -> StoreEvent(name, ops = obj["ops"]?.takeIf { it !is JsonNull }?.decode(opsSer) ?: emptyList())
                "session.usage" -> StoreEvent(name, used = obj["used"]?.jsonPrimitive?.longOrNull ?: 0, size = obj["size"]?.jsonPrimitive?.longOrNull ?: 0)
                else -> StoreEvent(name, summary, extras)
            }
        }
    }
}

@Suppress("unused")
private fun JsonElement.arr() = jsonArray
