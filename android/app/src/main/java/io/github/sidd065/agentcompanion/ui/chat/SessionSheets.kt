package io.github.sidd065.agentcompanion.ui.chat

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.automirrored.outlined.NoteAdd
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.SessionExtras
import io.github.sidd065.agentcompanion.core.SessionSummary
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.components.AppSheet
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.ui.components.copyText
import io.github.sidd065.agentcompanion.ui.components.fmtTokens
import io.github.sidd065.agentcompanion.ui.components.relTime
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put

@Composable
private fun SheetTitle(text: String) {
    Text(text, style = MaterialTheme.typography.titleLarge, modifier = Modifier.padding(start = 20.dp, end = 20.dp, bottom = 8.dp))
}

@Composable
private fun StatusRow(label: String, value: String, mono: Boolean = false, onCopy: (() -> Unit)? = null) {
    Row(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(label, style = MaterialTheme.typography.bodyMedium, color = LocalExtra.current.muted, modifier = Modifier.width(112.dp))
        SelectionContainer(Modifier.weight(1f)) {
            Text(value, style = MaterialTheme.typography.bodyMedium, fontFamily = if (mono) FontFamily.Monospace else null)
        }
        if (onCopy != null) IconButton(onClick = onCopy) { Icon(Icons.Outlined.ContentCopy, "Copy") }
    }
}

