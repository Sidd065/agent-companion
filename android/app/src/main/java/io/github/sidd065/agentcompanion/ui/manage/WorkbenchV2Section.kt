package io.github.sidd065.agentcompanion.ui.manage

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.PlayArrow
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.AssistChip
import androidx.compose.material3.AssistChipDefaults
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.InputChip
import androidx.compose.material3.InputChipDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.foundation.verticalScroll
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.AgentProfile
import io.github.sidd065.agentcompanion.core.AppJson
import io.github.sidd065.agentcompanion.core.CustomModel
import io.github.sidd065.agentcompanion.core.HostHealth
import io.github.sidd065.agentcompanion.core.ProviderConfig
import io.github.sidd065.agentcompanion.core.ProviderHealth
import io.github.sidd065.agentcompanion.core.ProvidersListResult
import io.github.sidd065.agentcompanion.core.ScheduledTask
import io.github.sidd065.agentcompanion.core.UsageSummary
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.decode
import io.github.sidd065.agentcompanion.net.params
import kotlinx.serialization.json.JsonObject
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.ui.components.Pill
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.put

/**
 * Pure helper for building the `providers.upsert` payload for Codex / OpenRouter
 * while strictly preserving `"***"` secrets (or `env = null` when no prior env exists)
 * unless the user explicitly enters a new key or clicks "Clear stored key".
 */
internal fun buildCodexProviderUpsertConfig(
    existing: ProviderConfig?,
    agentProfile: AgentProfile?,
    typedApiKey: String,
    clearStoredKey: Boolean,
    defaultModel: String,
    customModels: List<CustomModel>,
): ProviderConfig {
    val cleanedCustoms = customModels
        .map { CustomModel(slug = it.slug.trim(), name = it.name.trim()) }
        .filter { it.slug.isNotEmpty() }
        .distinctBy { it.slug }
    val trimmedKey = typedApiKey.trim()
    val nextEnv: Map<String, String>? = when {
        clearStoredKey -> {
            val m = (existing?.env ?: emptyMap()).toMutableMap()
            m["OPENROUTER_API_KEY"] = ""
            m
        }
        trimmedKey.isNotEmpty() && trimmedKey != "***" -> {
            val m = (existing?.env ?: emptyMap()).toMutableMap()
            m["OPENROUTER_API_KEY"] = trimmedKey
            m
        }
        existing != null -> {
            val prevEnv = existing.env
            if (prevEnv == null) {
                null
            } else {
                val m = prevEnv.toMutableMap()
                if ("OPENROUTER_API_KEY" in prevEnv) {
                    m["OPENROUTER_API_KEY"] = "***"
                }
                m
            }
        }
        else -> null
    }
    return if (existing != null) {
        existing.copy(
            env = nextEnv,
            defaultModel = defaultModel.trim(),
            customModels = cleanedCustoms,
        )
    } else {
        ProviderConfig(
            id = "codex",
            name = agentProfile?.name?.ifBlank { "Codex" } ?: "Codex",
            driver = agentProfile?.driver?.ifBlank { "codex" } ?: "codex",
            enabled = true,
            env = nextEnv,
            defaultModel = defaultModel.trim(),
            customModels = cleanedCustoms,
            builtin = agentProfile?.builtin ?: true,
            features = agentProfile?.features.orEmpty(),
        )
    }
}

