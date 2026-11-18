package io.github.sidd065.agentcompanion.ui.components

import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.ExpandLess
import androidx.compose.material.icons.outlined.ExpandMore
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Tune
import androidx.compose.material.icons.outlined.WarningAmber
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
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
import io.github.sidd065.agentcompanion.core.ClaudeFlags
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.ui.sessions.DirectoryPicker
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject

/** A Claude Code model as listed by the daemon (claude.catalog). */
data class ClaudeModelChoice(val value: String, val name: String, val description: String, val efforts: List<String>)

/** The agent's saved launch flags as a compact card; tapping opens the editor. */
@Composable
fun AgentFlagsCard(agent: String, kind: String, onEdit: () -> Unit, modifier: Modifier = Modifier) {
    if (agent != "claude") return AgyFlagsCard(kind, onEdit, modifier)
    val flags = LocalContext.current.graph.prefs.flow.collectAsState().value.claudeFlags
    val ex = LocalExtra.current
    Card(
        modifier.fillMaxWidth().clickable(onClick = onEdit),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
        shape = RoundedCornerShape(14.dp),
    ) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.Tune, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.primary)
                Spacer(Modifier.width(8.dp))
                Text("Claude Code flags", style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
                Text("Edit", color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.labelLarge)
            }
            Text(
                if (kind == "tui") flags.tuiPreview() else flags.headlessPreview(),
                style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace,
                color = if (flags.skipPermissions && flags.mode.isBlank()) ex.warning else MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 3, overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** Flags editor for an agent. */
@Composable
fun AgentFlagsSheet(agent: String, conn: HostConnection?, onDismiss: () -> Unit) {
    if (agent == "claude") ClaudeFlagsSheet(conn, onDismiss) else AgyFlagsSheet(conn, onDismiss)
}

private val claudeEfforts = listOf("" to "Default", "low" to "Low", "medium" to "Medium", "high" to "High", "xhigh" to "XHigh", "max" to "Max")
private val claudeModes = listOf(
    "" to "Default", "acceptEdits" to "Accept edits", "plan" to "Plan", "auto" to "Auto", "dontAsk" to "Don't ask", "bypassPermissions" to "Bypass",
)

/**
 * Editor for Claude Code launch flags. Changes save immediately on the phone
 * and apply to every new Claude session and Claude terminal.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ClaudeFlagsSheet(conn: HostConnection?, onDismiss: () -> Unit) {
    val g = LocalContext.current.graph
    val scope = rememberCoroutineScope()
    val saved by g.prefs.flow.collectAsState()
    val f = saved.claudeFlags
    val ex = LocalExtra.current
    val sheet = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    var models by remember { mutableStateOf<List<ClaudeModelChoice>>(emptyList()) }
    var agents by remember { mutableStateOf<List<Pair<String, String>>>(emptyList()) }
    var modelMenu by remember { mutableStateOf(false) }
    var fallbackMenu by remember { mutableStateOf(false) }
    var picking by remember { mutableStateOf(false) }
    var gen by remember { mutableStateOf(0) }
    var advanced by remember {
        mutableStateOf(f.allowedTools.isNotBlank() || f.disallowedTools.isNotBlank() || f.appendSystemPrompt.isNotBlank() || f.fallbackModel.isNotBlank() ||
            f.maxBudgetUsd.isNotBlank() || f.mcpConfig.isNotBlank() || f.strictMcpConfig || f.settingsJson.isNotBlank() || f.extra.isNotBlank())
    }
    var tuiOpen by remember { mutableStateOf(f.worktree || f.tuiExtra.isNotBlank()) }
    fun upd(block: (ClaudeFlags) -> ClaudeFlags) = scope.launch { g.prefs.update { it.copy(claudeFlags = block(it.claudeFlags)) } }
    LaunchedEffect(conn) {
        if (conn != null) {
            runCatching {
                val cat = conn.rpc.call("claude.catalog", timeoutMs = 120_000).jsonObject
                models = cat["models"]?.jsonArray?.mapNotNull { m ->
                    val o = m.jsonObject
                    val v = o.str("value") ?: return@mapNotNull null
                    ClaudeModelChoice(v, o.str("displayName") ?: v, o.str("description").orEmpty(),
                        (o["supportedEffortLevels"] as? JsonArray)?.mapNotNull { (it as? kotlinx.serialization.json.JsonPrimitive)?.content } ?: emptyList())
                } ?: emptyList()
                agents = cat["agents"]?.jsonArray?.mapNotNull { a -> a.jsonObject.str("name")?.let { it to a.jsonObject.str("description").orEmpty() } } ?: emptyList()
            }
        }
    }
    val modelLabel = models.firstOrNull { it.value == f.model }?.name ?: f.model.ifBlank { "Claude's default (settings.json)" }
    AppSheet(onDismissRequest = onDismiss, sheetState = sheet) {
        Column(
            Modifier.fillMaxWidth().imePadding().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).padding(bottom = 32.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Text("Claude Code flags", style = MaterialTheme.typography.titleLarge)
            Text("Saved on this phone and used for every new Claude session and Claude terminal.", style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant)
            Surface(color = ex.codeBg, shape = RoundedCornerShape(10.dp)) {
                SelectionContainer {
                    Column(Modifier.fillMaxWidth().padding(10.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        Text("Chat sessions", style = MaterialTheme.typography.labelSmall, color = ex.muted)
                        Text(f.headlessPreview(), style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
                        Text("Terminals", style = MaterialTheme.typography.labelSmall, color = ex.muted)
                        Text(f.tuiPreview(), style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
                    }
                }
            }

            FlagSwitch("--dangerously-skip-permissions", "Run every tool without asking (bypass permissions mode)", f.skipPermissions, warn = true) { v -> upd { it.copy(skipPermissions = v) } }
            if (f.skipPermissions) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(Icons.Outlined.WarningAmber, null, Modifier.size(14.dp), tint = ex.warning)
                    Spacer(Modifier.width(6.dp))
                    Text(
                        if (f.mode.isNotBlank()) "Starts in the mode below; you can switch to Bypass from a session's mode menu."
                        else "Claude runs commands and edits files without asking.",
                        style = MaterialTheme.typography.labelSmall, color = ex.warning,
                    )
                }
            }

            HorizontalDivider()
            Text("--model", style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace)
            Box {
                OutlinedButton(onClick = { modelMenu = true }, modifier = Modifier.fillMaxWidth()) { Text(modelLabel, modifier = Modifier.weight(1f)) }
                DropdownMenu(modelMenu, { modelMenu = false }) {
                    DropdownMenuItem(text = { Text("Claude's default (settings.json)") }, onClick = { modelMenu = false; upd { it.copy(model = "") } })
                    models.filter { it.value != "default" }.forEach { m ->
                        DropdownMenuItem(
                            text = { Column { Text(m.name); if (m.description.isNotBlank()) Text(m.description, style = MaterialTheme.typography.labelSmall, color = ex.muted) } },
                            onClick = { modelMenu = false; upd { it.copy(model = m.value) } },
                        )
                    }
                    if (models.isEmpty()) DropdownMenuItem(text = { Text("Connect to a computer to list models") }, onClick = { modelMenu = false })
                }
            }
            Text("--effort", style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace)
            val supported = models.firstOrNull { it.value == f.model.ifBlank { "default" } }?.efforts.orEmpty()
            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                claudeEfforts.forEach { (k, l) ->
                    FilterChip(f.effort == k, onClick = { upd { it.copy(effort = k) } }, label = { Text(l) },
                        enabled = k.isEmpty() || supported.isEmpty() || k in supported)
                }
            }
            Text("--permission-mode", style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace)
            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                claudeModes.forEach { (k, l) -> FilterChip(f.mode == k, onClick = { upd { it.copy(mode = k) } }, label = { Text(l) }) }
            }
            when (f.mode) {
                "plan" -> Hint("Claude researches and proposes a plan for you to approve before changing anything.")
                "acceptEdits" -> Hint("File edits are applied without asking; commands still ask.")
                "auto" -> Hint("A classifier approves safe actions and asks about risky ones (models that support it).")
                "dontAsk" -> Hint("Anything not already allowed is denied instead of asking.")
                "" -> if (!f.skipPermissions) Hint("Claude asks before editing files or running commands.")
            }
            FlagText(gen = gen, label = "--agent", placeholder = "Run the session as a custom agent", value = f.agent) { v -> upd { it.copy(agent = v) } }
            if (agents.isNotEmpty()) {
                Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    agents.forEach { (a, _) -> FilterChip(f.agent == a, onClick = { upd { it.copy(agent = if (it.agent == a) "" else a) }; gen++ }, label = { Text(a) }) }
                }
            }
            Text("--add-dir", style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace)
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                f.addDirs.forEach { d ->
                    AssistChip(onClick = { upd { it.copy(addDirs = it.addDirs - d) } }, label = { Text(d) }, trailingIcon = { Icon(Icons.Outlined.Close, null, Modifier.size(16.dp)) })
                }
                AssistChip(onClick = { if (conn != null) picking = true }, enabled = conn != null, label = { Text("Add folder") }, leadingIcon = { Icon(Icons.Outlined.Folder, null, Modifier.size(16.dp)) })
            }

            HorizontalDivider()
            Row(Modifier.fillMaxWidth().clickable { advanced = !advanced }.padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Text("Tools, prompts and limits", style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
                Icon(if (advanced) Icons.Outlined.ExpandLess else Icons.Outlined.ExpandMore, null)
            }
            if (advanced) {
                FlagText(gen = gen, label = "--allowedTools", placeholder = "e.g. Bash(git:*) Edit  (always allowed)", value = f.allowedTools, mono = true) { v -> upd { it.copy(allowedTools = v) } }
                FlagText(gen = gen, label = "--disallowedTools", placeholder = "e.g. WebFetch Bash(rm:*)", value = f.disallowedTools, mono = true) { v -> upd { it.copy(disallowedTools = v) } }
                FlagText(gen = gen, label = "--append-system-prompt", placeholder = "Extra instructions for every session", value = f.appendSystemPrompt, mono = true) { v -> upd { it.copy(appendSystemPrompt = v) } }
                Text("--fallback-model", style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace)
                Box {
                    OutlinedButton(onClick = { fallbackMenu = true }, modifier = Modifier.fillMaxWidth()) {
                        Text(models.firstOrNull { it.value == f.fallbackModel }?.name ?: f.fallbackModel.ifBlank { "None" }, modifier = Modifier.weight(1f))
                    }
                    DropdownMenu(fallbackMenu, { fallbackMenu = false }) {
                        DropdownMenuItem(text = { Text("None") }, onClick = { fallbackMenu = false; upd { it.copy(fallbackModel = "") } })
                        models.filter { it.value != "default" }.forEach { m ->
                            DropdownMenuItem(text = { Text(m.name) }, onClick = { fallbackMenu = false; upd { it.copy(fallbackModel = m.value) } })
                        }
                    }
                }
                Hint("Used automatically when the main model is overloaded.")
                FlagText(gen = gen, label = "--max-budget-usd", placeholder = "Stop a chat session after spending this much (e.g. 5)", value = f.maxBudgetUsd) { v -> upd { it.copy(maxBudgetUsd = v) } }
                FlagText(gen = gen, label = "--mcp-config", placeholder = "MCP servers: JSON or a file path", value = f.mcpConfig, mono = true) { v -> upd { it.copy(mcpConfig = v) } }
                FlagSwitch("--strict-mcp-config", "Only use the MCP servers from --mcp-config", f.strictMcpConfig) { v -> upd { it.copy(strictMcpConfig = v) } }
                FlagText(gen = gen, label = "--settings", placeholder = "JSON object of settings, or a file path", value = f.settingsJson, mono = true) { v -> upd { it.copy(settingsJson = v) } }
                FlagText(gen = gen, label = "Extra arguments for chat sessions", placeholder = "appended to claude -p, e.g. --verbose", value = f.extra, mono = true) { v -> upd { it.copy(extra = v) } }
            }

            HorizontalDivider()
            Row(Modifier.fillMaxWidth().clickable { tuiOpen = !tuiOpen }.padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Text("Terminal (TUI) only", style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
                Icon(if (tuiOpen) Icons.Outlined.ExpandLess else Icons.Outlined.ExpandMore, null)
            }
            if (tuiOpen) {
                FlagSwitch("--worktree", "Start new terminals in a fresh git worktree", f.worktree) { v -> upd { it.copy(worktree = v) } }
                FlagText(gen = gen, label = "Extra arguments for terminals", placeholder = "appended to claude, e.g. --ide", value = f.tuiExtra, mono = true) { v -> upd { it.copy(tuiExtra = v) } }
            }

            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.End)) {
                TextButton(onClick = { upd { ClaudeFlags() }; gen++ }) { Text("Reset") }
                Button(onClick = onDismiss) { Text("Done") }
            }
            Spacer(Modifier.height(8.dp))
        }
    }
    if (picking && conn != null) {
        DirectoryPicker(conn, "", onDismiss = { picking = false }) { d ->
            picking = false
            upd { if (d in it.addDirs) it else it.copy(addDirs = it.addDirs + d) }
        }
    }
}

private fun kotlinx.serialization.json.JsonObject.str(k: String): String? =
    (this[k] as? kotlinx.serialization.json.JsonPrimitive)?.takeIf { it.isString }?.content