/** /status: what this session is and how it's set up. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun StatusSheet(s: SessionSummary?, extras: SessionExtras, agentVersion: String?, host: String, onDismiss: () -> Unit) {
    val ctx = LocalContext.current
    AppSheet(onDismissRequest = onDismiss, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(bottom = 24.dp)) {
            SheetTitle("Status")
            if (s == null) {
                Text("Loading…", Modifier.padding(20.dp))
                return@Column
            }
            val model = s.modelName?.takeIf { it.isNotBlank() } ?: extras.models?.availableModels?.firstOrNull { it.modelId == s.model }?.name ?: s.model
            val mode = extras.modes?.availableModes?.firstOrNull { it.id == s.mode }?.name ?: s.mode
            StatusRow("Agent", listOfNotNull(s.agentLabel, agentVersion).joinToString(" "))
            StatusRow("Computer", host)
            StatusRow("Folder", s.cwd, mono = true)
            if (extras.addDirs.isNotEmpty()) StatusRow("Also", extras.addDirs.joinToString("\n"), mono = true)
            StatusRow("Model", model ?: "default")
            s.thought?.takeIf { it.isNotBlank() }?.let { StatusRow(if (s.isClaude) "Effort" else "Thinking", it) }
            StatusRow("Mode", mode ?: "default")
            StatusRow("Permissions", if (s.skipPermissions) "skipped (--dangerously-skip-permissions)" else "asks before risky actions")
            HorizontalDivider(Modifier.padding(vertical = 6.dp))
            StatusRow("State", listOfNotNull(s.state.ifBlank { null }, s.status, if (s.live) "process ${s.pid}" else "not running").joinToString(" · "))
            StatusRow("Turns", s.turns.toString())
            if (s.contextSize > 0) StatusRow("Context", "${fmtTokens(s.contextUsed)} of ${fmtTokens(s.contextSize)} (${s.contextUsed * 100 / s.contextSize}%)")
            if (s.usage.totalTokens > 0) StatusRow("Tokens", "${fmtTokens(s.usage.inputTokens)} in · ${fmtTokens(s.usage.outputTokens)} out · ${fmtTokens(s.usage.cachedReadTokens)} cached")
            if (s.usage.costUsd > 0) StatusRow("Cost", fmtUsd(s.usage.costUsd))
            StatusRow("Started", if (s.createdAt > 0) relTime(s.createdAt) else "—")
            StatusRow("Updated", if (s.updatedAt > 0) relTime(s.updatedAt) else "—")
            s.forkedFrom?.let { StatusRow("Forked from", it, mono = true) }
            StatusRow("Session ID", s.id, mono = true) { ctx.copyText(s.id, "Session ID") }
        }
    }
}

/** /permissions (Claude Code): the allow, ask and deny rules that apply to this session. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PermissionRulesSheet(conn: HostConnection, sessionId: String, onDismiss: () -> Unit) {
    var rules by remember { mutableStateOf<List<JsonObject>?>(null) }
    var err by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(sessionId) {
        runCatching {
            val r = conn.rpc.call("session.control", params { put("id", sessionId); put("subtype", "list_permission_rules") }).jsonObject
            (r["state"]?.jsonObject?.get("rules") as? JsonArray)?.map { it.jsonObject }.orEmpty()
        }.onSuccess { rules = it }.onFailure { err = it.message }
    }
    AppSheet(onDismissRequest = onDismiss, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.fillMaxWidth().heightIn(min = 200.dp).verticalScroll(rememberScrollState()).padding(bottom = 24.dp)) {
            SheetTitle("Permission rules")
            Text("Rules Claude Code applies in this session, from your settings files. \"Always allow\" on a permission card adds one here.",
                style = MaterialTheme.typography.bodySmall, color = LocalExtra.current.muted, modifier = Modifier.padding(horizontal = 20.dp))
            Spacer(Modifier.padding(4.dp))
            when {
                err != null -> Text(err!!, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(20.dp))
                rules == null -> Text("Loading…", Modifier.padding(20.dp))
                rules!!.isEmpty() -> Text("No rules: Claude asks before anything that needs permission.", Modifier.padding(20.dp))
                else -> for (behavior in listOf("allow", "ask", "deny")) {
                    val group = rules!!.filter { it.str("behavior") == behavior }
                    if (group.isEmpty()) continue
                    Text(
                        when (behavior) { "allow" -> "Allowed without asking"; "ask" -> "Always ask"; else -> "Denied" },
                        style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary,
                        modifier = Modifier.padding(start = 20.dp, top = 12.dp, bottom = 2.dp),
                    )
                    for (r in group) {
                        val d = r["description"]?.jsonObject
                        val desc = if (d != null) listOfNotNull(d.str("prefix"), d.str("emphasis"), d.str("suffix")).joinToString("") else null
                        ListItem(
                            headlineContent = { Text(r.str("rule").orEmpty(), fontFamily = FontFamily.Monospace) },
                            supportingContent = {
                                Text(listOfNotNull(desc, sourceLabel(r.str("source"))).joinToString(" · "), style = MaterialTheme.typography.bodySmall)
                            },
                        )
                    }
                }
            }
        }
    }
}

private fun JsonObject.str(k: String): String? = runCatching { this[k]?.jsonPrimitive?.content }.getOrNull()?.takeIf { it.isNotBlank() }

private fun sourceLabel(src: String?): String? = when (src) {
    "userSettings" -> "your settings (~/.claude/settings.json)"
    "projectSettings" -> "project settings (.claude/settings.json)"
    "localSettings" -> "local project settings (.claude/settings.local.json)"
    "policySettings" -> "managed policy"
    "cliArg" -> "launch flags"
    "session" -> "this session only"
    "command" -> "a command"
    null -> null
    else -> src
}

/** /memory (Claude Code): the CLAUDE.md files Claude reads in this folder, to open or create. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MemorySheet(conn: HostConnection, cwd: String, home: String?, onOpen: (String) -> Unit, onDismiss: () -> Unit) {
    val scope = rememberCoroutineScope()
    val snack = LocalSnack.current
    val files = remember(cwd, home) {
        listOfNotNull(
            "$cwd/CLAUDE.md" to "Project memory, shared with the repository",
            "$cwd/CLAUDE.local.md" to "Project memory just for you (not committed)",
            home?.let { "$it/.claude/CLAUDE.md" to "Your memory for every project" },
        )
    }
    var exists by remember { mutableStateOf<Map<String, Boolean>>(emptyMap()) }
    LaunchedEffect(files) {
        exists = files.associate { (p, _) -> p to runCatching { conn.rpc.call("fs.stat", params { put("path", p) }) }.isSuccess }
    }
    AppSheet(onDismissRequest = onDismiss) {
        Column(Modifier.fillMaxWidth().padding(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            SheetTitle("Memory")
            Text("Claude Code reads these CLAUDE.md files at the start of every session in this folder.",
                style = MaterialTheme.typography.bodySmall, color = LocalExtra.current.muted, modifier = Modifier.padding(horizontal = 20.dp, vertical = 4.dp))
            for ((path, what) in files) {
                val there = exists[path]
                ListItem(
                    headlineContent = { Text(path.removePrefix("$cwd/").let { if (home != null) it.replaceFirst(home, "~") else it }, fontFamily = FontFamily.Monospace) },
                    supportingContent = { Text(what + if (there == false) " · doesn't exist yet" else "") },
                    leadingContent = { Icon(if (there == false) Icons.AutoMirrored.Outlined.NoteAdd else Icons.Outlined.Description, null) },
                    modifier = Modifier.clickable(enabled = there != null) {
                        if (there == true) {
                            onOpen(path)
                        } else {
                            scope.launch {
                                runCatching {
                                    conn.rpc.call("fs.write", params { put("path", path); put("text", ""); put("createDirs", true); put("noOverwrite", true) })
                                }.onSuccess { onOpen(path) }.onFailure { snack.showSnackbar("Create $path: ${it.message}") }
                            }
                        }
                    },
                )
            }
        }
    }
}
