package io.github.sidd065.agentcompanion.core

import android.content.Context
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.test.core.app.ApplicationProvider
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.Noise
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.OkHttpClient
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
class CodexFlagsTest {

    @Test
    fun defaultCodexFlagsEmitEmptyDaemonJson() {
        val f = CodexFlags()
        assertFalse(f.effectiveSkipPermissions)
        assertTrue(f.daemonJson().isEmpty())
        assertEquals(emptyList<String>(), f.tuiArgs())
        assertEquals(emptyList<String>(), f.tuiResumeArgs())
        assertEquals("codex app-server", f.headlessPreview())
        assertEquals("codex", f.tuiPreview())
    }

    @Test
    fun supervisedAndPlanModesExplicitlyEmitSkipPermissionsFalse() {
        val supervised = CodexFlags(mode = "default")
        assertFalse(supervised.effectiveSkipPermissions)
        assertEquals(false, supervised.daemonJson()["skipPermissions"]!!.jsonPrimitive.boolean)
        assertEquals("default", supervised.daemonJson()["mode"]!!.jsonPrimitive.content)

        val plan = CodexFlags(mode = "plan")
        assertFalse(plan.effectiveSkipPermissions)
        assertEquals(false, plan.daemonJson()["skipPermissions"]!!.jsonPrimitive.boolean)
        assertEquals("plan", plan.daemonJson()["mode"]!!.jsonPrimitive.content)

        val explicitPolicy = CodexFlags(approvalPolicy = "on-request", sandbox = "workspace-write")
        assertFalse(explicitPolicy.effectiveSkipPermissions)
        assertEquals(false, explicitPolicy.daemonJson()["skipPermissions"]!!.jsonPrimitive.boolean)
        assertEquals("on-request", explicitPolicy.daemonJson()["approvalPolicy"]!!.jsonPrimitive.content)
        assertEquals("workspace-write", explicitPolicy.daemonJson()["sandbox"]!!.jsonPrimitive.content)
    }

    @Test
    fun fullAccessAndSkipPermissionsEmitTrueAndDaemonFields() {
        val f = CodexFlags(
            skipPermissions = true,
            provider = "openrouter",
            model = "deepseek/deepseek-r1:free",
            effort = "xhigh",
            mode = "acceptEdits",
            approvalPolicy = "on-request",
            sandbox = "workspace-write",
            addDirs = listOf("/srv/shared", ""),
            extraArgs = "--profile ci --config \"foo=bar baz\"",
            tuiExtra = "--search",
        )
        assertTrue(f.effectiveSkipPermissions)
        val j = f.daemonJson()
        assertEquals(true, j["skipPermissions"]!!.jsonPrimitive.boolean)
        assertEquals("openrouter", j["provider"]!!.jsonPrimitive.content)
        assertEquals("deepseek/deepseek-r1:free", j["model"]!!.jsonPrimitive.content)
        assertEquals("xhigh", j["effort"]!!.jsonPrimitive.content)
        assertEquals("acceptEdits", j["mode"]!!.jsonPrimitive.content)
        assertEquals("on-request", j["approvalPolicy"]!!.jsonPrimitive.content)
        assertEquals("workspace-write", j["sandbox"]!!.jsonPrimitive.content)
        assertEquals(listOf("/srv/shared"), (j["addDirs"] as JsonArray).map { it.jsonPrimitive.content })
        assertEquals(
            listOf("--profile", "ci", "--config", "foo=bar baz"),
            (j["extraArgs"] as JsonArray).map { it.jsonPrimitive.content },
        )

        // Mode full-access or never+danger-full-access also activate effectiveSkipPermissions.
        val byMode = CodexFlags(mode = "full-access", model = "openai/gpt-5-chat-latest")
        assertTrue(byMode.effectiveSkipPermissions)
        assertEquals(true, byMode.daemonJson()["skipPermissions"]!!.jsonPrimitive.boolean)
        assertEquals("openai/gpt-5-chat-latest", byMode.daemonJson()["model"]!!.jsonPrimitive.content)

        val byPolicy = CodexFlags(approvalPolicy = "never", sandbox = "danger-full-access")
        assertTrue(byPolicy.effectiveSkipPermissions)
        assertEquals(true, byPolicy.daemonJson()["skipPermissions"]!!.jsonPrimitive.boolean)
    }

