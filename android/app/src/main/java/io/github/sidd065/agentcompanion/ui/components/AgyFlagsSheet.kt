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
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
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
import io.github.sidd065.agentcompanion.core.AgyFlags
import io.github.sidd065.agentcompanion.core.AgyModel
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.decode
import io.github.sidd065.agentcompanion.ui.sessions.DirectoryPicker
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/** Compact card showing the saved agy flags; tapping opens the editor. */
@Composable
fun AgyFlagsCard(kind: String, onEdit: () -> Unit, modifier: Modifier = Modifier) {
    val flags = LocalContext.current.graph.prefs.flow.collectAsState().value.agyFlags
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
                Text("agy flags", style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
                Text("Edit", color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.labelLarge)
            }
            Text(
                if (kind == "tui") flags.tuiPreview() else flags.acpPreview(),
                style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace,
                color = if (flags.skipPermissions) ex.warning else MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 3, overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

private val efforts = listOf("" to "Not set", "low" to "Low", "medium" to "Medium", "high" to "High", "xhigh" to "XHigh", "max" to "Max")
private val modes = listOf("" to "Not set", "default" to "Default", "accept-edits" to "Accept edits", "plan" to "Plan", "auto" to "Auto")

/**
 * Editor for the agy launch flags. Changes save immediately on the phone and
 * apply to every new session and agy terminal.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AgyFlagsSheet(conn: HostConnection?, onDismiss: () -> Unit) {
    val g = LocalContext.current.graph
    val scope = rememberCoroutineScope()
    val saved by g.prefs.flow.collectAsState()
    val f = saved.agyFlags
    val ex = LocalExtra.current
    val sheet = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    var models by remember { mutableStateOf<List<AgyModel>>(emptyList()) }
    // agentc 0.3.3+ marks the model agy's settings.json picks.
    val defaultLabel = models.firstOrNull { it.default }?.let { "agy default · ${it.name}" } ?: "agy default (settings.json)"
    var agents by remember { mutableStateOf<List<String>>(emptyList()) }
    var modelMenu by remember { mutableStateOf(false) }
    var picking by remember { mutableStateOf(false) }
    var gen by remember { mutableStateOf(0) }
    var tuiOpen by remember { mutableStateOf(f.project.isNotBlank() || f.newProject || f.remoteControl || f.remoteAgent || f.settingsJson.isNotBlank() || f.tuiExtra.isNotBlank()) }
    fun upd(block: (AgyFlags) -> AgyFlags) = scope.launch { g.prefs.update { it.copy(agyFlags = block(it.agyFlags)) } }
    LaunchedEffect(conn) {
        if (conn != null) {
            runCatching { models = conn.rpc.call("agy.models").decode(ListSerializer(AgyModel.serializer())) }
            runCatching {
                val out = conn.rpc.call("agy.agents").jsonObject["stdout"]?.jsonPrimitive?.content.orEmpty()
                // `agy agents` prints one agent per line (nothing when there are none).
                agents = out.lines().map { it.trim().trimStart('-', '*', '•').trim() }
                    .filter { it.isNotEmpty() && !it.endsWith(":") && !it.startsWith("No ", true) && !it.startsWith("Available", true) }
                    .map { it.split(Regex("\\s+"), 2).first().trimEnd(':', ',') }
                    .filter { it.isNotEmpty() && it.length <= 40 }
                    .distinct().take(20)
            }
        }
    }

    AppSheet(onDismissRequest = onDismiss, sheetState = sheet) {
        Column(
            Modifier.fillMaxWidth().imePadding().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).padding(bottom = 32.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Text("agy flags", style = MaterialTheme.typography.titleLarge)
            Text("Saved on this phone and used for every new session and agy terminal.", style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant)
            Surface(color = ex.codeBg, shape = RoundedCornerShape(10.dp)) {
                SelectionContainer {
                    Column(Modifier.fillMaxWidth().padding(10.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        Text("Chat sessions", style = MaterialTheme.typography.labelSmall, color = ex.muted)
                        Text(f.acpPreview(), style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
                        Text("Terminals", style = MaterialTheme.typography.labelSmall, color = ex.muted)
                        Text(f.tuiPreview(), style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
                    }
                }
            }

            FlagSwitch("--dangerously-skip-permissions", "Auto-approve every tool call without asking", f.skipPermissions, warn = true) { v -> upd { it.copy(skipPermissions = v) } }
            if (f.skipPermissions) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(Icons.Outlined.WarningAmber, null, Modifier.size(14.dp), tint = ex.warning)
                    Spacer(Modifier.width(6.dp))
                    Text("agy runs commands and edits files without asking (system denylist still applies).", style = MaterialTheme.typography.labelSmall, color = ex.warning)
                }
            }
            FlagSwitch("--sandbox", "Run agent commands in the terminal sandbox", f.sandbox) { v -> upd { it.copy(sandbox = v) } }

            HorizontalDivider()
            Text("--model", style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace)
            Box {
                OutlinedButton(onClick = { modelMenu = true }, modifier = Modifier.fillMaxWidth()) {
                    Text(models.firstOrNull { it.id == f.model }?.name ?: f.model.ifBlank { defaultLabel }, modifier = Modifier.weight(1f))
                }
                DropdownMenu(modelMenu, { modelMenu = false }) {
                    DropdownMenuItem(text = { Text(defaultLabel) }, onClick = { modelMenu = false; upd { it.copy(model = "") } })
                    models.forEach { m -> DropdownMenuItem(text = { Text(m.name) }, onClick = { modelMenu = false; upd { it.copy(model = m.id) } }) }
                    if (models.isEmpty()) DropdownMenuItem(text = { Text("Connect to a machine to list models") }, onClick = { modelMenu = false })
                }
            }
            Text("--effort", style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace)
            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                efforts.forEach { (k, l) -> FilterChip(f.effort == k, onClick = { upd { it.copy(effort = k) } }, label = { Text(l) }) }
            }
            if (f.effort == "xhigh" || f.effort == "max") Hint("Chat sessions use the highest level the model offers (usually High).")
            Text("--mode", style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace)
            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                modes.forEach { (k, l) -> FilterChip(f.mode == k, onClick = { upd { it.copy(mode = k) } }, label = { Text(l) }) }
            }
            when (f.mode) {
                "auto" -> Hint("Auto applies to terminals; chat sessions keep their default mode.")
                "default" -> Hint("Terminals start in agy's default mode (no --mode flag).")
            }
            FlagText(gen = gen, label = "--agent", placeholder = "Custom agent name (from ~/.gemini/config or .agents)", value = f.agent) { v -> upd { it.copy(agent = v) } }
            if (agents.isNotEmpty()) {
                Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    agents.forEach { a -> FilterChip(f.agent == a, onClick = { upd { it.copy(agent = if (it.agent == a) "" else a) }; gen++ }, label = { Text(a) }) }
                }
            }

            Text("--add-dir", style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace)
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                f.addDirs.forEach { d ->
                    AssistChip(onClick = { upd { it.copy(addDirs = it.addDirs - d) } }, label = { Text(d) }, trailingIcon = { Icon(Icons.Outlined.Close, null, Modifier.size(16.dp)) })
                }
                AssistChip(onClick = { if (conn != null) picking = true }, enabled = conn != null, label = { Text("Add folder") }, leadingIcon = { Icon(Icons.Outlined.Folder, null, Modifier.size(16.dp)) })
            }
            FlagSwitch("Auto-allow read requests", "Auto-approve agy file read & search prompts outside the workspace", f.autoAllowReads) { v ->
                upd { it.copy(autoAllowReads = v) }
                if (conn != null && conn.rpc.isConnected) {
                    scope.launch {
                        runCatching {
                            conn.rpc.call("daemon.config.set", io.github.sidd065.agentcompanion.net.params {
                                put("agyAutoAllowReads", kotlinx.serialization.json.JsonPrimitive(v))
                            })
                        }
                    }
                }
            }
            FlagSwitch("--disable-telemetry", "Chat sessions only", f.disableTelemetry) { v -> upd { it.copy(disableTelemetry = v) } }
            FlagSwitch("--include-default-customizations", "Load agy's default customizations (chat sessions)", f.includeDefaultCustomizations) { v -> upd { it.copy(includeDefaultCustomizations = v) } }
            FlagText(gen = gen, label = "Extra arguments for chat sessions", placeholder = "appended to agy acp, e.g. --process-per-session", value = f.acpExtra, mono = true) { v -> upd { it.copy(acpExtra = v) } }

            HorizontalDivider()
            Row(Modifier.fillMaxWidth().clickable { tuiOpen = !tuiOpen }.padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Text("Terminal (TUI) only", style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
                Icon(if (tuiOpen) Icons.Outlined.ExpandLess else Icons.Outlined.ExpandMore, null)
            }
            if (tuiOpen) {
                FlagText(gen = gen, label = "--project", placeholder = "Project ID or name", value = f.project) { v -> upd { it.copy(project = v) } }
                FlagSwitch("--new-project", "Create a new project for the session", f.newProject) { v -> upd { it.copy(newProject = v) } }
                FlagSwitch("--remote-control", "Also open agy's own (Google) remote control", f.remoteControl) { v -> upd { it.copy(remoteControl = v) } }
                FlagSwitch("--remote-agent", "Run conversations on remote agents", f.remoteAgent) { v -> upd { it.copy(remoteAgent = v) } }
                FlagText(gen = gen, label = "--settings", placeholder = "JSON object of settings.json keys, or a file path", value = f.settingsJson, mono = true) { v -> upd { it.copy(settingsJson = v) } }
                FlagText(gen = gen, label = "Extra arguments for terminals", placeholder = "appended to agy, e.g. --log-file /tmp/agy.log", value = f.tuiExtra, mono = true) { v -> upd { it.copy(tuiExtra = v) } }
            }

            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.End)) {
                TextButton(onClick = { upd { AgyFlags() }; gen++ }) { Text("Reset") }
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

@Composable
internal fun Hint(text: String) {
    Text(text, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
}

@Composable
internal fun FlagSwitch(flag: String, desc: String, checked: Boolean, warn: Boolean = false, onChange: (Boolean) -> Unit) {
    val ex = LocalExtra.current
    Row(Modifier.fillMaxWidth().clickable { onChange(!checked) }.padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(flag, style = MaterialTheme.typography.bodyMedium, fontFamily = FontFamily.Monospace, color = if (warn && checked) ex.warning else MaterialTheme.colorScheme.onSurface)
            Text(desc, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Spacer(Modifier.width(12.dp))
        Switch(checked, onChange)
    }
}

@Composable
internal fun FlagText(label: String, placeholder: String, value: String, mono: Boolean = false, gen: Int = 0, onChange: (String) -> Unit) {
    var text by remember(gen) { mutableStateOf(value) }
    OutlinedTextField(
        value = text,
        onValueChange = { text = it; onChange(it) },
        label = { Text(label, fontFamily = if (label.startsWith("--")) FontFamily.Monospace else FontFamily.Default) },
        placeholder = { Text(placeholder) },
        singleLine = !mono,
        textStyle = if (mono) MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace) else MaterialTheme.typography.bodyMedium,
        modifier = Modifier.fillMaxWidth(),
    )
}
