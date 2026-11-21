package io.github.sidd065.agentcompanion.ui.manage

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.PlayArrow
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.HostHealth
import io.github.sidd065.agentcompanion.core.ScheduledTask
import io.github.sidd065.agentcompanion.core.UsageSummary
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.decode
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.put

/**
 * Protocol v2 management cards: Local Usage & Cost, Scheduled Tasks, Linux Host Health/PSI, and Providers.
 */
@Composable
fun WorkbenchV2Section(
    conn: HostConnection,
    reloadKey: Int,
    onOpenSession: (String) -> Unit,
) {
    val snack = LocalSnack.current
    val scope = rememberCoroutineScope()
    val ex = LocalExtra.current
    val agents by conn.agents.collectAsState()
    val projects by conn.projects.collectAsState()

    var days by remember { mutableIntStateOf(30) }
    var usage by remember { mutableStateOf<UsageSummary?>(null) }
    var tasks by remember { mutableStateOf<List<ScheduledTask>>(emptyList()) }
    var health by remember { mutableStateOf<HostHealth?>(null) }
    var localRefresh by remember { mutableIntStateOf(0) }
    var showNewTask by remember { mutableStateOf(false) }

    LaunchedEffect(conn.config.id, reloadKey, localRefresh, days) {
        usage = runCatching {
            conn.rpc.call("usage.summary", params { put("days", days) }).decode(UsageSummary.serializer())
        }.getOrNull()
        tasks = runCatching {
            val obj = conn.rpc.call("schedules.list").jsonObject
            obj["tasks"]?.decode(ListSerializer(ScheduledTask.serializer())) ?: emptyList()
        }.getOrDefault(emptyList())
        health = runCatching {
            conn.rpc.call("daemon.health").decode(HostHealth.serializer())
        }.getOrNull()
    }

    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        // 1. Usage & Cost Card
        Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer)) {
            Column(Modifier.fillMaxWidth().padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
                    Text("Usage & Cost", style = MaterialTheme.typography.titleSmall)
                    Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        listOf(7 to "7d", 30 to "30d", 90 to "90d").forEach { (d, lbl) ->
                            FilterChip(selected = days == d, onClick = { days = d }, label = { Text(lbl) })
                        }
                    }
                }
                val u = usage
                if (u == null) {
                    Text("Requires agentc v2 daemon", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                } else {
                    Text(
                        "${u.turns} turns · ${"%.3f".format(u.costUsd)} USD · ${u.totalTokens / 1000}k tokens",
                        style = MaterialTheme.typography.bodyMedium,
                        fontFamily = FontFamily.Monospace,
                    )
                    u.byAgent.take(4).forEach { b ->
                        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                            Text(b.key.ifBlank { "unknown" }, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
                            Text("${b.turns}t · $${"%.3f".format(b.costUsd)}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                }
            }
        }

        // 2. Scheduled Tasks Card
        Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer)) {
            Column(Modifier.fillMaxWidth().padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
                    Text("Scheduled Tasks (${tasks.size})", style = MaterialTheme.typography.titleSmall)
                    TextButton(onClick = { showNewTask = true }) {
                        Icon(Icons.Outlined.Add, null, Modifier.size(16.dp))
                        Spacer(Modifier.width(4.dp))
                        Text("New")
                    }
                }
                if (tasks.isEmpty()) {
                    Text("No scheduled tasks configured.", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                } else {
                    tasks.forEach { t ->
                        HorizontalDivider()
                        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                            Column(Modifier.weight(1f)) {
                                Text(t.name, style = MaterialTheme.typography.bodyMedium)
                                Text(
                                    "${t.cron ?: "every ${t.intervalSec}s"} · ${t.agent}",
                                    style = MaterialTheme.typography.bodySmall,
                                    fontFamily = FontFamily.Monospace,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                                t.lastSessionId?.takeIf { it.isNotBlank() }?.let { sid ->
                                    TextButton(onClick = { onOpenSession(sid) }) { Text("Open last run") }
                                }
                            }
                            IconButton(onClick = {
                                scope.launch {
                                    runCatching { conn.rpc.call("schedules.runNow", params { put("id", t.id) }) }
                                        .onSuccess { snack.showSnackbar("Triggered ${t.name}"); localRefresh++ }
                                        .onFailure { snack.showSnackbar("Run failed: ${it.message}") }
                                }
                            }) { Icon(Icons.Outlined.PlayArrow, "Run now") }
                            Switch(
                                checked = t.enabled,
                                onCheckedChange = { en ->
                                    scope.launch {
                                        runCatching {
                                            conn.rpc.call("schedules.upsert", params {
                                                put("id", t.id); put("name", t.name); put("enabled", en)
                                                put("kind", t.kind); t.cron?.let { put("cron", it) }
                                                put("cwd", t.cwd); put("agent", t.agent); put("prompt", t.prompt)
                                                put("useWorktree", t.useWorktree)
                                            })
                                            localRefresh++
                                        }
                                    }
                                },
                            )
                            IconButton(onClick = {
                                scope.launch {
                                    runCatching { conn.rpc.call("schedules.delete", params { put("id", t.id) }); localRefresh++ }
                                }
                            }) { Icon(Icons.Outlined.Delete, "Delete") }
                        }
                    }
                }
            }
        }

        // 3. Linux Host Health & PSI Card
        health?.let { h ->
            Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer)) {
                Column(Modifier.fillMaxWidth().padding(14.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text("Host Health & Memory Pressure (PSI)", style = MaterialTheme.typography.titleSmall)
                    Text(
                        "RAM ${"%.1f".format(h.memUsedPct)}% (${h.memAvailableKiB / 1024} MiB free) · PSI ${"%.2f".format(h.psiSomeAvg10)}% · Load ${"%.2f".format(h.loadAvg1)}",
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                        color = if (h.memUsedPct > 85.0) ex.danger else MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    if (h.processes.isNotEmpty()) {
                        HorizontalDivider()
                        h.processes.forEach { p ->
                            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                                Column(Modifier.weight(1f)) {
                                    Text(p.title.ifBlank { p.sessionId }, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                    Text("PID ${p.pid} · ${p.agent} · ${p.rssKiB / 1024} MiB", style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                }
                                OutlinedButton(onClick = {
                                    scope.launch {
                                        runCatching { conn.rpc.call("daemon.killProcess", params { put("pid", p.pid) }) }
                                            .onSuccess { snack.showSnackbar("Killed PID ${p.pid}"); localRefresh++ }
                                            .onFailure { snack.showSnackbar("Kill failed: ${it.message}") }
                                    }
                                }) { Text("Kill") }
                            }
                        }
                    }
                }
            }
        }

        // 4. Configured Providers & ACP Summary
        if (agents.isNotEmpty()) {
            Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer)) {
                Column(Modifier.fillMaxWidth().padding(14.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text("Providers & ACP (${agents.size})", style = MaterialTheme.typography.titleSmall)
                    agents.forEach { ag ->
                        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                            Text("${ag.name.ifBlank { ag.id }} (${ag.driver ?: ag.id})", style = MaterialTheme.typography.bodySmall)
                            Text(
                                if (ag.available) (ag.version ?: "available") else "unavailable",
                                style = MaterialTheme.typography.labelSmall,
                                color = if (ag.available) ex.success else MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
            }
        }
    }

    if (showNewTask) {
        var name by remember { mutableStateOf("") }
        var cron by remember { mutableStateOf("0 9 * * 1-5") }
        var cwd by remember { mutableStateOf(projects.firstOrNull()?.path ?: "~") }
        var agent by remember { mutableStateOf(agents.firstOrNull { it.available }?.id ?: "claude") }
        var prompt by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { showNewTask = false },
            title = { Text("New Scheduled Task") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(name, { name = it }, label = { Text("Name") }, singleLine = true)
                    OutlinedTextField(cron, { cron = it }, label = { Text("Cron (5-field)") }, singleLine = true)
                    OutlinedTextField(cwd, { cwd = it }, label = { Text("Folder") }, singleLine = true)
                    OutlinedTextField(agent, { agent = it }, label = { Text("Agent (claude/agy/codex/…)") }, singleLine = true)
                    OutlinedTextField(prompt, { prompt = it }, label = { Text("Prompt") }, minLines = 2)
                }
            },
            confirmButton = {
                Button(
                    enabled = name.isNotBlank() && prompt.isNotBlank(),
                    onClick = {
                        scope.launch {
                            runCatching {
                                conn.rpc.call("schedules.upsert", params {
                                    put("name", name.trim())
                                    put("enabled", true)
                                    put("kind", "cron")
                                    put("cron", cron.trim())
                                    put("cwd", cwd.trim())
                                    put("agent", agent.trim())
                                    put("prompt", prompt.trim())
                                })
                                showNewTask = false
                                snack.showSnackbar("Saved schedule")
                                localRefresh++
                            }.onFailure { snack.showSnackbar("Save failed: ${it.message}") }
                        }
                    },
                ) { Text("Save") }
            },
            dismissButton = { TextButton(onClick = { showNewTask = false }) { Text("Cancel") } },
        )
    }
}