    @Test
    fun tuiAndResumeArgsMatchRealCodex0172Cli() {
        val f = CodexFlags(
            model = "openrouter/google/gemini-3-flash-preview",
            effort = "xhigh",
            mode = "acceptEdits",
            addDirs = listOf("/srv/data"),
            extraArgs = "--profile dev",
            tuiExtra = "--no-alt-screen",
        )
        assertEquals(
            listOf(
                "-m", "google/gemini-3-flash-preview",
                "-c", "model_reasoning_effort=\"xhigh\"",
                "-s", "workspace-write",
                "-a", "on-request",
                "--add-dir", "/srv/data",
                "--profile", "dev",
                "--no-alt-screen",
            ),
            f.tuiArgs(),
        )

        // Resume strips model/effort/mode/approvalPolicy/sandbox while keeping --add-dir, extraArgs & tuiExtra.
        assertEquals(
            listOf("--add-dir", "/srv/data", "--profile", "dev", "--no-alt-screen"),
            f.tuiResumeArgs(),
        )

        // Untrusted policy maps to -a on-request for CLI/TUI (since codex-cli 0.172.0 removed approval_policy="untrusted"),
        // while daemonJson() preserves "untrusted" for codex app-server thread/start.
        val untrusted = CodexFlags(approvalPolicy = "untrusted", sandbox = "read-only")
        assertEquals(
            listOf("-s", "read-only", "-a", "on-request"),
            untrusted.tuiArgs(),
        )
        assertEquals("untrusted", untrusted.daemonJson()["approvalPolicy"]!!.jsonPrimitive.content)

        // Explicit provider = "openai" emits -c model_provider="openai" and does NOT strip leading "openrouter/".
        val openaiNative = CodexFlags(
            provider = "openai",
            model = "gpt-4.1-mini",
            mode = "auto",
            addDirs = listOf("/srv/native"),
        )
        assertEquals(
            listOf(
                "-c", "model_provider=\"openai\"",
                "-m", "gpt-4.1-mini",
                "-s", "workspace-write",
                "-a", "never",
                "--add-dir", "/srv/native",
            ),
            openaiNative.tuiArgs(),
        )
        assertEquals(
            listOf("--add-dir", "/srv/native"),
            openaiNative.tuiResumeArgs(),
        )
        assertEquals(
            listOf("-c", "model_provider=\"openai\"", "-m", "gpt-4.1-mini"),
            CodexFlags(provider = "openai", model = "openrouter/openai/gpt-4.1-mini").tuiArgs(),
        )
        assertEquals(
            listOf("-c", "model_provider=\"openai\"", "-m", "gpt-4.1-mini"),
            CodexFlags(provider = "openai", model = "openai/gpt-4.1-mini").tuiArgs(),
        )
        assertEquals(
            listOf("-c", "model_provider=\"openai\""),
            CodexFlags(provider = "openai", model = "openrouter/").tuiArgs(),
        )

        // OpenRouter first-party router slugs stay intact, mixed-case prefixes normalize, and bare "openrouter/" omits -m.
        assertEquals("openrouter/free", normalizeCodexTuiModelSlug("openrouter/free"))
        assertEquals("openrouter/auto:nitro", normalizeCodexTuiModelSlug("OpenRouter/auto:nitro"))
        assertEquals("google/gemini-2.5-flash", normalizeCodexTuiModelSlug("OpenRouter/google/gemini-2.5-flash"))
        assertEquals("", normalizeCodexTuiModelSlug("openrouter/"))
        assertEquals("", normalizeCodexTuiModelSlug("OpenRouter/   "))
        assertEquals("gpt-4.1-mini", normalizeCodexTuiModelSlug("OpenAI/gpt-4.1-mini", provider = "openai"))
        assertEquals("gpt-4.1-mini", normalizeCodexTuiModelSlug("OpenRouter/OpenAI/gpt-4.1-mini", provider = "openai"))
        assertEquals(listOf("-m", "openrouter/free"), CodexFlags(model = "openrouter/free").tuiArgs())
        assertEquals(listOf("-m", "openrouter/auto:nitro"), CodexFlags(model = "OpenRouter/auto:nitro").tuiArgs())
        assertEquals(emptyList<String>(), CodexFlags(model = "openrouter/").tuiArgs())
        assertEquals(emptyList<String>(), CodexFlags(model = "OpenRouter/   ").tuiArgs())

        // Explicit provider = "openrouter" emits -c model_provider="openrouter" and strips leading "openrouter/".
        val explicitOpenRouter = CodexFlags(
            provider = "openrouter",
            model = "openrouter/google/gemini-3-flash-preview",
            approvalPolicy = "untrusted",
            sandbox = "read-only",
        )
        assertEquals(
            listOf(
                "-c", "model_provider=\"openrouter\"",
                "-m", "google/gemini-3-flash-preview",
                "-s", "read-only",
                "-a", "on-request",
            ),
            explicitOpenRouter.tuiArgs(),
        )
        assertEquals(
            emptyList<String>(),
            explicitOpenRouter.tuiResumeArgs(),
        )

        // Bypass preview omits -s/-a and adds --dangerously-bypass-approvals-and-sandbox.
        val bypass = CodexFlags(skipPermissions = true, model = "deepseek/deepseek-r1:free")
        assertEquals(listOf("-m", "deepseek/deepseek-r1:free"), bypass.tuiArgs())
        assertEquals(
            "codex --dangerously-bypass-approvals-and-sandbox -m deepseek/deepseek-r1:free",
            bypass.tuiPreview(),
        )
    }

