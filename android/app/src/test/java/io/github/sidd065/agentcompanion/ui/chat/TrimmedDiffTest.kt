package io.github.sidd065.agentcompanion.ui.chat

import io.github.sidd065.agentcompanion.core.Tool
import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Test

class TrimmedDiffTest {
    private fun tool(content: String) = Tool(callId = "c1", kind = "edit", content = Json.parseToJsonElement(content))

    @Test fun trimmedDiffKeepsFileLineNumbers() {
        // agentc kept lines 497..503 of a long file around a change on line 500.
        val t = tool("""[{"type":"diff","path":"/x/a.py","line":497,
            "oldText":"l497\nl498\nl499\nl500\nl501\nl502\nl503\n",
            "newText":"l497\nl498\nl499\nCHANGED\nl501\nl502\nl503\n"}]""")
        val d = t.diffs().single()
        assertEquals(497, d.startLine)
        val lines = d.lines()
        assertEquals(497, lines.first().oldNo)
        assertEquals(497, lines.first().newNo)
        val removed = lines.single { it.type == '-' }
        val added = lines.single { it.type == '+' }
        assertEquals(500, removed.oldNo)
        assertEquals(0, removed.newNo)
        assertEquals(500, added.newNo)
        assertEquals(503, lines.last().newNo)
    }

    @Test fun untrimmedDiffStartsAtLineOne() {
        val d = tool("""[{"type":"diff","path":"/x/b.py","oldText":"a\nb\n","newText":"a\nc\n"}]""").diffs().single()
        assertEquals(1, d.startLine)
        assertEquals(listOf(1, 2, 0), d.lines().map { it.oldNo })
    }
}
