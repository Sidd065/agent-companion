package io.github.sidd065.agentcompanion.ui.chat

import org.junit.Assert.assertEquals
import org.junit.Test

class SubToolSummaryTest {
    @Test fun picksTheUsefulField() {
        assertEquals("git show 757cda7 | head", subToolSummary("""{"command":"git show 757cda7 | head","description":"Show the commit"}"""))
        assertEquals("/repo/Engine.kt", subToolSummary("""{"file_path":"/repo/Engine.kt","limit":40}"""))
        assertEquals("say \"hi\"\nnext", subToolSummary("""{"command":"say \"hi\"\nnext"}"""))
        // Truncated JSON still yields the command.
        assertEquals("long command", subToolSummary("""{"command":"long command","description":"a very long descr…"""))
        assertEquals("{\"a\":1}", subToolSummary("""{"a":1}"""))
        assertEquals("", subToolSummary(""))
    }
}