    @Test
    fun realCodex0172CliValidatesAllGeneratedTuiAndResumeFlagsWithoutConfigErrors() {
        val userHome = System.getProperty("user.home")!!
        val codexBin = File(userHome, ".local/bin/codex")
        assertTrue("Expected executable codex CLI at ${codexBin.absolutePath}", codexBin.canExecute())

        val scratch = File(userHome, ".cache/agc-cxflag-" + UUID.randomUUID().toString().take(8))
        assertFalse("scratch must not be under /tmp", scratch.absolutePath.startsWith("/tmp"))
        scratch.mkdirs()
        try {
            val openRouterPrepend = listOf(
                "-c",
                "model_providers.openrouter={name=\"OpenRouter\",base_url=\"http://127.0.0.1:1/v1\",wire_api=\"responses\"}",
                "-c",
                "model_provider=\"openrouter\"",
            )

            fun runCodexCli(args: List<String>): String {
                val proc = ProcessBuilder(listOf(codexBin.absolutePath) + args)
                    .directory(scratch)
                    .redirectErrorStream(true)
                    .apply {
                        environment()["HOME"] = scratch.absolutePath
                        environment()["CODEX_HOME"] = File(scratch, "cxhome").apply { mkdirs() }.absolutePath
                    }
                    .start()
                proc.outputStream.close()
                val out = proc.inputStream.bufferedReader().readText()
                assertTrue("codex CLI hung on args=$args", proc.waitFor(10, java.util.concurrent.TimeUnit.SECONDS))
                return out
            }

            val flagCases = listOf(
                "untrusted-policy" to CodexFlags(approvalPolicy = "untrusted", sandbox = "read-only"),
                "openai-override" to CodexFlags(provider = "openai", model = "gpt-4.1-mini", effort = "high", mode = "auto"),
                "openrouter-explicit" to CodexFlags(
                    provider = "openrouter",
                    model = "openrouter/google/gemini-3-flash-preview",
                    effort = "xhigh",
                    mode = "plan",
                    approvalPolicy = "untrusted",
                ),
            )

            for ((label, flags) in flagCases) {
                val out = runCodexCli(openRouterPrepend + flags.tuiArgs() + listOf("exec", "noop"))
                assertFalse("[$label] unexpected removed-policy error: $out", out.contains("is no longer supported"))
                assertFalse("[$label] unexpected config load error: $out", out.contains("Error loading configuration"))
                assertFalse("[$label] unexpected missing provider error: $out", out.contains("Model provider"))
                assertFalse("[$label] unexpected CLI parse error: $out", out.contains("unexpected argument"))
                assertTrue(
                    "[$label] expected codex to pass TOML/CLI config parsing and stop at non-git repo check, got: $out",
                    out.contains("Not inside a trusted directory"),
                )
            }

            val resumeFlags = CodexFlags(
                provider = "openai",
                approvalPolicy = "untrusted",
                addDirs = listOf(scratch.absolutePath),
            )
            val resumeOut = runCodexCli(
                openRouterPrepend + listOf("resume", "00000000-0000-0000-0000-000000000000") + resumeFlags.tuiResumeArgs(),
            )
            assertFalse("resume unexpected config error: $resumeOut", resumeOut.contains("is no longer supported"))
            assertFalse("resume unexpected config load error: $resumeOut", resumeOut.contains("Error loading configuration"))
            assertTrue(
                "expected codex resume to pass config validation and stop at non-TTY stdin check, got: $resumeOut",
                resumeOut.contains("stdin is not a terminal"),
            )
        } finally {
            scratch.deleteRecursively()
        }
    }

