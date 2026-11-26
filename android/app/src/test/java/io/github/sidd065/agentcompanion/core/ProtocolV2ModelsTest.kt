package io.github.sidd065.agentcompanion.core

import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.Noise
import io.github.sidd065.agentcompanion.ui.chat.TranscriptActions
import io.github.sidd065.agentcompanion.ui.chat.commandOutput
import io.github.sidd065.agentcompanion.ui.chat.permOptionLabel
import io.github.sidd065.agentcompanion.ui.chat.toolTitle
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import okhttp3.OkHttpClient
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ProtocolV2ModelsTest {

    @Test
    fun decodesSessionSummaryWithV2FieldsAndCheckpoint() {
        val raw = """
            {
              "id": "s-123",
              "agent": "codex",
              "title": "Refactor auth",
              "cwd": "/home/user/repo",
              "state": "idle",
              "projectId": "proj-1",
              "worktreePath": "/home/user/.local/share/agentc/worktrees/feat-auth",
              "branch": "feat-auth",
              "settled": true,
              "draft": "follow up later",
              "lastCheckpoint": {
                "turnIndex": 2,
                "files": [{"path": "auth.go", "status": "M", "additions": 14, "deletions": 3}],
                "totalAdd": 14,
                "totalDel": 3
              }
            }
        """.trimIndent()

        val s = AppJson.decodeFromString(SessionSummary.serializer(), raw)
        assertEquals("s-123", s.id)
        assertEquals("Codex", s.agentLabel)
        assertEquals("proj-1", s.projectId)
        assertEquals("feat-auth", s.branch)
        assertTrue(s.settled)
        assertEquals("follow up later", s.draft)
        assertNotNull(s.lastCheckpoint)
        assertEquals(2, s.lastCheckpoint!!.turnIndex)
        assertEquals(14, s.lastCheckpoint!!.totalAdd)
        assertEquals(1, s.lastCheckpoint!!.files.size)
    }

    @Test
    fun decodesGenericAcpAgentLabelCleanly() {
        assertEquals("Claude", agentLabel("claude"))
        assertEquals("Codex", agentLabel("codex"))
        assertEquals("agy", agentLabel("agy"))
        assertEquals("Fakeacp", agentLabel("fakeacp"))
        assertEquals("Codex / OpenRouter", agentProduct("codex"))
        assertTrue(SessionSummary(id = "c-1", agent = "codex").isCodex)
        val skill = AppJson.decodeFromString(
            SkillInfo.serializer(),
            """{"name":"lint","description":"Run linter","path":"/repo/.codex/skills/lint","source":"codex","scope":"project"}""",
        )
        assertEquals("project", skill.scope)
    }

    @Test
    fun decodesGitStatusUsageScheduleAndHostHealth() {
        val gitRaw = """
            {
              "isRepo": true,
              "branch": "main",
              "ahead": 2,
              "behind": 1,
              "isDefaultBranch": true,
              "files": [{"path": "README.md", "unstaged": "M", "additions": 5, "deletions": 1}],
              "totalAdd": 5,
              "totalDel": 1,
              "forge": "github"
            }
        """.trimIndent()
        val git = AppJson.decodeFromString(GitStatus.serializer(), gitRaw)
        assertTrue(git.isRepo)
        assertTrue(git.isDefaultBranch)
        assertEquals(1, git.files.size)
        assertEquals("github", git.forge)

        val usageRaw = """
            {
              "days": 30,
              "turns": 42,
              "totalTokens": 125000,
              "costUsd": 1.425,
              "byAgent": [{"key": "claude", "turns": 30, "totalTokens": 90000, "costUsd": 1.2}]
            }
        """.trimIndent()
        val usage = AppJson.decodeFromString(UsageSummary.serializer(), usageRaw)
        assertEquals(42, usage.turns)
        assertEquals(1, usage.byAgent.size)
        assertEquals("claude", usage.byAgent[0].key)

        val healthRaw = """
            {
              "memTotalKiB": 16000000,
              "memAvailableKiB": 8000000,
              "memUsedPct": 50.0,
              "psiSomeAvg10": 0.12,
              "psiFullAvg10": 0.01,
              "loadAvg1": 0.75,
              "processes": [{"sessionId": "s-1", "title": "Build", "agent": "claude", "pid": 4321, "rssKiB": 204800}]
            }
        """.trimIndent()
        val health = AppJson.decodeFromString(HostHealth.serializer(), healthRaw)
        assertEquals(50.0, health.memUsedPct, 0.01)
        assertEquals(4321, health.processes[0].pid)
    }

    @Test
    fun decodesProviderModelsListConfigAndHealth() {
        val modelsRaw = """
            {
              "provider": "codex",
              "defaultModelId": "google/gemini-3-flash-preview",
              "openRouterConfigured": true,
              "nativeAuthConfigured": false,
              "models": [
                {
                  "modelId": "google/gemini-3-flash-preview",
                  "name": "Google: Gemini 3 Flash Preview",
                  "description": "Fast multimodal coding model",
                  "provider": "openrouter",
                  "contextWindow": 1048576,
                  "supportsReasoning": true,
                  "supportsTools": true,
                  "supportsImages": true,
                  "isCustom": false
                }
              ],
              "customModels": [
                {"slug": "deepseek/deepseek-r1:free", "name": "DeepSeek R1 Free"}
              ],
              "availableModes": [
                {"id": "default", "name": "Build (Supervised)"},
                {"id": "plan", "name": "Plan"}
              ],
              "reasoningEfforts": ["low", "medium", "high", "xhigh"]
            }
        """.trimIndent()
        val pm = AppJson.decodeFromString(ProviderModelsResult.serializer(), modelsRaw)
        assertEquals("codex", pm.provider)
        assertEquals("google/gemini-3-flash-preview", pm.defaultModelId)
        assertTrue(pm.openRouterConfigured)
        assertEquals(1, pm.models.size)
        assertEquals(1048576, pm.models[0].contextWindow)
        assertTrue(pm.models[0].supportsReasoning)
        assertTrue(pm.models[0].supportsImages)
        assertEquals("deepseek/deepseek-r1:free", pm.customModels[0].slug)
        assertEquals(listOf("default", "plan"), pm.availableModes.map { it.id })
        assertEquals(listOf("low", "medium", "high", "xhigh"), pm.reasoningEfforts)

        val listRaw = """
            {
              "agents": [
                {"id": "codex", "name": "Codex", "driver": "codex", "available": true, "builtin": true, "features": ["openrouter", "customModels"]}
              ],
              "configs": [
                {
                  "id": "codex",
                  "name": "Codex",
                  "driver": "codex",
                  "enabled": true,
                  "env": {"OPENROUTER_API_KEY": "***"},
                  "defaultModel": "google/gemini-3-flash-preview",
                  "customModels": [{"slug": "openai/gpt-5-chat-latest", "name": "GPT-5 Chat Latest"}],
                  "builtin": true
                }
              ]
            }
        """.trimIndent()
        val pl = AppJson.decodeFromString(ProvidersListResult.serializer(), listRaw)
        assertEquals(1, pl.agents.size)
        assertEquals(1, pl.configs.size)
        assertEquals("***", pl.configs[0].env!!["OPENROUTER_API_KEY"])
        assertEquals("openai/gpt-5-chat-latest", pl.configs[0].customModels[0].slug)

        val provHealthRaw = """
            {
              "id": "codex",
              "available": true,
              "command": "/home/user/.local/bin/codex",
              "version": "codex-cli 0.172.0",
              "authStatus": "openrouter",
              "message": "OpenRouter configured",
              "latencyMs": 18,
              "checkedAt": 1700000000000
            }
        """.trimIndent()
        val ph = AppJson.decodeFromString(ProviderHealth.serializer(), provHealthRaw)
        assertTrue(ph.available)
        assertEquals("openrouter", ph.authStatus)
        assertEquals("codex-cli 0.172.0", ph.version)
        assertEquals(18L, ph.latencyMs)
    }

    @Test
    fun codexTranscriptHelpersHandleExitCodeMultiFileDiffsAndPermOptions() {
        // 1. Tool.commandOutput() extracts camelCase "exitCode" (Codex Bash) alongside snake_case "exit_code" (agy).
        val codexBash = Tool(
            callId = "call-bash-1",
            name = "Bash",
            kind = "execute",
            status = "failed",
            input = buildJsonObject { put("command", "go test ./...") },
            output = buildJsonObject {
                put("output", "FAIL\texample/pkg\t0.012s\n")
                put("exitCode", 17)
            },
        )
        val (codexExit, codexBody) = codexBash.commandOutput()
        assertEquals(17, codexExit)
        assertEquals("FAIL\texample/pkg\t0.012s", codexBody)

        val codexZero = Tool(
            callId = "call-bash-0",
            name = "Bash",
            kind = "execute",
            status = "completed",
            output = buildJsonObject {
                put("output", "ok")
                put("exitCode", 0)
            },
        )
        assertEquals(0, codexZero.commandOutput().first)

        // 2. toolTitle(t) preserves (+N more) suffix on multi-file Codex Edit tools.
        val multiEdit = Tool(
            callId = "call-edit-3",
            name = "Edit",
            kind = "edit",
            status = "completed",
            locations = JsonArray(
                listOf(
                    buildJsonObject { put("path", "/repo/src/a.go") },
                    buildJsonObject { put("path", "/repo/src/b.go") },
                    buildJsonObject { put("path", "/repo/src/c.go") },
                ),
            ),
            content = JsonArray(
                listOf(
                    buildJsonObject { put("type", "diff"); put("path", "/repo/src/a.go"); put("oldText", "x"); put("newText", "y") },
                    buildJsonObject { put("type", "diff"); put("path", "/repo/src/b.go"); put("oldText", "1"); put("newText", "2") },
                    buildJsonObject { put("type", "diff"); put("path", "/repo/src/c.go"); put("oldText", "m"); put("newText", "n") },
                ),
            ),
        )
        assertEquals("a.go (+2 more)", toolTitle(multiEdit))

        val singleEdit = Tool(
            callId = "call-edit-1",
            name = "Edit",
            kind = "edit",
            status = "completed",
            locations = JsonArray(listOf(buildJsonObject { put("path", "/repo/src/only.go") })),
            content = JsonArray(
                listOf(
                    buildJsonObject { put("type", "diff"); put("path", "/repo/src/only.go"); put("oldText", "a"); put("newText", "b") },
                ),
            ),
        )
        assertEquals("only.go", toolTitle(singleEdit))

        // 3. TranscriptActions.isCodex & PermCard option labels preserve distinct Codex decisions.
        val dummyKey = Noise.generateKeyPair()
        val dummyConn = HostConnection(
            config = HostConfig(id = "h1", name = "DevBox", url = "http://127.0.0.1:9473", hostKey = "AAAA", devicePub = "BBBB", devicePrivWrapped = "CCCC"),
            http = OkHttpClient(),
            deviceName = "UnitTest",
            onNotifySeq = { _, _ -> },
            lastNotifSeq = { 0L },
            keyPairProvider = { dummyKey },
        )
        try {
            val codexActions = TranscriptActions(
                conn = dummyConn,
                sessionId = "s-codex",
                onUserLong = {},
                onAgentLong = {},
                onSendText = {},
                onPrefill = {},
                onOpenFile = {},
                onQueueAction = { _, _ -> },
                onApprovePlan = {},
                agent = "codex",
            )
            assertTrue(codexActions.isCodex)
            assertFalse(codexActions.isClaude)
            assertEquals("Codex", codexActions.label)

            val codexOpts = listOf(
                PermOption("accept", "Allow once", "allow_once"),
                PermOption("acceptForSession", "Allow for session", "allow_always"),
                PermOption("decline", "Decline", "reject_once"),
                PermOption("cancel", "Cancel turn", "reject_once"),
            )
            assertEquals(
                listOf("Allow once", "Allow for session", "Decline", "Cancel turn"),
                codexOpts.map { permOptionLabel(it, codexActions.isClaude, codexActions.isCodex) },
            )
            // Standard agy fallback still maps allow_always -> "Always allow" and reject_once -> "Deny".
            assertEquals(
                listOf("Allow once", "Always allow", "Deny", "Deny"),
                codexOpts.map { permOptionLabel(it, isClaude = false, isCodex = false) },
            )
        } finally {
            dummyConn.close()
        }
    }
}
