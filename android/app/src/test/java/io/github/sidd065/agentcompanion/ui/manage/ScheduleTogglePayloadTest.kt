package io.github.sidd065.agentcompanion.ui.manage

import io.github.sidd065.agentcompanion.core.AppJson
import io.github.sidd065.agentcompanion.core.ScheduledTask
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.long
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

class ScheduleTogglePayloadTest {
    @Test
    fun deserializesModeAndPreservesAllFieldsAcrossCronIntervalAndOnceToggles() {
        val rawCronJson = """
            {
              "id": "sched-1",
              "name": "Morning Review",
              "enabled": true,
              "kind": "cron",
              "cron": "0 9 * * 1-5",
              "projectId": "proj-alpha",
              "cwd": "/home/dev/repo",
              "agent": "codex",
              "model": "google/gemini-3-flash-preview",
              "mode": "auto",
              "prompt": "Audit open PRs",
              "useWorktree": true
            }
        """.trimIndent()

        val cronTask = AppJson.decodeFromString(ScheduledTask.serializer(), rawCronJson)
        assertEquals("auto", cronTask.mode)

        val cronToggled = buildScheduleTogglePayload(cronTask, enabled = false)
        assertEquals("sched-1", cronToggled["id"]?.jsonPrimitive?.content)
        assertFalse(cronToggled["enabled"]!!.jsonPrimitive.boolean)
        assertEquals("0 9 * * 1-5", cronToggled["cron"]?.jsonPrimitive?.content)
        assertEquals("proj-alpha", cronToggled["projectId"]?.jsonPrimitive?.content)
        assertEquals("google/gemini-3-flash-preview", cronToggled["model"]?.jsonPrimitive?.content)
        assertEquals("auto", cronToggled["mode"]?.jsonPrimitive?.content)
        assertFalse(cronToggled.containsKey("intervalSec"))
        assertFalse(cronToggled.containsKey("runAt"))

        val intervalTask = ScheduledTask(
            id = "sched-2",
            name = "Heartbeat",
            enabled = true,
            kind = "interval",
            intervalSec = 120L,
            cwd = "/home/dev/repo",
            agent = "codex",
            model = "openrouter/free",
            mode = "full-access",
            prompt = "Check status",
            useWorktree = false,
        )
        val intervalOff = buildScheduleTogglePayload(intervalTask, enabled = false)
        assertEquals(120L, intervalOff["intervalSec"]?.jsonPrimitive?.long)
        assertEquals("openrouter/free", intervalOff["model"]?.jsonPrimitive?.content)
        assertEquals("full-access", intervalOff["mode"]?.jsonPrimitive?.content)

        val onceTask = ScheduledTask(
            id = "sched-3",
            name = "One-Shot",
            enabled = false,
            kind = "once",
            runAt = 1_800_000_999_000L,
            cwd = "/home/dev/repo",
            projectId = "proj-beta",
            agent = "claude",
            prompt = "Run migration",
        )
        val onceOn = buildScheduleTogglePayload(onceTask, enabled = true)
        assertEquals(true, onceOn["enabled"]?.jsonPrimitive?.boolean)
        assertEquals(1_800_000_999_000L, onceOn["runAt"]?.jsonPrimitive?.long)
        assertEquals("proj-beta", onceOn["projectId"]?.jsonPrimitive?.content)
    }
}