/**
 * Protocol v2 management cards: Local Usage & Cost, Scheduled Tasks, Linux Host Health/PSI, and Providers.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun WorkbenchV2Section(
    conn: HostConnection,
    reloadKey: Int,
    onOpenSession: (String) -> Unit,
) {
    val ctx = LocalContext.current
    val g = ctx.graph
    val snack = LocalSnack.current
    val scope = rememberCoroutineScope()
    val ex = LocalExtra.current
    val settings by g.prefs.flow.collectAsState()
    val agents by conn.agents.collectAsState()
    val projects by conn.projects.collectAsState()
    val providerModelsMap by conn.providerModels.collectAsState()
    val codexModels = providerModelsMap["codex"]

    var rangeKey by remember { mutableStateOf("30d") }
    var usage by remember(conn.config.id) { mutableStateOf<UsageSummary?>(null) }
    var tasks by remember(conn.config.id) { mutableStateOf<List<ScheduledTask>>(emptyList()) }
    var health by remember(conn.config.id) { mutableStateOf<HostHealth?>(null) }
    var providersList by remember(conn.config.id) { mutableStateOf<ProvidersListResult?>(null) }
    var providerHealth by remember(conn.config.id) { mutableStateOf<List<ProviderHealth>>(emptyList()) }
    var refreshingModels by remember(conn.config.id) { mutableStateOf(false) }
    var checkingHealth by remember(conn.config.id) { mutableStateOf(false) }
    var localRefresh by remember(conn.config.id) { mutableIntStateOf(0) }
    var showNewTask by remember(conn.config.id) { mutableStateOf(false) }
    var showAddCustomModel by remember(conn.config.id) { mutableStateOf(false) }
    var showConfigureCodex by remember(conn.config.id) { mutableStateOf(false) }

    LaunchedEffect(conn.config.id, reloadKey, localRefresh, rangeKey) {
        usage = runCatching {
            val p = params {
                when (rangeKey) {
                    "5h" -> put("hours", 5)
                    "12h" -> put("hours", 12)
                    "24h" -> put("hours", 24)
                    "7d" -> put("days", 7)
                    "30d" -> put("days", 30)
                    "90d" -> put("days", 90)
                    "180d" -> put("days", 180)
                    "all" -> put("allTime", true)
                    else -> put("days", 30)
                }
            }
            conn.rpc.call("usage.summary", p).decode(UsageSummary.serializer())
        }.getOrNull()
        tasks = runCatching {
            val obj = conn.rpc.call("schedules.list").jsonObject
            obj["tasks"]?.decode(ListSerializer(ScheduledTask.serializer())) ?: emptyList()
        }.getOrDefault(emptyList())
        health = runCatching {
            conn.rpc.call("daemon.health").decode(HostHealth.serializer())
        }.getOrNull()
    }

    LaunchedEffect(conn.config.id, reloadKey, localRefresh) {
        providersList = runCatching {
            conn.rpc.call("providers.list").decode(ProvidersListResult.serializer())
        }.getOrNull()
        providerHealth = runCatching {
            (conn.rpc.call("providers.health") as? JsonArray)
                ?.decode(ListSerializer(ProviderHealth.serializer()))
                ?: emptyList()
        }.getOrDefault(emptyList())
        runCatching { conn.fetchProviderModels("codex", refresh = false) }
    }

    val codexConfig = providersList?.configs?.firstOrNull { it.id == "codex" }
    val codexAgent = (providersList?.agents?.firstOrNull { it.id == "codex" })
        ?: agents.firstOrNull { it.id == "codex" }
    val customModels = remember(codexModels, codexConfig, settings.customCodexModels) {
        (codexModels?.customModels.orEmpty() + codexConfig?.customModels.orEmpty() + settings.customCodexModels)
            .map { CustomModel(slug = it.slug.trim(), name = it.name.trim()) }
            .filter { it.slug.isNotEmpty() }
            .distinctBy { it.slug }
    }

    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        // 1. Usage & Cost Card
        Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer)) {
            Column(Modifier.fillMaxWidth().padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("Usage & Cost", style = MaterialTheme.typography.titleSmall)
                FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    listOf("5h" to "5h", "12h" to "12h", "24h" to "24h", "7d" to "7d", "30d" to "30d", "90d" to "90d", "180d" to "180d", "all" to "All time").forEach { (k, lbl) ->
                        FilterChip(selected = rangeKey == k, onClick = { rangeKey = k }, label = { Text(lbl) })
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
                                            conn.rpc.call("schedules.upsert", buildScheduleTogglePayload(t, en))
                                            localRefresh++
                                        }.onFailure {
                                            snack.showSnackbar("Schedule update failed: ${it.message}")
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

        // 4. Interactive Providers & OpenRouter Card
        val displayAgents = providersList?.agents?.ifEmpty { agents } ?: agents
        if (displayAgents.isNotEmpty() || providersList != null || codexModels != null) {
            ProvidersCard(
                conn = conn,
                agents = displayAgents,
                codexConfig = codexConfig,
                codexModelsCount = codexModels?.models?.size,
                openRouterReady = codexModels?.openRouterConfigured == true ||
                    conn.openRouterConfigured ||
                    codexConfig?.env?.get("OPENROUTER_API_KEY")?.isNotBlank() == true,
                nativeAuthReady = codexModels?.nativeAuthConfigured == true,
                defaultModel = codexConfig?.defaultModel?.ifBlank { codexModels?.defaultModelId.orEmpty() }
                    ?: codexModels?.defaultModelId.orEmpty(),
                customModels = customModels,
                providerHealth = providerHealth,
                refreshingModels = refreshingModels,
                checkingHealth = checkingHealth,
                onRefreshModels = {
                    if (!refreshingModels) {
                        refreshingModels = true
                        scope.launch {
                            runCatching { conn.fetchProviderModels("codex", refresh = true) }
                                .onSuccess { snack.showSnackbar("Loaded ${it.models.size} Codex / OpenRouter models") }
                                .onFailure { snack.showSnackbar("Refresh failed: ${it.message}") }
                            refreshingModels = false
                        }
                    }
                },
                onCheckHealth = {
                    if (!checkingHealth) {
                        checkingHealth = true
                        scope.launch {
                            runCatching {
                                val list = (conn.rpc.call("providers.health") as? JsonArray)
                                    ?.decode(ListSerializer(ProviderHealth.serializer()))
                                    ?: emptyList()
                                providerHealth = list
                                list
                            }.onSuccess { list ->
                                val okCount = list.count { it.available }
                                snack.showSnackbar("Health checked: $okCount/${list.size} providers available")
                            }.onFailure { snack.showSnackbar("Health check failed: ${it.message}") }
                            checkingHealth = false
                        }
                    }
                },
                onAddCustomModel = { showAddCustomModel = true },
                onRemoveCustomModel = { slug ->
                    val remaining = customModels.filterNot { it.slug == slug }
                    g.scope.launch {
                        runCatching { conn.saveProviderCustomModels("codex", remaining) }
                            .onFailure { scope.launch { snack.showSnackbar("Remove failed: ${it.message}") } }
                        g.prefs.update { it.withoutCustomCodexModel(slug) }
                        localRefresh++
                    }
                },
                onConfigureCodex = { showConfigureCodex = true },
            )
        }
    }

    if (showNewTask) {
        var name by remember(conn.config.id) { mutableStateOf("") }
        var cron by remember(conn.config.id) { mutableStateOf("0 9 * * 1-5") }
        var cwd by remember(conn.config.id) { mutableStateOf(projects.firstOrNull()?.path ?: "~") }
        var agent by remember(conn.config.id) { mutableStateOf(agents.firstOrNull { it.available }?.id ?: "claude") }
        var model by remember(conn.config.id) { mutableStateOf("") }
        var prompt by remember(conn.config.id) { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { showNewTask = false },
            title = { Text("New Scheduled Task") },
            text = {
                Column(Modifier.verticalScroll(androidx.compose.foundation.rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(name, { name = it }, label = { Text("Name") }, singleLine = true)
                    OutlinedTextField(cron, { cron = it }, label = { Text("Cron (5-field)") }, singleLine = true)
                    OutlinedTextField(cwd, { cwd = it }, label = { Text("Folder") }, singleLine = true)
                    OutlinedTextField(agent, { agent = it }, label = { Text("Agent (claude/agy/codex/…)") }, singleLine = true)
                    OutlinedTextField(model, { model = it }, label = { Text("Model slug (optional)") }, singleLine = true)
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
                                    model.trim().takeIf { it.isNotEmpty() }?.let { put("model", it) }
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

    if (showAddCustomModel) {
        var slug by remember(conn.config.id) { mutableStateOf("") }
        var label by remember(conn.config.id) { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { showAddCustomModel = false },
            title = { Text("Add Custom Model") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(
                        value = slug,
                        onValueChange = { slug = it },
                        label = { Text("Model slug") },
                        placeholder = { Text("openrouter/google/gemini-2.5-pro:nitro") },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth(),
                    )
                    OutlinedTextField(
                        value = label,
                        onValueChange = { label = it },
                        label = { Text("Display name (optional)") },
                        placeholder = { Text("Gemini 2.5 Pro (Nitro)") },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth(),
                    )
                    Text(
                        "Exact slugs (including `-latest` and `:free` / `:nitro` / `:exacto` suffixes) are preserved verbatim.",
                        style = MaterialTheme.typography.labelSmall,
                        color = ex.muted,
                    )
                }
            },
            confirmButton = {
                Button(
                    enabled = slug.trim().isNotEmpty(),
                    onClick = {
                        val cleanSlug = slug.trim()
                        val cleanName = label.trim()
                        val updated = (customModels.filterNot { it.slug == cleanSlug } + CustomModel(cleanSlug, cleanName))
                        showAddCustomModel = false
                        g.scope.launch {
                            runCatching { conn.saveProviderCustomModels("codex", updated) }
                                .onSuccess { scope.launch { snack.showSnackbar("Saved custom model $cleanSlug") } }
                                .onFailure { scope.launch { snack.showSnackbar("Save failed: ${it.message}") } }
                            g.prefs.update {
                                it.withRecentCodexModel(cleanSlug, customName = cleanName.ifBlank { null }, isCustom = true)
                            }
                            localRefresh++
                        }
                    },
                ) { Text("Add") }
            },
            dismissButton = { TextButton(onClick = { showAddCustomModel = false }) { Text("Cancel") } },
        )
    }

    if (showConfigureCodex) {
        val hasStoredKey = codexConfig?.env?.get("OPENROUTER_API_KEY")?.isNotBlank() == true || conn.openRouterConfigured
        var apiKey by remember(conn.config.id) { mutableStateOf("") }
        var clearKey by remember(conn.config.id) { mutableStateOf(false) }
        var defaultModel by remember(conn.config.id) { mutableStateOf(codexConfig?.defaultModel.orEmpty()) }
        AlertDialog(
            onDismissRequest = { showConfigureCodex = false },
            title = { Text("Configure Codex & OpenRouter") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(
                        value = apiKey,
                        onValueChange = {
                            apiKey = it
                            if (it.isNotBlank()) clearKey = false
                        },
                        label = { Text("OPENROUTER_API_KEY") },
                        placeholder = {
                            Text(
                                if (hasStoredKey && !clearKey) {
                                    "Configured (*** — leave blank to keep)"
                                } else {
                                    "sk-or-v1-…"
                                },
                            )
                        },
                        visualTransformation = PasswordVisualTransformation(),
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth(),
                    )
                    if (hasStoredKey) {
                        FilterChip(
                            selected = clearKey,
                            onClick = {
                                clearKey = !clearKey
                                if (clearKey) apiKey = ""
                            },
                            label = { Text(if (clearKey) "Will clear stored key on save" else "Clear stored key") },
                        )
                    }
                    OutlinedTextField(
                        value = defaultModel,
                        onValueChange = { defaultModel = it },
                        label = { Text("Default model (optional)") },
                        placeholder = { Text("openrouter/google/gemini-2.5-pro") },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            },
            confirmButton = {
                Button(
                    onClick = {
                        val payloadCfg = buildCodexProviderUpsertConfig(
                            existing = codexConfig,
                            agentProfile = codexAgent,
                            typedApiKey = apiKey,
                            clearStoredKey = clearKey,
                            defaultModel = defaultModel,
                            customModels = customModels,
                        )
                        showConfigureCodex = false
                        g.scope.launch {
                            runCatching {
                                val json = AppJson.encodeToJsonElement(ProviderConfig.serializer(), payloadCfg).jsonObject
                                conn.rpc.call("providers.upsert", json)
                                conn.fetchProviderModels("codex", refresh = true)
                            }.onSuccess {
                                scope.launch { snack.showSnackbar("Saved Codex / OpenRouter configuration") }
                                localRefresh++
                            }.onFailure {
                                scope.launch { snack.showSnackbar("Save failed: ${it.message}") }
                            }
                        }
                    },
                ) { Text("Save") }
            },
            dismissButton = { TextButton(onClick = { showConfigureCodex = false }) { Text("Cancel") } },
        )
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun ProvidersCard(
    conn: HostConnection,
    agents: List<AgentProfile>,
    codexConfig: ProviderConfig?,
    codexModelsCount: Int?,
    openRouterReady: Boolean,
    nativeAuthReady: Boolean,
    defaultModel: String,
    customModels: List<CustomModel>,
    providerHealth: List<ProviderHealth>,
    refreshingModels: Boolean,
    checkingHealth: Boolean,
    onRefreshModels: () -> Unit,
    onCheckHealth: () -> Unit,
    onAddCustomModel: () -> Unit,
    onRemoveCustomModel: (String) -> Unit,
    onConfigureCodex: () -> Unit,
) {
    val ex = LocalExtra.current
    val healthById = remember(providerHealth) { providerHealth.associateBy { it.id } }
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer)) {
        Column(Modifier.fillMaxWidth().padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
                Text(
                    if (agents.isNotEmpty()) "Providers & OpenRouter (${agents.size})" else "Providers & OpenRouter",
                    style = MaterialTheme.typography.titleSmall,
                )
                Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                    TextButton(onClick = onCheckHealth, enabled = !checkingHealth) {
                        Text(if (checkingHealth) "Checking…" else "Check health")
                    }
                    TextButton(onClick = onRefreshModels, enabled = !refreshingModels) {
                        Text(if (refreshingModels) "Refreshing…" else "Refresh")
                    }
                }
            }

            agents.forEach { ag ->
                val h = healthById[ag.id]
                val avail = h?.available ?: ag.available
                val ver = h?.version?.ifBlank { ag.version } ?: ag.version
                Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
                        Text("${ag.name.ifBlank { ag.id }} (${ag.driver?.ifBlank { ag.id } ?: ag.id})", style = MaterialTheme.typography.bodySmall)
                        Text(
                            if (avail) (ver?.ifBlank { "available" } ?: "available") else "unavailable",
                            style = MaterialTheme.typography.labelSmall,
                            color = if (avail) ex.success else MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    if (h != null && (h.authStatus.isNotBlank() || h.message.isNotBlank() || h.latencyMs > 0)) {
                        Text(
                            listOfNotNull(
                                h.authStatus.takeIf { it.isNotBlank() },
                                h.latencyMs.takeIf { it > 0 }?.let { "${it}ms" },
                                h.message.takeIf { it.isNotBlank() },
                            ).joinToString(" · "),
                            style = MaterialTheme.typography.labelSmall,
                            color = ex.muted,
                            maxLines = 2,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
            }

            HorizontalDivider()

            Text("Codex & OpenRouter", style = MaterialTheme.typography.labelLarge)
            FlowRow(
                horizontalArrangement = Arrangement.spacedBy(6.dp),
                verticalArrangement = Arrangement.spacedBy(6.dp),
                itemVerticalAlignment = Alignment.CenterVertically,
            ) {
                Pill(
                    if (openRouterReady) "OpenRouter ready" else "OpenRouter not configured",
                    if (openRouterReady) ex.success else ex.muted,
                )
                if (nativeAuthReady) {
                    Pill("OpenAI auth", ex.success)
                }
                codexModelsCount?.let { count ->
                    Pill("$count models", MaterialTheme.colorScheme.primary)
                }
            }

            if (defaultModel.isNotBlank()) {
                Text(
                    "Default model: $defaultModel",
                    style = MaterialTheme.typography.labelSmall,
                    fontFamily = FontFamily.Monospace,
                    color = ex.muted,
                )
            }

            Text("Custom OpenRouter / Codex slugs", style = MaterialTheme.typography.labelMedium, color = ex.muted)
            FlowRow(
                horizontalArrangement = Arrangement.spacedBy(6.dp),
                verticalArrangement = Arrangement.spacedBy(4.dp),
                itemVerticalAlignment = Alignment.CenterVertically,
            ) {
                customModels.forEach { cm ->
                    InputChip(
                        selected = false,
                        onClick = {},
                        label = {
                            Text(
                                cm.slug,
                                fontFamily = FontFamily.Monospace,
                                style = MaterialTheme.typography.labelSmall,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                        },
                        trailingIcon = {
                            Icon(
                                Icons.Outlined.Close,
                                contentDescription = "Remove ${cm.slug}",
                                modifier = Modifier
                                    .size(InputChipDefaults.AvatarSize)
                                    .clickable { onRemoveCustomModel(cm.slug) },
                            )
                        },
                    )
                }
                AssistChip(
                    onClick = onAddCustomModel,
                    leadingIcon = { Icon(Icons.Outlined.Add, null, Modifier.size(AssistChipDefaults.IconSize)) },
                    label = { Text("+ Custom model") },
                )
            }

            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                OutlinedButton(onClick = onConfigureCodex) {
                    Text(
                        if (codexConfig?.env?.get("OPENROUTER_API_KEY")?.isNotBlank() == true || conn.openRouterConfigured) {
                            "Configure key & default…"
                        } else {
                            "Set OpenRouter key…"
                        },
                    )
                }
            }
        }
    }
}

fun buildScheduleTogglePayload(t: ScheduledTask, enabled: Boolean): JsonObject = params {
    put("id", t.id)
    put("name", t.name)
    put("enabled", enabled)
    put("kind", t.kind)
    t.cron?.takeIf { it.isNotBlank() }?.let { put("cron", it) }
    if (t.intervalSec > 0) put("intervalSec", t.intervalSec)
    if (t.runAt > 0) put("runAt", t.runAt)
    put("cwd", t.cwd)
    t.projectId?.takeIf { it.isNotBlank() }?.let { put("projectId", it) }
    put("agent", t.agent)
    t.model?.takeIf { it.isNotBlank() }?.let { put("model", it) }
    t.mode?.takeIf { it.isNotBlank() }?.let { put("mode", it) }
    put("prompt", t.prompt)
    put("useWorktree", t.useWorktree)
}
