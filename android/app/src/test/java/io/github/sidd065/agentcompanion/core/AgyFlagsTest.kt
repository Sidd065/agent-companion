package io.github.sidd065.agentcompanion.core

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class AgyFlagsTest {
    @Test fun defaultsSkipPermissionsOnly() {
        val f = AgyFlags()
        assertEquals(listOf("--dangerously-skip-permissions"), f.acpArgs())
        assertEquals(emptyList<String>(), f.tuiArgs()) // skip is sent separately for terminals
        assertEquals("agy --dangerously-skip-permissions", f.tuiPreview())
        assertEquals("agy acp --dangerously-skip-permissions", f.acpPreview())
    }

    @Test fun skipCanBeTurnedOff() {
        val f = AgyFlags(skipPermissions = false)
        assertFalse("--dangerously-skip-permissions" in f.acpArgs())
        assertEquals("agy", f.tuiPreview())
        assertEquals(false, f.daemonJson()["skipPermissions"]!!.jsonPrimitive.content.toBoolean())
    }

    @Test fun tuiFlags() {
        val f = AgyFlags(sandbox = true, model = "gemini-4-argon", effort = "max", mode = "plan", agent = "reviewer",
            addDirs = listOf("/a b", "/c"), project = "p1", newProject = true, remoteControl = true, remoteAgent = true,
            settingsJson = """{"x":1}""", tuiExtra = "--log-file '/tmp/my log'")
        assertEquals(
            listOf("--sandbox", "--model", "gemini-4-argon", "--effort", "max", "--mode", "plan", "--agent", "reviewer",
                "--add-dir", "/a b", "--add-dir", "/c", "--project", "p1", "--new-project", "--remote-control", "--remote-agent",
                "--settings", """{"x":1}""", "--log-file", "/tmp/my log"),
            f.tuiArgs(),
        )
        assertTrue(f.tuiPreview().contains("'/a b'"))
    }

    @Test fun defaultModeHasNoTuiFlag() {
        assertEquals(emptyList<String>(), AgyFlags(mode = "default").tuiArgs())
        assertEquals("default", AgyFlags(mode = "default").daemonJson()["mode"]!!.jsonPrimitive.content)
    }

    @Test fun acpFlagsAndDaemonJson() {
        val f = AgyFlags(sandbox = true, agent = "a1", disableTelemetry = true, includeDefaultCustomizations = false,
            acpExtra = "--process-per-session", model = "m", effort = "high", mode = "accept-edits", addDirs = listOf("/x"))
        assertEquals(
            listOf("--dangerously-skip-permissions", "--sandbox", "--agent", "a1", "--add-dir", "/x", "--disable-telemetry",
                "--include-default-customizations=false", "--process-per-session"),
            f.acpArgs(),
        )
        val j = f.daemonJson()
        assertEquals("a1", j["agentName"]!!.jsonPrimitive.content)
        assertEquals("high", j["thought"]!!.jsonPrimitive.content)
        assertEquals("m", j["model"]!!.jsonPrimitive.content)
        assertEquals(listOf("--include-default-customizations=false", "--process-per-session"),
            (j["extraArgs"] as JsonArray).map { it.jsonPrimitive.content })
        assertNull(AgyFlags().daemonJson()["extraArgs"])
    }

    @Test fun resumeArgsKeepConversationSettings() {
        val f = AgyFlags(sandbox = true, model = "m", effort = "max", mode = "plan", agent = "a", project = "p", newProject = true,
            remoteAgent = true, remoteControl = true, addDirs = listOf("/x"), settingsJson = "{}", tuiExtra = "--log-file /tmp/l")
        assertEquals(listOf("--sandbox", "--add-dir", "/x", "--remote-control", "--settings", "{}", "--log-file", "/tmp/l"), f.tuiResumeArgs())
    }

    @Test fun splitAndQuote() {
        assertEquals(listOf("a", "b c", "d\"e", "f g"), splitArgs("""a "b c" "d\"e" 'f g'"""))
        assertEquals(emptyList<String>(), splitArgs("   "))
        assertEquals("plain-arg", shellQuote("plain-arg"))
        assertEquals("'it'\\''s'", shellQuote("it's"))
        assertEquals("''", shellQuote(""))
    }
}
