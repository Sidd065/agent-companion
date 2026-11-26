package io.github.sidd065.agentcompanion.ui.components

import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
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
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
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
import io.github.sidd065.agentcompanion.core.CodexFlags
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.ui.sessions.DirectoryPicker
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch

/** Compact card showing the saved Codex / OpenRouter flags; tapping opens the editor. */
@Composable
fun CodexFlagsCard(kind: String, onEdit: () -> Unit, modifier: Modifier = Modifier) {
    val flags = LocalContext.current.graph.prefs.flow.collectAsState().value.codexFlags
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
                Text("Codex / OpenRouter flags", style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
                Text("Edit", color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.labelLarge)
            }
            Text(
                if (kind == "tui") flags.tuiPreview() else flags.headlessPreview(),
                style = MaterialTheme.typography.bodySmall,
                fontFamily = FontFamily.Monospace,
                color = if (flags.effectiveSkipPermissions) ex.warning else MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 3,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

private val codexProviders = listOf(
    "" to "Auto",
    "openrouter" to "OpenRouter",
    "openai" to "OpenAI native",
)

private val codexModes = listOf(
    "" to "Default",
    "default" to "Build (Supervised)",
    "plan" to "Plan",
    "acceptEdits" to "Auto-accept edits",
    "auto" to "Auto (Workspace write)",
    "full-access" to "Full access",
)

private val codexEfforts = listOf(
    "" to "Default",
    "low" to "Low",
    "medium" to "Medium",
    "high" to "High",
    "xhigh" to "XHigh",
)

private val codexApprovalPolicies = listOf(
    "" to "From mode",
    "untrusted" to "Untrusted",
    "on-request" to "On request",
    "never" to "Never",
)

private val codexSandboxes = listOf(
    "" to "From mode",
    "read-only" to "Read-only",
    "workspace-write" to "Workspace write",
    "danger-full-access" to "Danger full access",
)

/**
 * Editor for Codex / OpenRouter launch flags. Changes save immediately on the phone
 * and apply to every new Codex session and Codex terminal.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CodexFlagsSheet(conn: HostConnection?, onDismiss: () -> Unit) {
    val g = LocalContext.current.graph
    val scope = rememberCoroutineScope()
    val saved by g.prefs.flow.collectAsState()
    val f = saved.codexFlags
    val ex = LocalExtra.current
    val sheet = rememberModalBottomSheetState(skipPartiallyExpanded = true)

    val providerModels = conn?.providerModels?.collectAsState()?.value.orEmpty()
    val catalog = providerModels["codex"]

    var modelPickerOpen by remember { mutableStateOf(false) }
    var pickingDir by remember { mutableStateOf(false) }
    var gen by remember { mutableStateOf(0) }
    var advanced by remember {
        mutableStateOf(
            f.approvalPolicy.isNotBlank() || f.sandbox.isNotBlank() ||
                f.extraArgs.isNotBlank() || f.tuiExtra.isNotBlank(),
        )
    }

    fun upd(block: (CodexFlags) -> CodexFlags) = scope.launch {
        g.prefs.update { it.copy(codexFlags = block(it.codexFlags)) }
    }

    LaunchedEffect(conn) {
        if (conn != null) {
            runCatching { conn.fetchProviderModels("codex", refresh = false) }
        }
    }

    val defaultModelSlug = catalog?.defaultModelId?.takeIf { it.isNotBlank() } ?: "google/gemini-3-flash-preview"
    val defaultLabel = "Host default · $defaultModelSlug"
    val mergedModels = remember(catalog, saved.customCodexModels) {
        mergePickerModels(catalog, saved.customCodexModels)
    }
    val modelLabel = if (f.model.isBlank()) {
        defaultLabel
    } else {
        mergedModels.firstOrNull { it.modelId == f.model }?.name?.ifBlank { f.model } ?: f.model
    }

    AppSheet(onDismissRequest = onDismiss, sheetState = sheet) {
        Column(
            Modifier
                .fillMaxWidth()
                .imePadding()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp)
                .padding(bottom = 32.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Text("Codex / OpenRouter flags", style = MaterialTheme.typography.titleLarge)
            Text(
                "Saved on this phone and used for every new Codex session and Codex terminal.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
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

            FlagSwitch(
                "--dangerously-bypass-approvals-and-sandbox",
                "Skip all confirmation prompts and run commands without sandboxing",
                f.skipPermissions,
                warn = true,
            ) { v -> upd { it.copy(skipPermissions = v) } }
            if (f.effectiveSkipPermissions) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(Icons.Outlined.WarningAmber, null, Modifier.size(14.dp), tint = ex.warning)
                    Spacer(Modifier.width(6.dp))
                    Text(
                        "Codex runs commands and edits files without asking or sandboxing.",
                        style = MaterialTheme.typography.labelSmall,
                        color = ex.warning,
                    )
                }
            }

            HorizontalDivider()
            Text("--model", style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace)
            OutlinedButton(
                onClick = { modelPickerOpen = true },
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(
                    modelLabel,
                    modifier = Modifier.weight(1f),
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }

            Text("Provider", style = MaterialTheme.typography.labelLarge)
            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                codexProviders.forEach { (k, l) ->
                    FilterChip(
                        selected = f.provider == k,
                        onClick = { upd { it.copy(provider = k) } },
                        label = { Text(l) },
                    )
                }
            }

            Text("Mode", style = MaterialTheme.typography.labelLarge)
            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                codexModes.forEach { (k, l) ->
                    FilterChip(
                        selected = f.mode == k,
                        onClick = { upd { it.copy(mode = k) } },
                        label = { Text(l) },
                    )
                }
            }
            when (f.mode) {
                "default" -> Hint("Supervised build mode: asks before risky shell commands.")
                "plan" -> Hint("Read-only planning mode before applying changes.")
                "acceptEdits" -> Hint("Workspace file edits apply automatically; risky commands still ask.")
                "auto" -> Hint("Workspace-write sandbox with automatic command execution.")
                "full-access" -> Hint("Full access without sandbox restrictions.")
            }

            Text("Reasoning effort", style = MaterialTheme.typography.labelLarge)
            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                codexEfforts.forEach { (k, l) ->
                    FilterChip(
                        selected = f.effort == k,
                        onClick = { upd { it.copy(effort = k) } },
                        label = { Text(l) },
                    )
                }
            }

            Text("--add-dir", style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace)
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                f.addDirs.forEach { d ->
                    AssistChip(
                        onClick = { upd { it.copy(addDirs = it.addDirs - d) } },
                        label = { Text(d) },
                        trailingIcon = { Icon(Icons.Outlined.Close, null, Modifier.size(16.dp)) },
                    )
                }
                AssistChip(
                    onClick = { if (conn != null) pickingDir = true },
                    enabled = conn != null,
                    label = { Text("Add folder") },
                    leadingIcon = { Icon(Icons.Outlined.Folder, null, Modifier.size(16.dp)) },
                )
            }

            HorizontalDivider()
            Row(
                Modifier
                    .fillMaxWidth()
                    .clickable { advanced = !advanced }
                    .padding(vertical = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text("Advanced policy & arguments", style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
                Icon(if (advanced) Icons.Outlined.ExpandLess else Icons.Outlined.ExpandMore, null)
            }
            if (advanced) {
                Text("--ask-for-approval", style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace)
                Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    codexApprovalPolicies.forEach { (k, l) ->
                        FilterChip(
                            selected = f.approvalPolicy == k,
                            onClick = { upd { it.copy(approvalPolicy = k) } },
                            label = { Text(l) },
                        )
                    }
                }

                Text("--sandbox", style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace)
                Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    codexSandboxes.forEach { (k, l) ->
                        FilterChip(
                            selected = f.sandbox == k,
                            onClick = { upd { it.copy(sandbox = k) } },
                            label = { Text(l) },
                        )
                    }
                }

                FlagText(
                    gen = gen,
                    label = "Extra arguments for chat sessions",
                    placeholder = "appended to codex app-server, e.g. --profile ci",
                    value = f.extraArgs,
                    mono = true,
                ) { v -> upd { it.copy(extraArgs = v) } }

                FlagText(
                    gen = gen,
                    label = "Extra arguments for terminals",
                    placeholder = "appended to codex, e.g. --search",
                    value = f.tuiExtra,
                    mono = true,
                ) { v -> upd { it.copy(tuiExtra = v) } }
            }

            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.End)) {
                TextButton(onClick = { upd { CodexFlags() }; gen++ }) { Text("Reset") }
                Button(onClick = onDismiss) { Text("Done") }
            }
            Spacer(Modifier.height(8.dp))
        }
    }

    if (modelPickerOpen) {
        ModelPickerSheet(
            conn = conn,
            currentModelId = f.model,
            allowDefault = true,
            defaultLabel = defaultLabel,
            onSelect = { slug -> upd { it.copy(model = slug) } },
            onDismiss = { modelPickerOpen = false },
        )
    }

    if (pickingDir && conn != null) {
        DirectoryPicker(conn, "", onDismiss = { pickingDir = false }) { d ->
            pickingDir = false
            upd { if (d in it.addDirs) it else it.copy(addDirs = it.addDirs + d) }
        }
    }
}