    @Test
    fun appSettingsIsolatesFlagsAndAddDirsPerAgent() {
        val s = AppSettings(
            agyFlags = AgyFlags(
                skipPermissions = true,
                sandbox = true,
                disableTelemetry = true,
                includeDefaultCustomizations = false,
                addDirs = listOf("/agy/dir"),
                tuiExtra = "--agy-tui",
            ),
            claudeFlags = ClaudeFlags(
                skipPermissions = false,
                model = "sonnet",
                addDirs = listOf("/claude/dir"),
                tuiExtra = "--claude-tui",
            ),
            codexFlags = CodexFlags(
                skipPermissions = false,
                mode = "plan",
                model = "google/gemini-3-flash-preview",
                addDirs = listOf("/codex/dir"),
                tuiExtra = "--codex-tui",
            ),
        )

        val codexJson = s.flagsJson("codex")
        assertEquals("google/gemini-3-flash-preview", codexJson["model"]!!.jsonPrimitive.content)
        assertEquals("plan", codexJson["mode"]!!.jsonPrimitive.content)
        assertEquals(false, codexJson["skipPermissions"]!!.jsonPrimitive.boolean)
        assertNull(codexJson["disableTelemetry"])
        assertNull(codexJson["sandbox"])
        assertNull(codexJson["extraArgs"]) // agy --include-default-customizations=false never leaks

        assertTrue(s.flagsJson("custom-acp").isEmpty())
        assertEquals(s.agyFlags.daemonJson(), s.flagsJson("agy"))
        assertEquals(s.agyFlags.daemonJson(), s.flagsJson(null))
        assertEquals(s.claudeFlags.daemonJson(), s.flagsJson("claude"))

        assertEquals(listOf("/codex/dir"), s.addDirs("codex"))
        assertEquals(listOf("/claude/dir"), s.addDirs("claude"))
        assertEquals(listOf("/agy/dir"), s.addDirs("agy"))
        assertEquals(listOf("/agy/dir"), s.addDirs(""))
        assertEquals(emptyList<String>(), s.addDirs("custom-acp"))

        assertEquals(listOf("--add-dir", "/codex/dir", "--codex-tui"), s.tuiResumeArgs("codex"))
        assertEquals(emptyList<String>(), s.tuiResumeArgs("custom-acp"))

        assertFalse(s.skipPermissions("codex"))
        assertFalse(s.skipPermissions("claude"))
        assertTrue(s.skipPermissions("agy"))
        assertFalse(s.skipPermissions("custom-acp"))
    }

    @Test
    fun hostConfigLabelDisambiguatesLanIpLocalhostAndTunnels() {
        fun cfg(url: String, name: String = "") = HostConfig(
            id = "h",
            name = name,
            url = url,
            hostKey = "k",
            devicePub = "p",
            devicePrivWrapped = "w",
        )

        assertEquals("Workstation", cfg("https://abc.trycloudflare.com", name = "Workstation").label)
        assertEquals("box-xyz", cfg("https://box-xyz.trycloudflare.com").label)
        assertEquals("box-xyz", cfg("https://box-xyz.trycloudflare.com/ws").label)
        assertEquals("192.168.1.10:9473", cfg("http://192.168.1.10:9473").label)
        assertEquals("192.168.1.20:9473", cfg("http://192.168.1.20:9473").label)
        assertEquals("127.0.0.1:41001", cfg("http://127.0.0.1:41001").label)
        assertEquals("127.0.0.1:41002", cfg("http://127.0.0.1:41002").label)
        assertEquals("localhost:9473", cfg("http://localhost:9473/rpc").label)
        assertEquals("[::1]:9473", cfg("http://[::1]:9473").label)
    }

    @Test
    fun hostConnectionSupportsKeyPairProviderAndCodexFeatures() {
        val host = HostConfig("h1", "DevBox", "http://127.0.0.1:19473", "aG9zdEtleQ", "cHVi", "d3JhcA")
        val conn = HostConnection(
            config = host,
            http = OkHttpClient(),
            deviceName = "PixelTest",
            onNotifySeq = { _, _ -> },
            lastNotifSeq = { 0L },
            keyPairProvider = { Noise.generateKeyPair() },
        )
        try {
            assertTrue(conn.agentHasFeature("codex", "openrouter"))
            assertTrue(conn.agentHasFeature("codex", "customModels"))
            assertTrue(conn.agentHasFeature("codex", "fork"))
            assertTrue(conn.agentHasFeature("codex", "rewind"))
            assertTrue(conn.agentHasFeature("codex", "terminal"))
            assertTrue(conn.agentHasFeature("codex", "sideQuestion"))
            assertTrue(conn.agentHasFeature("codex", "asides"))
            assertFalse(conn.agentHasFeature("custom-acp", "openrouter"))

            val store = conn.store("s-codex")
            store.summaryFrom(SessionSummary(id = "s-codex", agent = "codex"))
            assertTrue(store.sideQuestions)
        } finally {
            conn.close()
        }
    }

