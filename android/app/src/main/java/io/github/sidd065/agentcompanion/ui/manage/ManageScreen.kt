package io.github.sidd065.agentcompanion.ui.manage

import androidx.compose.foundation.clickable
import io.github.sidd065.agentcompanion.graph
import androidx.compose.runtime.collectAsState
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.DropdownMenu
import androidx.compose.material.icons.outlined.ArrowDropDown
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.AgyModel
import io.github.sidd065.agentcompanion.core.CmdResult
import io.github.sidd065.agentcompanion.core.ConfigFileInfo
import io.github.sidd065.agentcompanion.core.McpList
import io.github.sidd065.agentcompanion.core.SkillInfo
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.decode
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.BottomTabs
import io.github.sidd065.agentcompanion.ui.ExplicitHostChoice
import io.github.sidd065.agentcompanion.ui.LocalNav
import io.github.sidd065.agentcompanion.ui.Routes
import io.github.sidd065.agentcompanion.ui.TextHolder
import io.github.sidd065.agentcompanion.ui.activeConnection
import io.github.sidd065.agentcompanion.ui.components.ConfirmDialog
import io.github.sidd065.agentcompanion.ui.components.HostSelectorBar
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.ui.components.Pill
import io.github.sidd065.agentcompanion.ui.selectActiveHost
import io.github.sidd065.agentcompanion.ui.components.copyText
import io.github.sidd065.agentcompanion.ui.components.fmtDuration
import io.github.sidd065.agentcompanion.ui.sessions.ConnectionBanner
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import kotlinx.serialization.json.put

