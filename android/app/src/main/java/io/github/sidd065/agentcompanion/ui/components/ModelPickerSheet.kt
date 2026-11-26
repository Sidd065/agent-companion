package io.github.sidd065.agentcompanion.ui.components

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.CustomModel
import io.github.sidd065.agentcompanion.core.ModelInfo
import io.github.sidd065.agentcompanion.core.ProviderModelInfo
import io.github.sidd065.agentcompanion.core.ProviderModelsResult
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.jsonPrimitive

/**
 * Merges daemon catalog models, daemon customModels, local DataStore customModels,
 * and session fallback models, deduplicating by exact modelId while preserving
 * `-latest` and `:variant` (e.g. `:free`, `:nitro`, `:exacto`) suffixes intact.
 */
fun mergePickerModels(
    catalog: ProviderModelsResult?,
    localCustom: List<CustomModel>,
    fallbackModels: List<ModelInfo> = emptyList(),
): List<ProviderModelInfo> {
    val catalogById = catalog?.models.orEmpty()
        .filter { it.modelId.isNotBlank() }
        .associateBy { it.modelId.trim() }
    val allCustoms = (localCustom + catalog?.customModels.orEmpty())
        .map { CustomModel(slug = it.slug.trim(), name = it.name.trim()) }
        .filter { it.slug.isNotEmpty() }
        .distinctBy { it.slug }
    val customSlugSet = allCustoms.mapTo(HashSet()) { it.slug }

    val seen = HashSet<String>()
    val result = ArrayList<ProviderModelInfo>()

    for (cm in allCustoms) {
        if (!seen.add(cm.slug)) continue
        val existing = catalogById[cm.slug]
        if (existing != null) {
            result.add(
                existing.copy(
                    modelId = cm.slug,
                    name = existing.name.ifBlank { cm.name.ifBlank { cm.slug } },
                    isCustom = true,
                ),
            )
        } else {
            result.add(
                ProviderModelInfo(
                    modelId = cm.slug,
                    name = cm.name.ifBlank { cm.slug },
                    provider = if ('/' in cm.slug) "openrouter" else "",
                    isCustom = true,
                ),
            )
        }
    }

    for (m in catalog?.models.orEmpty()) {
        val id = m.modelId.trim()
        if (id.isEmpty() || !seen.add(id)) continue
        result.add(
            m.copy(
                modelId = id,
                name = m.name.ifBlank { id },
                isCustom = m.isCustom || id in customSlugSet,
            ),
        )
    }

    for (fb in fallbackModels) {
        val id = fb.modelId.trim()
        if (id.isEmpty() || !seen.add(id)) continue
        result.add(
            ProviderModelInfo(
                modelId = id,
                name = fb.name.trim().ifBlank { id },
                description = fb.description.orEmpty(),
                provider = if ('/' in id) "openrouter" else "",
                isCustom = id in customSlugSet,
            ),
        )
    }

    return result
}

/**
 * Filters merged models by case-insensitive substring tokens across `modelId`, `name`,
 * `provider`, and `description`, placing exact matches, `currentModelId`, `recentSlugs`,
 * and prefix matches first.
 */
