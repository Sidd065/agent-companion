package io.github.sidd065.agentcompanion.ui.components

import android.content.Context
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.test.core.app.ApplicationProvider
import io.github.sidd065.agentcompanion.core.CustomModel
import io.github.sidd065.agentcompanion.core.ModelInfo
import io.github.sidd065.agentcompanion.core.Prefs
import io.github.sidd065.agentcompanion.core.ProviderModelInfo
import io.github.sidd065.agentcompanion.core.ProviderModelsResult
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.File
import java.util.UUID

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ModelPickerSheetTest {

    private val sampleCatalog = ProviderModelsResult(
        provider = "codex",
        defaultModelId = "google/gemini-3-flash-preview",
        openRouterConfigured = true,
        models = listOf(
            ProviderModelInfo(
                modelId = "google/gemini-3-flash-preview",
                name = "Google: Gemini 3 Flash Preview",
                description = "Fast Frontier Flash",
                provider = "openrouter",
                contextWindow = 1048576,
                supportsReasoning = true,
                supportsTools = true,
                supportsImages = true,
            ),
            ProviderModelInfo(
                modelId = "google/gemini-2.5-flash",
                name = "Google: Gemini 2.5 Flash",
                description = "Balanced workhorse",
                provider = "openrouter",
                contextWindow = 1048576,
                supportsReasoning = true,
                supportsTools = true,
            ),
            ProviderModelInfo(
                modelId = "deepseek/deepseek-r1",
                name = "DeepSeek: R1",
                description = "Reasoning model",
                provider = "openrouter",
                contextWindow = 128000,
                supportsReasoning = true,
            ),
            ProviderModelInfo(
                modelId = "deepseek/deepseek-r1:free",
                name = "DeepSeek: R1 (free)",
                description = "Free variant",
                provider = "openrouter",
                contextWindow = 128000,
                supportsReasoning = true,
            ),
        ),
        customModels = listOf(
            CustomModel(slug = "openai/gpt-5-chat-latest", name = "GPT-5 Chat Latest"),
        ),
    )

    @Test
    fun mergePickerModelsDeduplicatesExactIdsAndPreservesVariants() {
        val merged = mergePickerModels(
            catalog = sampleCatalog,
            localCustom = listOf(
                CustomModel(slug = "qwen/qwen3-coder:exacto", name = "Qwen 3 Coder Exacto"),
                CustomModel(slug = "openai/gpt-5-chat-latest", name = "Duplicate Custom"),
                CustomModel(slug = "deepseek/deepseek-r1:free", name = "Custom Free R1"),
            ),
            fallbackModels = listOf(
                ModelInfo(modelId = "google/gemini-2.5-flash", name = "Duplicate Fallback"),
                ModelInfo(modelId = "o4-mini", name = "OpenAI o4-mini"),
            ),
        )

        val ids = merged.map { it.modelId }
        // Exact deduplication: both base and :free remain distinct, duplicates collapsed.
        assertEquals(
            listOf(
                "qwen/qwen3-coder:exacto",
                "openai/gpt-5-chat-latest",
                "deepseek/deepseek-r1:free",
                "google/gemini-3-flash-preview",
                "google/gemini-2.5-flash",
                "deepseek/deepseek-r1",
                "o4-mini",
            ),
            ids,
        )
        // Custom model that also exists in catalog retains catalog contextWindow while marking isCustom = true.
        val r1Free = merged.first { it.modelId == "deepseek/deepseek-r1:free" }
        assertTrue(r1Free.isCustom)
        assertEquals(128000, r1Free.contextWindow)
        assertFalse(merged.first { it.modelId == "deepseek/deepseek-r1" }.isCustom)
    }

    @Test
    fun filterPickerModelsSearchesTokensAndRanksCurrentAndRecentsFirst() {
        val merged = mergePickerModels(sampleCatalog, emptyList())

        // Empty query ranks currentModelId first, then recentSlugs in MRU order.
        val ranked = filterPickerModels(
            models = merged,
            query = "",
            recentSlugs = listOf("deepseek/deepseek-r1", "google/gemini-2.5-flash"),
            currentModelId = "deepseek/deepseek-r1:free",
        )
        assertEquals("deepseek/deepseek-r1:free", ranked[0].modelId)
        assertEquals("deepseek/deepseek-r1", ranked[1].modelId)
        assertEquals("google/gemini-2.5-flash", ranked[2].modelId)

        // Multi-token search across name/id.
        val gemini3 = filterPickerModels(merged, "Gemini 3 Flash")
        assertEquals(listOf("google/gemini-3-flash-preview"), gemini3.map { it.modelId })

        // Variant filter `:free`.
        val freeOnly = filterPickerModels(merged, ":free")
        assertEquals(listOf("deepseek/deepseek-r1:free"), freeOnly.map { it.modelId })
    }

    @Test
    fun customModelSlugOfferPreservesLatestAndVariantSuffixesIntact() {
        val merged = mergePickerModels(sampleCatalog, emptyList())

        assertNull(customModelSlugOffer("", merged))
        assertNull(customModelSlugOffer("   ", merged))
        assertNull(customModelSlugOffer("Gemini 3 Flash", merged))
        assertNull(customModelSlugOffer("google/gemini-3-flash-preview", merged))
        assertNull(customModelSlugOffer("deepseek/deepseek-r1:free", merged))

        // Unlisted slugs preserve `-latest` and `:variant` byte-for-byte.
        assertEquals(
            "anthropic/claude-3.7-sonnet:thinking",
            customModelSlugOffer("  anthropic/claude-3.7-sonnet:thinking  ", merged),
        )
        assertEquals(
            "qwen/qwen3-coder:exacto",
            customModelSlugOffer("qwen/qwen3-coder:exacto", merged),
        )
        assertEquals(
            "openai/chatgpt-4o-latest",
            customModelSlugOffer("openai/chatgpt-4o-latest", merged),
        )
        assertEquals(
            "deepseek/deepseek-r1:nitro",
            customModelSlugOffer("deepseek/deepseek-r1:nitro", merged),
        )
    }

    @Test
    fun prefsCustomAndRecentSlugLifecycleAndRemoval() = runBlocking {
        val ctx = ApplicationProvider.getApplicationContext<Context>()
        val pbFile = File(ctx.filesDir, "picker_prefs_${UUID.randomUUID()}.preferences_pb")
        val job = SupervisorJob()
        val scope = CoroutineScope(job + Dispatchers.IO)
        try {
            val ds = PreferenceDataStoreFactory.create(scope = scope) { pbFile }
            val prefs = Prefs(ctx, scope, ds)

            prefs.update { s ->
                s.withRecentCodexModel("openai/gpt-5-chat-latest", isCustom = true)
                    .withRecentCodexModel("qwen/qwen3-coder:exacto", isCustom = true)
                    .withRecentCodexModel("google/gemini-3-flash-preview", isCustom = false)
            }
            assertEquals(
                listOf("google/gemini-3-flash-preview", "qwen/qwen3-coder:exacto", "openai/gpt-5-chat-latest"),
                prefs.value.recentCodexModels,
            )
            assertEquals(
                listOf("qwen/qwen3-coder:exacto", "openai/gpt-5-chat-latest"),
                prefs.value.customCodexModels.map { it.slug },
            )

            // Removing a custom model removes it from customCodexModels and recentCodexModels while keeping others intact.
            prefs.update { it.withoutCustomCodexModel("qwen/qwen3-coder:exacto") }
            assertEquals(
                listOf("openai/gpt-5-chat-latest"),
                prefs.value.customCodexModels.map { it.slug },
            )
            assertEquals(
                listOf("google/gemini-3-flash-preview", "openai/gpt-5-chat-latest"),
                prefs.value.recentCodexModels,
            )
        } finally {
            job.cancelAndJoin()
            pbFile.delete()
        }
    }
}