private fun JsonElement?.s(k: String): String? = (this as? JsonObject)?.get(k)?.let { (it as? JsonPrimitive)?.contentOrNull }
private fun JsonElement?.o(k: String): JsonObject? = (this as? JsonObject)?.get(k) as? JsonObject

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ManageScreen() {
    val nav = LocalNav.current
    val snack = LocalSnack.current
    val scope = rememberCoroutineScope()
    val conn = activeConnection() ?: return
    var reload by remember { mutableIntStateOf(0) }
    var daemon by remember { mutableStateOf<JsonObject?>(null) }
    var agy by remember { mutableStateOf<JsonObject?>(null) }
    var models by remember { mutableStateOf<List<AgyModel>>(emptyList()) }
    var mcp by remember { mutableStateOf<McpList?>(null) }
    var plugins by remember { mutableStateOf<JsonArray?>(null) }
    var skills by remember { mutableStateOf<List<SkillInfo>>(emptyList()) }
    var files by remember { mutableStateOf<List<ConfigFileInfo>>(emptyList()) }
    var rc by remember { mutableStateOf<String?>(null) }
    var pushStatus by remember { mutableStateOf<JsonObject?>(null) }
    var devices by remember { mutableStateOf<JsonArray?>(null) }
    var revoking by remember { mutableStateOf<JsonObject?>(null) }
    var hostMenu by remember { mutableStateOf(false) }
    val g = androidx.compose.ui.platform.LocalContext.current.graph
    val settings by g.prefs.flow.collectAsState()
    val allConns by g.connections.collectAsState()
    var busy by remember { mutableStateOf<String?>(null) }
    var addMcp by remember { mutableStateOf(false) }
    var pluginDialog by remember { mutableStateOf(false) }
    var runDialog by remember { mutableStateOf(false) }
    var confirmUpdate by remember { mutableStateOf(false) }

    LaunchedEffect(conn.config.id, reload) {
        daemon = null
        agy = null
        models = emptyList()
        mcp = null
        plugins = null
        skills = emptyList()
        files = emptyList()
        rc = null
        pushStatus = null
        devices = null
        launch { runCatching { daemon = conn.rpc.call("daemon.info").jsonObject } }
        launch { runCatching { agy = conn.rpc.call("agy.info", timeoutMs = 180_000).jsonObject } }
        launch { runCatching { models = conn.rpc.call("agy.models").decode(ListSerializer(AgyModel.serializer())) } }
        launch { runCatching { mcp = conn.rpc.call("agy.mcp.list").decode(McpList.serializer()) } }
        launch { runCatching { plugins = conn.rpc.call("agy.plugin.list") as? JsonArray } }
        launch { runCatching { skills = conn.rpc.call("agy.skills").decode(ListSerializer(SkillInfo.serializer())) } }
        launch { runCatching { files = conn.rpc.call("agy.configFiles").decode(ListSerializer(ConfigFileInfo.serializer())) } }
        launch { runCatching { rc = conn.rpc.call("agy.remoteControl").decode(CmdResult.serializer()).stdout.trim() } }
        launch { runCatching { pushStatus = conn.rpc.call("push.status").jsonObject } }
        launch { runCatching { devices = conn.rpc.call("devices.list") as? JsonArray } }
    }

    fun showResult(title: String, r: CmdResult) {
        val text = buildString {
            append("$ agy ${r.args.joinToString(" ")}\n")
            append("exit ${r.exitCode} · ${fmtDuration(r.durationMs)}\n\n")
            append(r.stdout)
            if (r.stderr.isNotBlank()) append("\n--- stderr ---\n").append(r.stderr)
        }
        nav.navigate(Routes.text(TextHolder.put(title, text)))
    }

    fun runCmd(label: String, method: String, p: JsonObject = JsonObject(emptyMap()), timeout: Long = 300_000, show: Boolean = true) {
        busy = label
        scope.launch {
            runCatching { conn.rpc.call(method, p, timeoutMs = timeout) }
                .onSuccess { r ->
                    runCatching { r.decode(CmdResult.serializer()) }.getOrNull()?.let { cr ->
                        if (show || cr.exitCode != 0) showResult(label, cr) else snack.showSnackbar("$label: done")
                    } ?: snack.showSnackbar("$label: done")
                }
                .onFailure { snack.showSnackbar("$label failed: ${it.message}") }
            busy = null
            reload++
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    androidx.compose.foundation.layout.Box {
                        Row(Modifier.clickable(enabled = settings.hosts.size > 1) { hostMenu = true }, verticalAlignment = Alignment.CenterVertically) {
                            Text(conn.config.label)
                            if (settings.hosts.size > 1) Icon(Icons.Outlined.ArrowDropDown, null)
                        }
                        DropdownMenu(hostMenu, { hostMenu = false }) {
                            settings.hosts.forEach { h ->
                                DropdownMenuItem(text = { Text(h.label + if (h.id == conn.config.id) "  ✓" else "") }, onClick = {
                                    hostMenu = false
                                    selectActiveHost(g.prefs, scope, h.id)
                                })
                            }
                            DropdownMenuItem(text = { Text("Hosts & pairing…") }, onClick = { hostMenu = false; nav.navigate("hosts") })
                        }
                    }
                },
                actions = {
                    if (busy != null) CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
                    IconButton(onClick = { reload++ }) { Icon(Icons.Outlined.Refresh, "Refresh") }
                    IconButton(onClick = { nav.navigate("settings") }) { Icon(Icons.Outlined.Settings, "App settings") }
                },
            )
        },
        bottomBar = { BottomTabs(nav) },
    ) { pad ->
        Column(Modifier.padding(pad).fillMaxSize()) {
            if (allConns.size > 1) {
                HostSelectorBar(
                    conns = allConns,
                    activeHostId = conn.config.id,
                    onSelect = { id -> selectActiveHost(g.prefs, scope, id) },
                )
            }
            Column(Modifier.weight(1f).fillMaxWidth().verticalScroll(rememberScrollState()).padding(horizontal = 12.dp)) {
            ConnectionBanner(conn)
            busy?.let { Text("$it…", style = MaterialTheme.typography.labelMedium, color = LocalExtra.current.muted, modifier = Modifier.padding(4.dp)) }

            // Machine / daemon
            Block("Machine") {
                val d = daemon
                if (d == null) Text("Loading…") else {
                    val tun = d.o("tunnel")
                    KV("Host", d.s("host"))
                    KV("Public URL", (d.s("publicUrl") ?: "").removePrefix("https://").ifBlank { "starting…" } +
                        (tun?.s("status")?.let { " ($it)" } ?: ""))
                    KV("Fingerprint", d.s("fingerprint"))
                    KV("Daemon", "agentc ${d.s("version")} · up ${fmtDuration((d["uptimeSec"]?.jsonPrimitive?.longOrNull ?: 0) * 1000)}")
                    val st = d.o("stats")
                    KV("Agent processes", "${st?.get("live")?.jsonPrimitive?.content ?: "?"} live of ${st?.get("sessions")?.jsonPrimitive?.content ?: "?"} sessions · ${(st?.get("rssKiB")?.jsonPrimitive?.longOrNull ?: 0) / 1024} MB")
                    KV("Connected apps", (d["clients"] as? JsonArray)?.joinToString { it.s("device") ?: "?" })
                    val cfg = d.o("config")
                    if (cfg != null) {
                        Spacer(Modifier.height(8.dp))
                        Text("Stop idle agent processes after", style = MaterialTheme.typography.bodyMedium)
                        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                            val cur = cfg["idleHibernateMinutes"]?.jsonPrimitive?.intOrNull
                            listOf(10, 30, 60, 180, 720).forEach { m ->
                                FilterChip(cur == m, onClick = {
                                    scope.launch { runCatching { conn.rpc.call("daemon.config.set", params { put("idleHibernateMinutes", m) }) }; reload++ }
                                }, label = { Text(if (m < 60) "${m}m" else "${m / 60}h") })
                            }
                        }
                        Text("Max running agent processes", style = MaterialTheme.typography.bodyMedium)
                        Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                            val cur = cfg["maxLive"]?.jsonPrimitive?.intOrNull
                            listOf(3, 5, 8, 12, 20).forEach { m ->
                                FilterChip(cur == m, onClick = {
                                    scope.launch { runCatching { conn.rpc.call("daemon.config.set", params { put("maxLive", m) }) }; reload++ }
                                }, label = { Text("$m") })
                            }
                        }
                    }
                }
            }

            // Paired devices
            Block("Paired devices") {
                val list = devices
                if (list == null) Text("Loading…") else if (list.isEmpty()) Text("None") else list.forEach { dv ->
                    val o = dv.jsonObject
                    Row(Modifier.fillMaxWidth().padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text((o.s("name") ?: "Device") + if (o["self"]?.jsonPrimitive?.booleanOrNull == true) "  (this phone)" else "",
                                style = MaterialTheme.typography.bodyMedium)
                            Text(listOfNotNull(o.s("platform"), o.s("appVersion")?.let { "v$it" },
                                o["lastSeen"]?.jsonPrimitive?.longOrNull?.takeIf { it > 0 }?.let { "seen " + io.github.sidd065.agentcompanion.ui.components.relTime(it) })
                                .joinToString(" · "), style = MaterialTheme.typography.labelSmall, color = LocalExtra.current.muted)
                        }
                        TextButton(onClick = { revoking = o }) { Text("Unpair") }
                    }
                }
                if (list != null && list.size > 1) {
                    OutlinedButton(
                        onClick = {
                            scope.launch {
                                runCatching { conn.rpc.call("devices.revokeOthers") }
                                    .onSuccess { snack.showSnackbar("Unpaired all other devices"); reload++ }
                                    .onFailure { snack.showSnackbar("Revoke failed: ${it.message}") }
                            }
                        },
                        modifier = Modifier.padding(top = 6.dp),
                    ) { Text("Unpair all other devices") }
                }
                Text("Pair another phone with `agentc pair` on ${conn.config.label}.", style = MaterialTheme.typography.labelSmall, color = LocalExtra.current.muted)
            }

            // Push
            Block("Push notifications") {
                val p = pushStatus
                if (p == null) Text("Loading…") else {
                    val ok = p["configured"]?.jsonPrimitive?.booleanOrNull == true
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Pill(if (ok) "Firebase ready" else "Not set up", if (ok) LocalExtra.current.success else LocalExtra.current.warning)
                        Spacer(Modifier.width(8.dp))
                        Text("${(p["devices"] as? JsonArray)?.size ?: 0} device(s) registered", style = MaterialTheme.typography.bodySmall)
                    }
                    if (!ok) {
                        Text(
                            "Save a Firebase service account key (Firebase console → Project settings → Service accounts → Generate new private key) on ${conn.config.label} at:",
                            style = MaterialTheme.typography.bodySmall,
                        )
                        SelectionContainer { Text(p.s("serviceAccountPath") ?: "~/.config/agentc/firebase-service-account.json", fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall) }
                    } else {
                        KV("Project", p.s("projectId"))
                        p.s("lastError")?.takeIf { it.isNotBlank() }?.let { KV("Last error", it) }
                    }
                    Text("Notification text", style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(top = 6.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        val cur = p.s("content")
                        FilterChip(cur == "full", onClick = { scope.launch { runCatching { conn.rpc.call("daemon.config.set", params { put("pushContent", "full") }) }; reload++ } },
                            label = { Text("Title + excerpt") })
                        FilterChip(cur == "minimal", onClick = { scope.launch { runCatching { conn.rpc.call("daemon.config.set", params { put("pushContent", "minimal") }) }; reload++ } },
                            label = { Text("No content via Google") })
                    }
                }
            }

            // Protocol v2 Workbench: Usage & Cost, Schedules, Host Health/PSI, Providers
            WorkbenchV2Section(conn, reload) { sid -> nav.navigate(Routes.chat(sid, host = conn.config.id)) }

            // Claude Code
            ClaudeManageBlocks(conn, reload) { reload++ }

            // Account
            Block("agy account") {
                val a = agy
                if (a == null) Text("Loading…") else {
                    val user = a.o("status").o("user")
                    KV("Version", a.s("version"))
                    KV("Signed in", listOfNotNull(user.s("name"), user.s("email")).joinToString(" · ").ifBlank { null })
                    KV("Plan", user.o("planStatus").o("planInfo").s("planName"))
                    user.o("planStatus")?.get("availablePromptCredits")?.jsonPrimitive?.contentOrNull?.let { KV("Credits", it) }
                    val auth = a.o("status").o("auth").o("authResult")
                    auth?.let { KV("Auth", listOfNotNull(if (it["hasValidAuth"]?.jsonPrimitive?.booleanOrNull == true) "valid" else "invalid", it.s("projectId"), it.s("location")).joinToString(" · ")) }
                    a.s("statusError")?.let { KV("Status", it) }
                    Row(Modifier.horizontalScroll(rememberScrollState()).padding(top = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        OutlinedButton(onClick = {
                            scope.launch {
                                runCatching { conn.rpc.call("agy.changelog").jsonObject["text"]?.jsonPrimitive?.content.orEmpty() }
                                    .onSuccess { nav.navigate(Routes.text(TextHolder.put("agy changelog", it))) }
                                    .onFailure { snack.showSnackbar("${it.message}") }
                            }
                        }) { Text("Changelog") }
                        OutlinedButton(onClick = { confirmUpdate = true }) { Text("Update agy") }
                        OutlinedButton(onClick = { runDialog = true }) { Text("Run agy command") }
                    }
                }
            }

            // Quick settings
            Block("agy settings") {
                val st = agy?.o("settings")
                val mode = st.s("agentMode") ?: ""
                Text("Agent mode (TUI default)", style = MaterialTheme.typography.bodyMedium)
                Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    listOf("" to "Default", "accept-edits" to "Accept edits", "plan" to "Plan", "auto" to "Auto").forEach { (k, l) ->
                        FilterChip(mode == k, onClick = { setSetting(conn, "agentMode", JsonPrimitive(k)) { reload++ } }, label = { Text(l) })
                    }
                }
                val q = st.s("queuedMessages") ?: ""
                Text("Messages while working", style = MaterialTheme.typography.bodyMedium)
                Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    FilterChip(!q.contains("immediate"), onClick = { setSetting(conn, "queuedMessages", JsonPrimitive("queue")) { reload++ } }, label = { Text("Queue") })
                    FilterChip(q.contains("immediate"), onClick = { setSetting(conn, "queuedMessages", JsonPrimitive("send-immediately")) { reload++ } }, label = { Text("Send immediately") })
                }
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Text("Allow access outside the workspace", style = MaterialTheme.typography.bodyMedium, modifier = Modifier.weight(1f))
                    val v = st?.get("allowNonWorkspaceAccess")?.jsonPrimitive?.booleanOrNull == true
                    Switch(v, { setSetting(conn, "allowNonWorkspaceAccess", JsonPrimitive(it)) { reload++ } })
                }
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Text("Auto-allow read requests (agentc)", style = MaterialTheme.typography.bodyMedium, modifier = Modifier.weight(1f))
                    val autoReads = daemon?.o("config")?.get("agyAutoAllowReads")?.jsonPrimitive?.booleanOrNull == true || settings.agyFlags.autoAllowReads
                    Switch(autoReads, { next ->
                        scope.launch {
                            g.prefs.update { it.copy(agyFlags = it.agyFlags.copy(autoAllowReads = next)) }
                            runCatching { conn.rpc.call("daemon.config.set", params { put("agyAutoAllowReads", next) }) }
                            reload++
                        }
                    })
                }
                KV("Default model", st.s("model"))
                TextButton(onClick = { files.firstOrNull { it.name == "settings.json" }?.let { nav.navigate(Routes.file(it.path, edit = true, host = conn.config.id)) } }) { Text("Edit settings.json") }
            }

            // Models
            Block("Models") {
                val cur = agy?.o("settings").s("model")
                if (models.isEmpty()) Text("Loading…", style = MaterialTheme.typography.bodySmall)
                models.forEach { m ->
                    Row(Modifier.fillMaxWidth().clickable { setSetting(conn, "model", JsonPrimitive(m.name)) { reload++ } }.padding(vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(m.name, style = MaterialTheme.typography.bodyMedium)
                            Text(m.id, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace, color = LocalExtra.current.muted)
                        }
                        if (m.name == cur) Pill("default", MaterialTheme.colorScheme.primary)
                    }
                }
                Text("Tap a model to make it agy's default (settings.json).", style = MaterialTheme.typography.labelSmall, color = LocalExtra.current.muted)
            }

            // MCP
            Block("MCP servers", action = { IconButton(onClick = { addMcp = true }) { Icon(Icons.Outlined.Add, "Add") } }) {
                val m = mcp
                if (m == null) Text("Loading…") else {
                    if (m.servers.isEmpty()) Text("No MCP servers configured.", style = MaterialTheme.typography.bodySmall)
                    m.servers.forEach { srv ->
                        Row(Modifier.fillMaxWidth().padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                            Column(Modifier.weight(1f)) {
                                Text(srv.name, style = MaterialTheme.typography.bodyMedium)
                                Text(srv.url ?: listOfNotNull(srv.command, srv.args?.joinToString(" ")).joinToString(" "), style = MaterialTheme.typography.labelSmall,
                                    fontFamily = FontFamily.Monospace, color = LocalExtra.current.muted, maxLines = 2, overflow = TextOverflow.Ellipsis)
                            }
                            Switch(!srv.disabled, { on -> runCmd(if (on) "Enable ${srv.name}" else "Disable ${srv.name}", if (on) "agy.mcp.enable" else "agy.mcp.disable", params { put("name", srv.name) }, show = false) })
                            IconButton(onClick = { runCmd("Remove ${srv.name}", "agy.mcp.remove", params { put("name", srv.name) }, show = false) }) { Icon(Icons.Outlined.Delete, "Remove") }
                        }
                    }
                    m.error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
                    TextButton(onClick = { nav.navigate(Routes.file(m.path, edit = true, host = conn.config.id)) }) { Text("Edit mcp_config.json") }
                }
            }

            // Plugins
            Block("Plugins", action = { IconButton(onClick = { pluginDialog = true }) { Icon(Icons.Outlined.Add, "Install") } }) {
                val p = plugins
                if (p == null) Text("Loading…") else if (p.isEmpty()) Text("No plugins installed.", style = MaterialTheme.typography.bodySmall)
                p?.forEach { pl ->
                    val name = pl.s("name") ?: pl.toString().take(40)
                    val enabled = (pl as? JsonObject)?.get("enabled")?.jsonPrimitive?.booleanOrNull ?: true
                    Row(Modifier.fillMaxWidth().padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(name, style = MaterialTheme.typography.bodyMedium)
                            Text(listOfNotNull(pl.s("version"), pl.s("description")).joinToString(" · "), style = MaterialTheme.typography.labelSmall, color = LocalExtra.current.muted, maxLines = 2)
                        }
                        Switch(enabled, { on -> runCmd("${if (on) "Enable" else "Disable"} $name", "agy.plugin.run", params { put("action", if (on) "enable" else "disable"); put("args", JsonArray(listOf(JsonPrimitive(name)))) }, show = false) })
                        IconButton(onClick = { runCmd("Uninstall $name", "agy.plugin.run", params { put("action", "uninstall"); put("args", JsonArray(listOf(JsonPrimitive(name)))) }) }) { Icon(Icons.Outlined.Delete, "Uninstall") }
                    }
                }
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedButton(onClick = { runCmd("Import Claude Code plugins", "agy.plugin.run", params { put("action", "import"); put("args", JsonArray(listOf(JsonPrimitive("claude")))) }) }) { Text("Import from Claude") }
                    OutlinedButton(onClick = { runCmd("Import Gemini extensions", "agy.plugin.run", params { put("action", "import"); put("args", JsonArray(listOf(JsonPrimitive("gemini")))) }) }) { Text("Import from Gemini") }
                }
            }

            // Skills
            Block("Skills (/commands)") {
                if (skills.isEmpty()) Text("Loading…", style = MaterialTheme.typography.bodySmall)
                skills.groupBy { it.source }.forEach { (src, list) ->
                    Text(src, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary, modifier = Modifier.padding(top = 6.dp))
                    list.forEach { sk ->
                        Column(Modifier.fillMaxWidth().clickable { nav.navigate(Routes.file(sk.path, host = conn.config.id)) }.padding(vertical = 4.dp)) {
                            Text("/" + sk.name, style = MaterialTheme.typography.bodyMedium, fontFamily = FontFamily.Monospace)
                            if (sk.description.isNotBlank()) Text(sk.description, style = MaterialTheme.typography.labelSmall, color = LocalExtra.current.muted, maxLines = 2, overflow = TextOverflow.Ellipsis)
                        }
                    }
                }
            }

            // Config files
            Block("Configuration files") {
                files.forEach { f ->
                    Row(Modifier.fillMaxWidth().clickable {
                        if (f.dir) nav.navigate(Routes.files(f.path, host = conn.config.id)) else nav.navigate(Routes.file(f.path, edit = !f.exists, host = conn.config.id))
                        if (!f.dir && !f.exists) scope.launch { runCatching { conn.rpc.call("fs.write", params { put("path", f.path); put("text", ""); put("createDirs", true); put("noOverwrite", true) }) } }
                    }.padding(vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(f.name, style = MaterialTheme.typography.bodyMedium, fontFamily = FontFamily.Monospace)
                            Text(f.desc, style = MaterialTheme.typography.labelSmall, color = LocalExtra.current.muted)
                        }
                        if (!f.exists) Pill("create", LocalExtra.current.muted)
                    }
                }
            }

            // agy's own remote control
            Block("agy Remote Control (Google)") {
                Text(rc ?: "Loading…", style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
                Text("agy's own browser remote control, which goes through Google's servers. Agent Companion doesn't need it.", style = MaterialTheme.typography.labelSmall, color = LocalExtra.current.muted)
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedButton(onClick = { runCmd("Start remote-control daemon", "agy.remoteControl", params { put("action", "start") }) }) { Text("Start") }
                    OutlinedButton(onClick = { runCmd("Stop remote-control daemon", "agy.remoteControl", params { put("action", "stop") }) }) { Text("Stop") }
                }
            }
            Spacer(Modifier.height(24.dp))
            }
        }
    }

    if (confirmUpdate) {
        ConfirmDialog("Update agy?", "Runs `agy update` on ${conn.config.label}. Running sessions keep the old version until they restart.", "Update", onDismiss = { confirmUpdate = false }) {
            runCmd("agy update", "agy.update", timeout = 900_000)
        }
    }
    if (addMcp) McpDialog(onDismiss = { addMcp = false }) { p -> runCmd("Add MCP server", "agy.mcp.add", p) }
    if (pluginDialog) PluginDialog(onDismiss = { pluginDialog = false }) { target -> runCmd("Install plugin", "agy.plugin.run", params { put("action", "install"); put("args", JsonArray(listOf(JsonPrimitive(target)))) }) }
    if (runDialog) RunDialog(onDismiss = { runDialog = false }) { args ->
        runCmd("agy " + args.joinToString(" "), "agy.exec", params { put("args", JsonArray(args.map { JsonPrimitive(it) })); put("timeoutSec", 600) }, timeout = 660_000)
    }
    revoking?.let { dv ->
        val self = dv["self"]?.jsonPrimitive?.booleanOrNull == true
        androidx.compose.material3.AlertDialog(
            onDismissRequest = { revoking = null },
            title = { Text("Unpair ${dv.s("name") ?: "device"}?") },
            text = { Text(if (self) "This phone loses access to ${conn.config.label} and forgets it." else "That device loses access to ${conn.config.label} immediately.") },
            confirmButton = {
                TextButton(onClick = {
                    revoking = null
                    scope.launch {
                        runCatching { conn.rpc.call("devices.revoke", params { put("id", dv.s("id") ?: "") }) }
                            .onFailure { snack.showSnackbar("Unpair failed: ${it.message}") }
                        if (self) {
                            if (ExplicitHostChoice.hostId == conn.config.id) ExplicitHostChoice.hostId = null
                            g.prefs.update { st ->
                                val rest = st.hosts.filterNot { it.id == conn.config.id }
                                st.copy(hosts = rest, activeHostId = rest.firstOrNull()?.id)
                            }
                        } else reload++
                    }
                }) { Text("Unpair") }
            },
            dismissButton = { TextButton(onClick = { revoking = null }) { Text("Cancel") } },
        )
    }
}