fun filterPickerModels(
    models: List<ProviderModelInfo>,
    query: String,
    recentSlugs: List<String> = emptyList(),
    currentModelId: String? = null,
): List<ProviderModelInfo> {
    val trimmed = query.trim()
    val qLower = trimmed.lowercase()
    val tokens = if (qLower.isEmpty()) emptyList() else qLower.split(Regex("\\s+")).filter { it.isNotEmpty() }
    val recentOrder = recentSlugs
        .map { it.trim() }
        .filter { it.isNotEmpty() }
        .distinct()
        .withIndex()
        .associate { (idx, slug) -> slug to idx }
    val curId = currentModelId?.trim()?.takeIf { it.isNotEmpty() }

    data class RankedModel(
        val model: ProviderModelInfo,
        val tier: Int,
        val subRank: Int,
        val origIndex: Int,
    )

    val matched = ArrayList<RankedModel>(models.size)
    for ((idx, m) in models.withIndex()) {
        if (tokens.isNotEmpty()) {
            val idLower = m.modelId.lowercase()
            val nameLower = m.name.lowercase()
            val provLower = m.provider.lowercase()
            val descLower = m.description.lowercase()
            val allMatch = tokens.all { tok ->
                idLower.contains(tok) || nameLower.contains(tok) || provLower.contains(tok) || descLower.contains(tok)
            }
            if (!allMatch) continue
        }

        val idLower = m.modelId.lowercase()
        val nameLower = m.name.lowercase()
        val slugTailLower = idLower.substringAfter('/')
        val recentIdx = recentOrder[m.modelId]

        val (tier, subRank) = when {
            qLower.isNotEmpty() && idLower == qLower -> 0 to 0
            curId != null && m.modelId == curId -> 1 to 0
            recentIdx != null -> 2 to recentIdx
            qLower.isNotEmpty() && (idLower.startsWith(qLower) || slugTailLower.startsWith(qLower) || nameLower.startsWith(qLower)) -> 3 to 0
            m.isCustom -> 4 to 0
            else -> 5 to 0
        }
        matched.add(RankedModel(m, tier, subRank, idx))
    }

    return matched
        .sortedWith(compareBy<RankedModel>({ it.tier }, { it.subRank }, { it.origIndex }))
        .map { it.model }
}

/**
 * Returns the exact trimmed custom model slug to offer as a one-tap
 * `Use custom model "<slug>"` action when `query` is non-blank and does not
 * byte-for-byte match an existing `modelId` (preserving `-latest`, `:free`, `:exacto`, etc.).
 * Rejects whitespace-containing search phrases.
 */
fun customModelSlugOffer(query: String, existingModels: List<ProviderModelInfo>): String? {
    val trimmed = query.trim()
    if (trimmed.isEmpty() || trimmed.any { it.isWhitespace() }) return null
    if (existingModels.any { it.modelId == trimmed }) return null
    return trimmed
}

