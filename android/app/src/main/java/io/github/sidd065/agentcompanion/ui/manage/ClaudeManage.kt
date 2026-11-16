package io.github.sidd065.agentcompanion.ui.manage

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.FilterChip
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.LocalNav
import io.github.sidd065.agentcompanion.ui.Routes
import io.github.sidd065.agentcompanion.ui.TextHolder
import io.github.sidd065.agentcompanion.ui.components.ConfirmDialog
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.ui.components.relTime
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.doubleOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.longOrNull
import kotlinx.serialization.json.put
import java.time.OffsetDateTime

private fun JsonElement?.cs(k: String): String? = (this as? JsonObject)?.get(k)?.let { (it as? JsonPrimitive)?.contentOrNull }
private fun JsonElement?.co(k: String): JsonObject? = (this as? JsonObject)?.get(k) as? JsonObject
private fun JsonElement?.ca(k: String): List<JsonObject> = ((this as? JsonObject)?.get(k) as? JsonArray)?.mapNotNull { it as? JsonObject } ?: emptyList()

private fun resetLabel(v: JsonElement?): String? {
    val p = v as? JsonPrimitive ?: return null
    val ms = p.longOrNull?.let { if (it < 10_000_000_000L) it * 1000 else it }
        ?: p.contentOrNull?.let { runCatching { OffsetDateTime.parse(it).toInstant().toEpochMilli() }.getOrNull() }
        ?: return null
    val d = ms - System.currentTimeMillis()
    if (d <= 0) return "resets now"
    val h = d / 3_600_000
    return if (h >= 24) "resets in ${h / 24}d ${h % 24}h" else if (h >= 1) "resets in ${h}h ${(d / 60_000) % 60}m" else "resets in ${d / 60_000}m"
}