private fun setSetting(conn: HostConnection, key: String, value: JsonElement, after: () -> Unit) {
    conn.scope.launch {
        runCatching { conn.rpc.call("agy.settings.set", params { put("key", key); put("value", value) }) }
        after()
    }
}

@Composable
internal fun Block(title: String, action: (@Composable () -> Unit)? = null, content: @Composable () -> Unit) {
    Card(Modifier.fillMaxWidth().padding(vertical = 6.dp), colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer), shape = RoundedCornerShape(16.dp)) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(title, style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
                action?.invoke()
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.5f), modifier = Modifier.padding(vertical = 4.dp))
            content()
        }
    }
}

@Composable
internal fun KV(k: String, v: String?) {
    if (v.isNullOrBlank()) return
    Row(Modifier.fillMaxWidth().padding(vertical = 2.dp)) {
        Text(k, style = MaterialTheme.typography.bodySmall, color = LocalExtra.current.muted, modifier = Modifier.width(118.dp))
        Text(v, style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f))
    }
}

@Composable
private fun McpDialog(onDismiss: () -> Unit, onAdd: (JsonObject) -> Unit) {
    var name by remember { mutableStateOf("") }
    var target by remember { mutableStateOf("") }
    var args by remember { mutableStateOf("") }
    var env by remember { mutableStateOf("") }
    var headers by remember { mutableStateOf("") }
    val http = target.startsWith("http://") || target.startsWith("https://")
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Add MCP server") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.verticalScroll(rememberScrollState())) {
                OutlinedTextField(name, { name = it.trim() }, label = { Text("Name") }, singleLine = true)
                OutlinedTextField(target, { target = it.trim() }, label = { Text("Command or URL") }, placeholder = { Text("npx or https://…") }, singleLine = true)
                if (!http) OutlinedTextField(args, { args = it }, label = { Text("Arguments (space separated)") }, singleLine = true)
                if (!http) OutlinedTextField(env, { env = it }, label = { Text("Environment (KEY=VALUE per line)") })
                if (http) OutlinedTextField(headers, { headers = it }, label = { Text("Headers (Key: Value per line)") })
            }
        },
        confirmButton = {
            Button(enabled = name.isNotBlank() && target.isNotBlank(), onClick = {
                onDismiss()
                onAdd(params {
                    put("name", name); put("target", target)
                    if (http) put("type", "http")
                    if (args.isNotBlank()) put("args", JsonArray(args.trim().split(Regex("\\s+")).map { JsonPrimitive(it) }))
                    val envMap = env.lines().mapNotNull { l -> l.split('=', limit = 2).takeIf { it.size == 2 && it[0].isNotBlank() }?.let { it[0].trim() to it[1] } }
                    if (envMap.isNotEmpty()) put("env", JsonObject(envMap.associate { it.first to JsonPrimitive(it.second) }))
                    val hMap = headers.lines().mapNotNull { l -> l.split(':', limit = 2).takeIf { it.size == 2 && it[0].isNotBlank() }?.let { it[0].trim() to it[1].trim() } }
                    if (hMap.isNotEmpty()) put("headers", JsonObject(hMap.associate { it.first to JsonPrimitive(it.second) }))
                })
            }) { Text("Add") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

@Composable
private fun PluginDialog(onDismiss: () -> Unit, onInstall: (String) -> Unit) {
    var target by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Install plugin") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                OutlinedTextField(target, { target = it.trim() }, label = { Text("Plugin") }, placeholder = { Text("name@antigravity-plugins-official") }, singleLine = true)
                Text("A marketplace name (plugin@marketplace), a GitHub URL, or a folder path on the computer.", style = MaterialTheme.typography.labelSmall)
            }
        },
        confirmButton = { Button(enabled = target.isNotBlank(), onClick = { onDismiss(); onInstall(target) }) { Text("Install") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

@Composable
private fun RunDialog(onDismiss: () -> Unit, onRun: (List<String>) -> Unit) {
    var cmd by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Run agy command") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                OutlinedTextField(cmd, { cmd = it }, label = { Text("agy …") }, placeholder = { Text("plugin validate ~/my-plugin") }, singleLine = true)
                Text("Runs non-interactively and shows the output. For the interactive TUI, open an agy terminal.", style = MaterialTheme.typography.labelSmall)
                listOf("models", "agents", "mcp list", "plugin list", "remote-control status", "--help").forEach { s ->
                    Text("agy $s", fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.primary,
                        modifier = Modifier.clickable { cmd = s }.padding(vertical = 2.dp))
                }
            }
        },
        confirmButton = {
            Button(enabled = cmd.isNotBlank(), onClick = {
                onDismiss()
                onRun(Regex("""[^\s"']+|"([^"]*)"|'([^']*)'""").findAll(cmd.removePrefix("agy ").trim()).map { m -> m.groupValues[1].ifEmpty { m.groupValues[2].ifEmpty { m.value } } }.toList())
            }) { Text("Run") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TextViewerScreen(key: String) {
    val nav = LocalNav.current
    val ctx = LocalContext.current
    val (title, text) = TextHolder.items[key] ?: ("Text" to "")
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                navigationIcon = { IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, null) } },
                actions = { IconButton(onClick = { ctx.copyText(text) }) { Icon(Icons.Outlined.ContentCopy, "Copy") } },
            )
        },
    ) { pad ->
        SelectionContainer {
            Text(
                text, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace,
                modifier = Modifier.padding(pad).fillMaxSize().verticalScroll(rememberScrollState()).horizontalScroll(rememberScrollState()).padding(12.dp),
            )
        }
    }
}

@Suppress("unused")
private fun keepJa(e: JsonElement) = e.jsonArray
