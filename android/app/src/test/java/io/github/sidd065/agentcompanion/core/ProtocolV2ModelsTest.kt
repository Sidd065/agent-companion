package io.github.sidd065.agentcompanion.core

import org.junit.Assert.assertEquals
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
        assertEquals("OpenAI Codex", agentProduct("codex"))
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
}
