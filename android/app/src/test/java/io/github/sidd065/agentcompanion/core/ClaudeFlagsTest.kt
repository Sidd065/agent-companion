package io.github.sidd065.agentcompanion.core

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ClaudeFlagsTest {
    @Test fun defaultsSkipPermissionsOnly() {
        val f = ClaudeFlags()
        assertEquals("claude -p --dangerously-skip-permissions", f.headlessPreview())
        assertEquals("claude --dangerously-skip-permissions", f.tuiPreview())
        assertEquals(emptyList<String>(), f.tuiArgs()) // skip is sent separately for terminals
        val j = f.daemonJson()
        assertEquals(true, j["skipPermissions"]!!.jsonPrimitive.content.toBoolean())
        assertNull(j["extraArgs"])
    }

    @Test fun modeReplacesSkipInPreviews() {
        val f = ClaudeFlags(mode = "plan", model = "haiku", effort = "high")
        assertEquals("claude -p --model haiku --effort high --permission-mode plan", f.headlessPreview())
        assertEquals(listOf("--model", "haiku", "--effort", "high", "--permission-mode", "plan"), f.tuiArgs())
        val j = f.daemonJson()
        assertEquals("plan", j["mode"]!!.jsonPrimitive.content)
        assertEquals("high", j["thought"]!!.jsonPrimitive.content)
        assertEquals("haiku", j["model"]!!.jsonPrimitive.content)
    }

    @Test fun extrasGoToTheRightPlaces() {
        val f = ClaudeFlags(
            skipPermissions = false, allowedTools = "Bash(git:*) Edit", appendSystemPrompt = "Be terse", maxBudgetUsd = "2.5",
            strictMcpConfig = true, mcpConfig = "/tmp/mcp.json", extra = "--verbose", worktree = true, tuiExtra = "--ide",
            addDirs = listOf("/srv/data"), agent = "reviewer",
        )
        val extra = (f.daemonJson()["extraArgs"] as JsonArray).map { it.jsonPrimitive.content }
        // The tool list stays one argument (it contains a space).
        assertTrue(extra.containsAll(listOf("--allowedTools", "Bash(git:*) Edit", "--append-system-prompt", "Be terse", "--max-budget-usd", "2.5",
            "--mcp-config", "/tmp/mcp.json", "--strict-mcp-config", "--verbose")))
        assertFalse("--worktree" in extra) // terminals only
        val tui = f.tuiArgs()
        assertTrue(tui.containsAll(listOf("--agent", "reviewer", "--add-dir", "/srv/data", "--worktree", "--ide", "--allowedTools", "Bash(git:*) Edit")))
        assertFalse("--max-budget-usd" in tui) // only works with -p
        assertEquals("reviewer", f.daemonJson()["agentName"]!!.jsonPrimitive.content)
    }

    @Test fun resumeKeepsTheConversationsOwnChoices() {
        val f = ClaudeFlags(model = "opus", effort = "max", mode = "plan", agent = "x", worktree = true, addDirs = listOf("/a"))
        val r = f.tuiResumeArgs()
        assertFalse("--model" in r || "--effort" in r || "--permission-mode" in r || "--agent" in r || "--worktree" in r)
        assertTrue(r.containsAll(listOf("--add-dir", "/a")))
    }

    @Test fun settingsPickFlagsPerAgent() {
        val s = AppSettings(agyFlags = AgyFlags(skipPermissions = true), claudeFlags = ClaudeFlags(skipPermissions = false))
        assertEquals(false, s.skipPermissions("claude"))
        assertEquals(true, s.skipPermissions("agy"))
        assertNull(s.flagsJson("claude")["sandbox"]) // agy-only options never reach Claude
    }
}
