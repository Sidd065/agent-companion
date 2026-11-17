package io.github.sidd065.agentcompanion.net

import io.github.sidd065.agentcompanion.core.Aside
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class AsidePromptTest {
    private val list = listOf(
        Aside("a", "Which file handles login?", answer = "auth.go", state = "done"),
        Aside("b", "Why there?", answer = "It owns sessions.", state = "done", followUp = "a"),
        Aside("c", "Broken", state = "failed"),
    )

    @Test fun plainQuestionIsSentAsIs() {
        assertEquals("Plain", legacyAsidePrompt(list, "Plain", null))
        assertEquals("Plain", legacyAsidePrompt(list, "Plain", "c")) // a failed answer is no context
    }

    @Test fun followUpCarriesTheEarlierExchangeOldestFirst() {
        val p = legacyAsidePrompt(list, "And tests?", "b")
        assertTrue(p.indexOf("auth.go") in 0 until p.indexOf("It owns sessions."))
        assertTrue(p.endsWith("Follow-up question: And tests?"))
    }
}
