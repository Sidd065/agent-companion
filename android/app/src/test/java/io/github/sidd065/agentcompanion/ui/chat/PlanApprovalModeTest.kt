package io.github.sidd065.agentcompanion.ui.chat

import org.junit.Assert.assertEquals
import org.junit.Test

class PlanApprovalModeTest {
    @Test
    fun selectsCanonicalAcceptEditsModeByAvailableModesAndAgent() {
        assertEquals("accept-edits", planApprovalAcceptModeId("agy", emptyList()))
        assertEquals("accept-edits", planApprovalAcceptModeId(null, emptyList()))
        assertEquals("acceptEdits", planApprovalAcceptModeId("codex", emptyList()))
        assertEquals("acceptEdits", planApprovalAcceptModeId("claude", emptyList()))

        assertEquals(
            "acceptEdits",
            planApprovalAcceptModeId("codex", listOf("default", "plan", "acceptEdits", "auto", "full-access")),
        )
        assertEquals(
            "accept-edits",
            planApprovalAcceptModeId("agy", listOf("default", "plan", "accept-edits", "auto")),
        )
        assertEquals(
            "auto-edits",
            planApprovalAcceptModeId("custom-acp", listOf("default", "auto-edits")),
        )
    }
}