/**
 * Searchable OpenRouter / Codex model picker bottom sheet with 1-tap arbitrary
 * custom slug entry, readiness badge, live catalog refresh, and custom model removal.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ModelPickerSheet(
    conn: HostConnection?,
    currentModelId: String?,
    onSelect: (String) -> Unit,
    onDismiss: () -> Unit,
    provider: String = "codex",
    allowDefault: Boolean = false,
    defaultLabel: String? = null,
    fallbackModels: List<ModelInfo> = emptyList(),
    title: String = "Select Model",
) {
    val g = LocalContext.current.graph
    val uiScope = rememberCoroutineScope()
    val saved by g.prefs.flow.collectAsState()
    val ex = LocalExtra.current
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)

    val providerModelsMap = conn?.providerModels?.collectAsState()?.value.orEmpty()
    val hello = conn?.hello?.collectAsState()?.value
    val catalog = providerModelsMap[provider]

    var query by remember { mutableStateOf("") }
    var refreshing by remember { mutableStateOf(false) }
    var refreshError by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(conn, provider) {
        if (conn != null) {
            runCatching { conn.fetchProviderModels(provider, refresh = false) }
        }
    }

    val openRouterReady = hello?.get("openRouterConfigured")?.jsonPrimitive?.booleanOrNull == true ||
        catalog?.openRouterConfigured == true
    val nativeReady = catalog?.nativeAuthConfigured == true

    val merged = remember(catalog, saved.customCodexModels, fallbackModels) {
        mergePickerModels(catalog, saved.customCodexModels, fallbackModels)
    }
    val filtered = remember(merged, query, saved.recentCodexModels, currentModelId) {
        filterPickerModels(merged, query, saved.recentCodexModels, currentModelId)
    }
    val customOffer = remember(query, merged) {
        customModelSlugOffer(query, merged)
    }
    val removableCustomSlugs = remember(saved.customCodexModels, catalog?.customModels) {
        (saved.customCodexModels.map { it.slug.trim() } + catalog?.customModels.orEmpty().map { it.slug.trim() })
            .filter { it.isNotEmpty() }
            .toSet()
    }

    fun selectSlug(slug: String, isCustom: Boolean) {
        val clean = slug.trim()
        if (clean.isEmpty()) return
        g.scope.launch {
            g.prefs.update { it.withRecentCodexModel(clean, isCustom = isCustom) }
            if (isCustom && conn != null) {
                val updated = (
                    listOf(CustomModel(slug = clean, name = clean)) +
                        saved.customCodexModels +
                        catalog?.customModels.orEmpty()
                    ).distinctBy { it.slug.trim() }.filter { it.slug.isNotBlank() }
                runCatching { conn.saveProviderCustomModels(provider, updated) }
            }
        }
        onSelect(clean)
        onDismiss()
    }

    fun removeCustomSlug(slug: String) {
        val clean = slug.trim()
        if (clean.isEmpty()) return
        g.scope.launch {
            g.prefs.update { it.withoutCustomCodexModel(clean) }
            if (conn != null) {
                val updated = (saved.customCodexModels + catalog?.customModels.orEmpty())
                    .filterNot { it.slug.trim() == clean }
                    .distinctBy { it.slug.trim() }
                    .filter { it.slug.isNotBlank() }
                runCatching { conn.saveProviderCustomModels(provider, updated) }
            }
        }
    }

    val hostDefaultTitle = defaultLabel?.takeIf { it.isNotBlank() }
        ?: catalog?.defaultModelId?.takeIf { it.isNotBlank() }?.let { "Host default ($it)" }
        ?: "Host default"

    AppSheet(onDismissRequest = onDismiss, sheetState = sheetState) {
        Column(
            Modifier
                .fillMaxWidth()
                .imePadding()
                .padding(bottom = 24.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Row(
                Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 20.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(title, style = MaterialTheme.typography.titleLarge, modifier = Modifier.weight(1f))
                when {
                    openRouterReady -> Pill("OpenRouter ready", ex.success)
                    nativeReady -> Pill("OpenAI native", MaterialTheme.colorScheme.primary)
                    conn == null -> Pill("Offline", ex.muted)
                    else -> Pill("Unconfigured", ex.warning)
                }
                if (conn != null) {
                    Spacer(Modifier.width(4.dp))
                    IconButton(
                        onClick = {
                            if (!refreshing) {
                                refreshing = true
                                refreshError = null
                                uiScope.launch {
                                    runCatching { conn.fetchProviderModels(provider, refresh = true) }
                                        .onFailure { refreshError = it.message }
                                    refreshing = false
                                }
                            }
                        },
                        enabled = !refreshing,
                    ) {
                        if (refreshing) {
                            CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                        } else {
                            Icon(Icons.Outlined.Refresh, contentDescription = "Refresh models")
                        }
                    }
                }
            }

            OutlinedTextField(
                value = query,
                onValueChange = { query = it },
                placeholder = { Text("Search or enter OpenRouter model ID (e.g. google/gemini-3-flash-preview)") },
                leadingIcon = { Icon(Icons.Outlined.Search, contentDescription = null) },
                trailingIcon = if (query.isNotEmpty()) {
                    {
                        IconButton(onClick = { query = "" }) {
                            Icon(Icons.Outlined.Close, contentDescription = "Clear search")
                        }
                    }
                } else null,
                singleLine = true,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
                keyboardActions = KeyboardActions(
                    onDone = {
                        val pick = customOffer ?: filtered.firstOrNull()?.modelId
                        if (pick != null) selectSlug(pick, isCustom = customOffer != null)
                    },
                ),
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 20.dp),
            )

            refreshError?.takeIf { it.isNotBlank() }?.let { err ->
                Text(
                    err,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.padding(horizontal = 20.dp),
                )
            }

            if (customOffer != null) {
                Surface(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 20.dp)
                        .clickable { selectSlug(customOffer, isCustom = true) },
                    shape = RoundedCornerShape(12.dp),
                    color = MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.55f),
                ) {
                    Row(
                        Modifier.padding(horizontal = 14.dp, vertical = 12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Icon(
                            Icons.Outlined.Add,
                            contentDescription = null,
                            tint = MaterialTheme.colorScheme.primary,
                            modifier = Modifier.size(20.dp),
                        )
                        Spacer(Modifier.width(10.dp))
                        Column(Modifier.weight(1f)) {
                            Text(
                                "Use custom model \"$customOffer\"",
                                style = MaterialTheme.typography.bodyMedium,
                                fontFamily = FontFamily.Monospace,
                                fontWeight = FontWeight.SemiBold,
                            )
                            Text(
                                "Save to custom OpenRouter models & select",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
            }

            HorizontalDivider(Modifier.padding(top = 4.dp))

            LazyColumn(
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(max = 420.dp),
            ) {
                if (allowDefault && query.isBlank()) {
                    item(key = "__host_default__") {
                        Row(
                            Modifier
                                .fillMaxWidth()
                                .clickable {
                                    onSelect("")
                                    onDismiss()
                                }
                                .padding(horizontal = 20.dp, vertical = 12.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Column(Modifier.weight(1f)) {
                                Text(hostDefaultTitle, style = MaterialTheme.typography.bodyLarge)
                                Text(
                                    "Use the daemon or provider default model",
                                    style = MaterialTheme.typography.labelSmall,
                                    color = ex.muted,
                                )
                            }
                            if (currentModelId.isNullOrBlank()) {
                                Icon(
                                    Icons.Outlined.Check,
                                    contentDescription = "Selected",
                                    tint = MaterialTheme.colorScheme.primary,
                                    modifier = Modifier.size(18.dp),
                                )
                            }
                        }
                        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.5f))
                    }
                }

                if (filtered.isEmpty() && customOffer == null) {
                    item(key = "__empty__") {
                        Text(
                            if (conn == null) "Connect to a host or type any OpenRouter model slug above."
                            else "No matching catalog models. Type a full slug (e.g. vendor/model) above to use it directly.",
                            style = MaterialTheme.typography.bodySmall,
                            color = ex.muted,
                            modifier = Modifier.padding(horizontal = 20.dp, vertical = 16.dp),
                        )
                    }
                }

                items(filtered, key = { it.modelId }) { m ->
                    val selected = !currentModelId.isNullOrBlank() && m.modelId == currentModelId
                    val isRemovableCustom = m.isCustom && m.modelId in removableCustomSlugs
                    val isRecent = m.modelId in saved.recentCodexModels
                    Row(
                        Modifier
                            .fillMaxWidth()
                            .clickable { selectSlug(m.modelId, isCustom = false) }
                            .padding(horizontal = 20.dp, vertical = 10.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                            Text(
                                m.name.ifBlank { m.modelId },
                                style = MaterialTheme.typography.bodyMedium,
                                fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Normal,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                            Text(
                                m.modelId,
                                style = MaterialTheme.typography.labelSmall,
                                fontFamily = FontFamily.Monospace,
                                color = ex.muted,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                            FlowRow(
                                horizontalArrangement = Arrangement.spacedBy(6.dp),
                                verticalArrangement = Arrangement.spacedBy(4.dp),
                            ) {
                                if (m.isCustom) Pill("Custom", MaterialTheme.colorScheme.tertiary)
                                else if (isRecent) Pill("Recent", MaterialTheme.colorScheme.secondary)
                                if (m.contextWindow >= 1000) Pill("${m.contextWindow / 1000}k ctx", ex.muted)
                                if (m.supportsReasoning) Pill("Reasoning", MaterialTheme.colorScheme.primary)
                                if (m.supportsImages) Pill("Images", ex.success)
                            }
                        }
                        if (selected) {
                            Spacer(Modifier.width(8.dp))
                            Icon(
                                Icons.Outlined.Check,
                                contentDescription = "Selected",
                                tint = MaterialTheme.colorScheme.primary,
                                modifier = Modifier.size(18.dp),
                            )
                        }
                        if (isRemovableCustom) {
                            Spacer(Modifier.width(4.dp))
                            IconButton(
                                onClick = { removeCustomSlug(m.modelId) },
                                modifier = Modifier.size(32.dp),
                            ) {
                                Icon(
                                    Icons.Outlined.Close,
                                    contentDescription = "Remove custom model ${m.modelId}",
                                    tint = ex.muted,
                                    modifier = Modifier.size(16.dp),
                                )
                            }
                        }
                    }
                    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.4f))
                }
            }
            Spacer(Modifier.height(4.dp))
        }
    }
}
