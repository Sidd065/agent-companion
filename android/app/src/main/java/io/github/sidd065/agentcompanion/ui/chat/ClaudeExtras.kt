package io.github.sidd065.agentcompanion.ui.chat

import io.github.sidd065.agentcompanion.ui.components.AppSheet
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.AutoAwesome
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.SessionSummary
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.components.fmtTokens
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.doubleOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.longOrNull
import kotlinx.serialization.json.put
import java.util.Locale

fun fmtUsd(d: Double): String = when {
    d <= 0 -> "$0"
    d < 0.01 -> "<$0.01"
    d < 10 -> String.format(Locale.US, "$%.2f", d)
    else -> String.format(Locale.US, "$%.1f", d)
}

private fun JsonObject.s(k: String): String? = (this[k] as? JsonPrimitive)?.takeIf { it.isString }?.content
private fun JsonObject.l(k: String): Long = (this[k] as? JsonPrimitive)?.longOrNull ?: (this[k] as? JsonPrimitive)?.doubleOrNull?.toLong() ?: 0
private fun JsonObject.d(k: String): Double = (this[k] as? JsonPrimitive)?.doubleOrNull ?: 0.0
private fun JsonObject.b(k: String): Boolean = (this[k] as? JsonPrimitive)?.booleanOrNull == true
private fun JsonObject.arr(k: String): List<JsonObject> = (this[k] as? JsonArray)?.mapNotNull { it as? JsonObject } ?: emptyList()

/** Shown when a Claude Code terminal on the computer has this conversation open. */
@Composable
fun LiveElsewhereBanner(s: SessionSummary?, onOpenTerminal: (() -> Unit)? = null, onFork: () -> Unit) {
    val st = s?.liveElsewhere ?: return
    val ex = LocalExtra.current
    Surface(color = ex.warning.copy(alpha = 0.12f), modifier = Modifier.fillMaxWidth()) {
        Row(Modifier.padding(start = 16.dp, end = 8.dp, top = 6.dp, bottom = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(Icons.Outlined.Terminal, null, Modifier.size(16.dp), tint = ex.warning)
            Spacer(Modifier.width(8.dp))
            Text(
                if (onOpenTerminal != null) "Running in a terminal ($st). It updates here live; type in the terminal, or fork it."
                else "Open in Claude Code on the computer ($st). It updates here live; close it there to continue here, or fork it.",
                style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f),
            )
            if (onOpenTerminal != null) TextButton(onClick = onOpenTerminal) { Text("Terminal") }
            TextButton(onClick = onFork) { Text("Fork") }
        }
    }
}