/** Claude Code on this computer: version, plan usage, terminals, models, MCP servers, settings. */
@Composable
fun ClaudeManageBlocks(conn: HostConnection, reload: Int, onReload: () -> Unit) {
    val nav = LocalNav.current
    val snack = LocalSnack.current
    val scope = rememberCoroutineScope()
    val ex = LocalExtra.current
    var info by remember { mutableStateOf<JsonObject?>(null) }
    var usage by remember { mutableStateOf<JsonObject?>(null) }
    var catalog by remember { mutableStateOf<JsonObject?>(null) }
    var settings by remember { mutableStateOf<JsonObject?>(null) }
    var mcp by remember { mutableStateOf<List<JsonObject>?>(null) }
    var mcpErr by remember { mutableStateOf<String?>(null) }
    var showMcp by remember { mutableStateOf(false) }
    var confirmUpdate by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(conn, reload) {
        runCatching { info = conn.rpc.call("claude.info").jsonObject }
        if (info?.get("available")?.let { (it as? JsonPrimitive)?.booleanOrNull } != true) return@LaunchedEffect
        runCatching { settings = conn.rpc.call("claude.settings.get").jsonObject }
        runCatching { catalog = conn.rpc.call("claude.catalog", timeoutMs = 120_000).jsonObject }
        runCatching { usage = conn.rpc.call("claude.usage", timeoutMs = 120_000).jsonObject }
    }
    LaunchedEffect(showMcp, reload) {
        if (!showMcp) return@LaunchedEffect
        mcp = null; mcpErr = null
        runCatching { mcp = conn.rpc.call("claude.query", params { put("subtype", "mcp_status") }, timeoutMs = 120_000).ca("mcpServers") }
            .onFailure { mcpErr = it.message }
    }
    val i = info ?: return
    if ((i["available"] as? JsonPrimitive)?.booleanOrNull != true) {
        Block("Claude Code") {
            Text(i.cs("reason") ?: "Claude Code isn't installed on this computer.", style = MaterialTheme.typography.bodySmall)
        }
        return
    }
    Block("Claude Code") {
        KV("Version", i.cs("version"))
        KV("Executable", i.cs("path"))
        val acc = catalog.co("account")
        KV("Plan", acc.cs("subscriptionType")?.replaceFirstChar { it.uppercase() } ?: usage.co("usage").cs("subscription_type")?.replaceFirstChar { it.uppercase() })
        KV("Provider", acc.cs("apiProvider"))
        KV("Organization", acc.cs("organization"))
        // Plan limits: from /usage, else the last rate-limit event a session reported.
        val limits = usage.co("usage").co("rate_limits")
        val rl = usage.co("rateLimit") ?: i.co("rateLimit")
        val windows = buildList {
            limits?.forEach { (k, v) ->
                val o = v as? JsonObject ?: return@forEach
                val u = (o["utilization"] as? JsonPrimitive)?.doubleOrNull ?: return@forEach
                add(Triple(k, u / 100.0, resetLabel(o["resets_at"])))
            }
            if (isEmpty()) rl.co("unifiedWindows")?.forEach { (k, v) ->
                val o = v as? JsonObject ?: return@forEach
                val u = (o["utilization"] as? JsonPrimitive)?.doubleOrNull ?: return@forEach
                add(Triple(k, u, resetLabel(o["resetsAt"])))
            }
        }
        if (windows.isNotEmpty()) {
            Text("Plan usage", style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(top = 6.dp))
            windows.forEach { (k, frac, reset) ->
                val name = when (k) {
                    "five_hour" -> "Current session (5h)"
                    "seven_day" -> "This week"
                    "seven_day_opus" -> "This week · Opus"
                    "seven_day_sonnet" -> "This week · Sonnet"
                    else -> k.replace('_', ' ').replaceFirstChar { it.uppercase() }
                }
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(name, style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f))
                    Text("${(frac * 100).toInt()}%" + (reset?.let { " · $it" } ?: ""), style = MaterialTheme.typography.labelSmall, color = ex.muted)
                }
                LinearProgressIndicator(progress = { frac.toFloat().coerceIn(0f, 1f) }, modifier = Modifier.fillMaxWidth().padding(bottom = 4.dp),
                    color = if (frac >= 0.9) ex.danger else if (frac >= 0.75) ex.warning else MaterialTheme.colorScheme.primary)
            }
        }
        usage.cs("error")?.let { Text("Usage: $it", style = MaterialTheme.typography.labelSmall, color = ex.muted) }
        Row(Modifier.horizontalScroll(rememberScrollState()).padding(top = 6.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedButton(onClick = { confirmUpdate = true }, enabled = busy == null) { Text("Update Claude Code") }
            i.cs("settingsPath")?.let { p -> OutlinedButton(onClick = { nav.navigate(Routes.file(p, edit = true)) }) { Text("Edit settings.json") } }
        }
    }

    val live = i.ca("live")
    Block("Claude Code terminals on ${conn.config.label}") {
        if (live.isEmpty()) Text("None running. Sessions you start in a terminal on the computer show up here.", style = MaterialTheme.typography.bodySmall, color = ex.muted)
        live.forEach { ls ->
            val sid = ls.cs("sessionId") ?: return@forEach
            Row(Modifier.fillMaxWidth().clickable { nav.navigate(Routes.chat(sid, host = conn.config.id)) }.padding(vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                val st = ls.cs("status").orEmpty()
                Box(Modifier.size(9.dp).clip(CircleShape).background(when (st) { "busy" -> ex.running; "idle" -> ex.success; else -> ex.warning }))
                Spacer(Modifier.width(10.dp))
                Column(Modifier.weight(1f)) {
                    Text(ls.cs("name") ?: sid.take(8), style = MaterialTheme.typography.bodyMedium)
                    Text(listOfNotNull(st.ifBlank { null }, ls.cs("kind"), ls.cs("cwd")?.replace(Regex("^/home/[^/]+"), "~"),
                        (ls["updatedAt"] as? JsonPrimitive)?.longOrNull?.let { relTime(it) }).joinToString(" · "),
                        style = MaterialTheme.typography.labelSmall, color = ex.muted)
                }
            }
        }
    }

    val st = settings.co("parsed")
    val models = catalog.ca("models")
    Block("Claude Code default model") {
        val cur = st.cs("model")
        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            FilterChip(cur == null, onClick = {
                scope.launch { runCatching { conn.rpc.call("claude.settings.set", params { put("key", "model"); put("value", JsonNull) }) }; onReload() }
            }, label = { Text("Default") })
            models.filter { it.cs("value") != "default" }.forEach { m ->
                val v = m.cs("value") ?: return@forEach
                FilterChip(cur == v, onClick = {
                    scope.launch { runCatching { conn.rpc.call("claude.settings.set", params { put("key", "model"); put("value", v) }) }; onReload() }
                }, label = { Text(m.cs("displayName") ?: v) })
            }
        }
        Text("Used by new Claude sessions and terminals when the app's Claude flags don't pick a model.", style = MaterialTheme.typography.labelSmall, color = ex.muted)
        val agents = catalog.ca("agents")
        if (agents.isNotEmpty()) {
            Text("Agents", style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(top = 6.dp))
            Text(agents.mapNotNull { it.cs("name") }.joinToString(" · "), style = MaterialTheme.typography.bodySmall, color = ex.muted)
        }
        val cmds = catalog.ca("commands")
        if (cmds.isNotEmpty()) KV("Commands & skills", "${cmds.size}")
    }

    Block("Claude Code MCP servers", action = { if (!showMcp) TextButton(onClick = { showMcp = true }) { Text("Check") } }) {
        when {
            !showMcp -> Text("Checking starts Claude Code briefly to connect each server.", style = MaterialTheme.typography.bodySmall, color = ex.muted)
            mcpErr != null -> Text(mcpErr!!, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
            mcp == null -> LinearProgressIndicator(Modifier.fillMaxWidth())
            mcp!!.isEmpty() -> Text("No MCP servers configured.", style = MaterialTheme.typography.bodySmall)
            else -> mcp!!.forEach { sv ->
                val status = sv.cs("status").orEmpty()
                Row(Modifier.fillMaxWidth().padding(vertical = 3.dp), verticalAlignment = Alignment.CenterVertically) {
                    Box(Modifier.size(9.dp).clip(CircleShape).background(when (status) {
                        "connected" -> ex.success; "failed" -> ex.danger; "needs-auth", "pending" -> ex.warning; else -> ex.muted
                    }))
                    Spacer(Modifier.width(10.dp))
                    Column(Modifier.weight(1f)) {
                        Text(sv.cs("name").orEmpty(), style = MaterialTheme.typography.bodyMedium)
                        Text(listOfNotNull(status, sv.ca("tools").size.takeIf { it > 0 }?.let { "$it tools" }, sv.cs("scope")).joinToString(" · "),
                            style = MaterialTheme.typography.labelSmall, color = ex.muted)
                        sv.cs("error")?.let { Text(it, style = MaterialTheme.typography.labelSmall, color = ex.danger, maxLines = 2) }
                    }
                }
            }
        }
    }

    if (confirmUpdate) {
        ConfirmDialog("Update Claude Code?", "Runs `claude update` on ${conn.config.label}. Running sessions keep the old version until they restart.", "Update",
            onDismiss = { confirmUpdate = false }) {
            busy = "update"
            scope.launch {
                runCatching { conn.rpc.call("claude.cli", params { put("args", JsonArray(listOf(JsonPrimitive("update")))) }, timeoutMs = 240_000).jsonObject }
                    .onSuccess { r ->
                        val out = listOfNotNull(r.cs("stdout"), r.cs("stderr")).joinToString("\n").trim()
                        nav.navigate(Routes.text(TextHolder.put("claude update", out.ifBlank { "Done." })))
                    }
                    .onFailure { snack.showSnackbar("Update failed: ${it.message}") }
                busy = null
                onReload()
            }
        }
    }
}