    @Test
    fun prefsPersistsCodexFlagsAndCustomRecentModelsInDataStore() = runBlocking {
        val ctx = ApplicationProvider.getApplicationContext<Context>()
        val pbFile = File(ctx.filesDir, "codex_prefs_${UUID.randomUUID()}.preferences_pb")

        val job1 = SupervisorJob()
        val scope1 = CoroutineScope(job1 + Dispatchers.IO)
        val ds1 = PreferenceDataStoreFactory.create(scope = scope1) { pbFile }
        val prefs1 = Prefs(ctx, scope1, ds1)

        prefs1.update { s ->
            s.copy(
                codexFlags = CodexFlags(
                    skipPermissions = false,
                    model = "openai/gpt-5-chat-latest",
                    effort = "high",
                    mode = "acceptEdits",
                    approvalPolicy = "on-request",
                    sandbox = "workspace-write",
                    provider = "openrouter",
                    addDirs = listOf("/workspace/lib"),
                    extraArgs = "--profile fast",
                    tuiExtra = "--search",
                ),
            )
                .withRecentCodexModel("google/gemini-3-flash-preview", isCustom = false)
                .withRecentCodexModel("deepseek/deepseek-r1:free", customName = "DeepSeek R1 Free", isCustom = true)
        }
        job1.cancelAndJoin()

        // Reopen from disk in a fresh scope & Prefs instance to prove real persistence.
        val job2 = SupervisorJob()
        val scope2 = CoroutineScope(job2 + Dispatchers.IO)
        try {
            val ds2 = PreferenceDataStoreFactory.create(scope = scope2) { pbFile }
            val prefs2 = Prefs(ctx, scope2, ds2)
            val loaded = prefs2.value
            assertEquals("openai/gpt-5-chat-latest", loaded.codexFlags.model)
            assertEquals("high", loaded.codexFlags.effort)
            assertEquals("acceptEdits", loaded.codexFlags.mode)
            assertEquals("on-request", loaded.codexFlags.approvalPolicy)
            assertEquals("workspace-write", loaded.codexFlags.sandbox)
            assertEquals("openrouter", loaded.codexFlags.provider)
            assertEquals(listOf("/workspace/lib"), loaded.codexFlags.addDirs)
            assertEquals("--profile fast", loaded.codexFlags.extraArgs)
            assertEquals("--search", loaded.codexFlags.tuiExtra)
            assertEquals(
                listOf(CustomModel(slug = "deepseek/deepseek-r1:free", name = "DeepSeek R1 Free")),
                loaded.customCodexModels,
            )
            assertEquals(
                listOf("deepseek/deepseek-r1:free", "google/gemini-3-flash-preview"),
                loaded.recentCodexModels,
            )
        } finally {
            job2.cancelAndJoin()
            pbFile.delete()
        }
    }

    @Test
    fun tuiResumeArgsStripsProviderPreventingOpenRouterHijackAndPreservesSessionSkipPerms() {
        val openRouterFlags = CodexFlags(
            provider = "openrouter",
            model = "google/gemini-2.5-flash",
            effort = "high",
            mode = "full-access",
            approvalPolicy = "never",
            sandbox = "danger-full-access",
            addDirs = listOf("/extra"),
        )
        assertEquals(listOf("--add-dir", "/extra"), openRouterFlags.tuiResumeArgs())

        val openAiFlags = CodexFlags(
            provider = "openai",
            model = "gpt-5.3-codex",
            addDirs = listOf("/repo/sub"),
            tuiExtra = "--no-alt-screen",
        )
        assertEquals(listOf("--add-dir", "/repo/sub", "--no-alt-screen"), openAiFlags.tuiResumeArgs())

        val settings = AppSettings(codexFlags = openRouterFlags)
        assertEquals(listOf("--add-dir", "/extra"), settings.tuiResumeArgs("codex"))
        assertTrue(settings.skipPermissions("codex"))

        val supervisedSummary = SessionSummary(
            id = "sess-supervised-1",
            agent = "codex",
            skipPermissions = false,
        )
        val resolvedSkipPerms = supervisedSummary.skipPermissions
        assertFalse(resolvedSkipPerms)
    }
}