/** Claude's predicted next prompt; tap to put it in the composer. */
@Composable
fun SuggestionChip(text: String, onClick: () -> Unit) {
    Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 2.dp)) {
        Surface(
            shape = RoundedCornerShape(16.dp), color = MaterialTheme.colorScheme.secondaryContainer.copy(alpha = 0.6f),
            modifier = Modifier.clip(RoundedCornerShape(16.dp)).clickable(onClick = onClick),
        ) {
            Row(Modifier.padding(horizontal = 12.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.AutoAwesome, null, Modifier.size(14.dp), tint = MaterialTheme.colorScheme.secondary)
                Spacer(Modifier.width(6.dp))
                Text(text, style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}

private val palette = listOf(
    Color(0xFF7C9CFF), Color(0xFF4FC3A1), Color(0xFFF4B860), Color(0xFFE57399), Color(0xFF9F7AEA),
    Color(0xFF5BC0EB), Color(0xFFB5C99A), Color(0xFFF08A5D), Color(0xFF6C8EAD), Color(0xFFC3B1E1),
)

/** Claude's /context: what fills the context window, by category. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ContextUsageSheet(conn: HostConnection, sessionId: String, onDismiss: () -> Unit, onCompact: () -> Unit) {
    var data by remember { mutableStateOf<JsonObject?>(null) }
    var err by remember { mutableStateOf<String?>(null) }
    val ex = LocalExtra.current
    LaunchedEffect(sessionId) {
        runCatching {
            data = conn.rpc.call("session.control", params { put("id", sessionId); put("subtype", "get_context_usage") }, timeoutMs = 120_000).jsonObject
        }.onFailure { err = it.message }
    }
    AppSheet(onDismissRequest = onDismiss) {
        Column(Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).padding(bottom = 28.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text("Context usage", style = MaterialTheme.typography.titleMedium)
            val d = data
            when {
                err != null -> Text(err!!, color = MaterialTheme.colorScheme.error)
                d == null -> Box(Modifier.fillMaxWidth().padding(24.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                else -> {
                    val total = d.l("totalTokens")
                    val max = d.l("maxTokens").takeIf { it > 0 } ?: d.l("rawMaxTokens")
                    val pct = d.d("percentage")
                    Text("${fmtTokens(total)} of ${fmtTokens(max)} tokens · ${String.format(Locale.US, "%.0f", pct)}%" +
                        (d.s("model")?.let { " · $it" } ?: ""), style = MaterialTheme.typography.bodyMedium)
                    val cats = d.arr("categories").filter { !it.b("isDeferred") && it.l("tokens") > 0 }
                    if (max > 0) {
                        Row(Modifier.fillMaxWidth().height(14.dp).clip(RoundedCornerShape(7.dp)).background(MaterialTheme.colorScheme.surfaceContainerHighest)) {
                            cats.forEachIndexed { i, c ->
                                val w = c.l("tokens").toFloat() / max
                                if (w > 0.002f && c.s("kind") != "free") {
                                    Box(Modifier.weight(w).fillMaxHeight().background(if (c.s("kind") == "buffer") ex.muted.copy(alpha = 0.4f) else palette[i % palette.size]))
                                }
                            }
                            val used = cats.filter { it.s("kind") != "free" }.sumOf { it.l("tokens") }.toFloat() / max
                            if (used < 1f) Spacer(Modifier.weight(1f - used))
                        }
                    }
                    cats.forEachIndexed { i, c ->
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Box(Modifier.size(10.dp).clip(CircleShape).background(
                                when (c.s("kind")) { "free" -> MaterialTheme.colorScheme.surfaceContainerHighest; "buffer" -> ex.muted.copy(alpha = 0.4f); else -> palette[i % palette.size] }))
                            Spacer(Modifier.width(8.dp))
                            Text(c.s("name").orEmpty(), style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f))
                            Text(fmtTokens(c.l("tokens")), style = MaterialTheme.typography.bodySmall, color = ex.muted)
                        }
                    }
                    val deferred = d.arr("categories").filter { it.b("isDeferred") && it.l("tokens") > 0 }
                    if (deferred.isNotEmpty()) {
                        Text("Loaded on demand: " + deferred.joinToString(" · ") { "${it.s("name")} ${fmtTokens(it.l("tokens"))}" },
                            style = MaterialTheme.typography.labelSmall, color = ex.muted)
                    }
                    val mem = d.arr("memoryFiles")
                    if (mem.isNotEmpty()) {
                        HorizontalDivider()
                        Text("Memory files", style = MaterialTheme.typography.titleSmall)
                        mem.forEach { m ->
                            Row {
                                Text(m.s("path").orEmpty().replace(Regex("^/home/[^/]+"), "~"), style = MaterialTheme.typography.bodySmall,
                                    fontFamily = FontFamily.Monospace, modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
                                Text(fmtTokens(m.l("tokens")), style = MaterialTheme.typography.bodySmall, color = ex.muted)
                            }
                        }
                    }
                    val tools = d.arr("mcpTools").filter { it.b("isLoaded") || !it.containsKey("isLoaded") }.sortedByDescending { it.l("tokens") }.take(12)
                    if (tools.isNotEmpty()) {
                        HorizontalDivider()
                        Text("Largest MCP tools in context", style = MaterialTheme.typography.titleSmall)
                        tools.forEach { t ->
                            Row {
                                Text("${t.s("serverName")} · ${t.s("name")}", style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f),
                                    maxLines = 1, overflow = TextOverflow.Ellipsis)
                                Text(fmtTokens(t.l("tokens")), style = MaterialTheme.typography.bodySmall, color = ex.muted)
                            }
                        }
                    }
                    OutlinedButton(onClick = { onDismiss(); onCompact() }) { Text("Compact conversation now") }
                }
            }
        }
    }
}

/** MCP servers of a Claude session, with their status and tools. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun McpStatusSheet(conn: HostConnection, sessionId: String, onDismiss: () -> Unit) {
    val scope = rememberCoroutineScope()
    var servers by remember { mutableStateOf<List<JsonObject>?>(null) }
    var err by remember { mutableStateOf<String?>(null) }
    var reload by remember { mutableStateOf(0) }
    var busy by remember { mutableStateOf<String?>(null) }
    val ex = LocalExtra.current
    LaunchedEffect(sessionId, reload) {
        runCatching {
            val r = conn.rpc.call("session.control", params { put("id", sessionId); put("subtype", "mcp_status") }, timeoutMs = 120_000).jsonObject
            servers = r.arr("mcpServers")
        }.onFailure { err = it.message }
    }
    fun act(name: String, subtype: String, extra: JsonObject.() -> Unit = {}) {
        busy = name
        scope.launch {
            runCatching {
                conn.rpc.call("session.control", params {
                    put("id", sessionId); put("subtype", subtype)
                    put("params", params { put("serverName", name); if (subtype == "mcp_toggle") put("enabled", servers?.firstOrNull { it.s("name") == name }?.s("status") == "disabled") })
                }, timeoutMs = 120_000)
            }.onFailure { err = it.message }
            busy = null
            reload++
        }
    }
    AppSheet(onDismissRequest = onDismiss) {
        Column(Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).padding(bottom = 28.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text("MCP servers", style = MaterialTheme.typography.titleMedium)
            err?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
            val list = servers
            when {
                list == null && err == null -> Box(Modifier.fillMaxWidth().padding(24.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                list.isNullOrEmpty() -> Text("No MCP servers configured.", style = MaterialTheme.typography.bodySmall)
                else -> list.forEach { sv ->
                    val name = sv.s("name").orEmpty()
                    val status = sv.s("status").orEmpty()
                    val color = when (status) {
                        "connected" -> ex.success
                        "failed" -> ex.danger
                        "needs-auth", "pending" -> ex.warning
                        else -> ex.muted
                    }
                    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Box(Modifier.size(9.dp).clip(CircleShape).background(color))
                            Spacer(Modifier.width(8.dp))
                            Column(Modifier.weight(1f)) {
                                Text(name, style = MaterialTheme.typography.bodyMedium)
                                Text(listOfNotNull(status, sv.arr("tools").size.takeIf { it > 0 }?.let { "$it tools" }, sv.s("scope")).joinToString(" · "),
                                    style = MaterialTheme.typography.labelSmall, color = ex.muted)
                            }
                            if (busy == name) CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
                            else {
                                if (status == "failed" || status == "needs-auth") TextButton(onClick = { act(name, "mcp_reconnect") }) { Text("Reconnect") }
                                Switch(checked = status != "disabled", onCheckedChange = { act(name, "mcp_toggle") })
                            }
                        }
                        sv.s("error")?.let { Text(it, style = MaterialTheme.typography.labelSmall, color = ex.danger, maxLines = 3) }
                    }
                }
            }
        }
    }
}
