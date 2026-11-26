package io.github.sidd065.agentcompanion.ui

import io.github.sidd065.agentcompanion.core.AppSettings
import io.github.sidd065.agentcompanion.core.LiveState
import io.github.sidd065.agentcompanion.core.LiveSubagent
import io.github.sidd065.agentcompanion.core.SessionSummary
import io.github.sidd065.agentcompanion.core.plainInlineTitle
import io.github.sidd065.agentcompanion.ui.chat.nextNoteSummary
import io.github.sidd065.agentcompanion.ui.chat.parseMarkdownBlocks
import io.github.sidd065.agentcompanion.ui.chat.parseNoteChecklist
import io.github.sidd065.agentcompanion.ui.chat.toggleNoteChecklistLine
import io.github.sidd065.agentcompanion.ui.components.DiagramDirection
import io.github.sidd065.agentcompanion.ui.components.MermaidAst
import io.github.sidd065.agentcompanion.ui.components.computeGraphLayout
import io.github.sidd065.agentcompanion.ui.components.parseGitHubAlert
import io.github.sidd065.agentcompanion.ui.components.parseMermaidDiagram
import io.github.sidd065.agentcompanion.ui.components.splitCarouselSlides
import io.github.sidd065.agentcompanion.ui.sessions.matchesSessionSearch
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class Stage2FeaturesTest {

    @Test
    fun mermaidParserAndLayout_handlesGate1FlowchartWithAmpersandEdges() {
        val gate1 = """
            flowchart LR
              Ref["ref.mp4 (360x640)"] --> CleanRefs["Curated Clean Frames"]
              CleanRefs --> Anchor16["Master Anchor 16:9 (2560x1440)"]
              CleanRefs --> Anchor9["Master Anchor 9:16 (1440x2560)"]
              Anchor16 & Anchor9 --> Calib["3D Pinhole Calibration (Z=0 Pond Plane)"]
              Calib --> Blender["Headless Blender 4.2 on Vast.ai (362f @ 24fps)"]
              Blender --> Mod01["Modules 01..12 (RGB + Depth Passes)"]
              Anchor16 & Mod01 --> H3["MiniMax H3 (FL2VA + Fun ControlNet)"]
              H3 --> QA["qa_check.py (Seam SSIM/LPIPS + Corner Flow + Hist)"]
              QA --> Master["Seamless 3-5m Assembly (Circular 6f Cross-Dissolve)"]
        """.trimIndent()

        val ast = parseMermaidDiagram(gate1) as? MermaidAst.Graph
        assertNotNull(ast)
        assertEquals(DiagramDirection.LR, ast!!.direction)
        assertEquals(10, ast.nodes.size)
        assertEquals(11, ast.edges.size)
        assertTrue(ast.edges.any { it.from == "Anchor16" && it.to == "Calib" })
        assertTrue(ast.edges.any { it.from == "Anchor9" && it.to == "Calib" })
        assertTrue(ast.edges.any { it.from == "Anchor16" && it.to == "H3" })
        assertTrue(ast.edges.any { it.from == "Mod01" && it.to == "H3" })

        val layout = computeGraphLayout(ast, nodeSize = { 120f to 44f })
        assertTrue(layout.nodes["Ref"]!!.x < layout.nodes["CleanRefs"]!!.x)
        assertTrue(layout.nodes["CleanRefs"]!!.x < layout.nodes["Anchor16"]!!.x)
        assertTrue(layout.nodes["Mod01"]!!.x < layout.nodes["H3"]!!.x)
        assertTrue(layout.nodes["H3"]!!.x < layout.nodes["Master"]!!.x)
    }

    @Test
    fun artifactBlocks_preservesFourBacktickCarouselAlertsAndMermaid() {
        val md = """
            > [!IMPORTANT]
            > **GPU Budget & Hygiene Status**: Vast.ai Blender instance `62509883` destroyed.

            ````carousel
            ![blockout_01.mp4 12-Frame Storyboard](/home/siddharth/duckvideo/blockout_01.mp4)
            <!-- slide -->
            ```python
            def example():
                print("nested code block inside slide")
            ```
            ````

            ```mermaid
            sequenceDiagram
              Alice->>Bob: Hello
            ```
        """.trimIndent()

        val blocks = parseMarkdownBlocks(md)
        assertEquals(listOf("alert", "carousel", "mermaid"), blocks.map { it.kind })

        val alert = parseGitHubAlert(blocks[0].text)
        assertNotNull(alert)
        assertEquals("IMPORTANT", alert!!.kind)
        assertTrue(alert.body.contains("GPU Budget"))

        val slides = splitCarouselSlides(blocks[1].text.lines().drop(1).dropLast(1).joinToString("\n"))
        assertEquals(2, slides.size)
        assertTrue(slides[1].contains("```python"))
    }

    @Test
    fun plainInlineTitle_stripsBackticksAndEmphasis() {
        val s = SessionSummary(id = "s1", title = "Setup `git init` in this dir and **push**")
        assertEquals("Setup git init in this dir and push", s.displayTitle)
        assertEquals("Fix app.kt", plainInlineTitle("### Fix [app.kt](file:///tmp/app.kt)"))
    }

    @Test
    fun subagentWorkingState_showsDoneInsteadOfWorkingForFinishedAgents() {
        val now = 1_700_000_000_000L
        val doneSub = LiveSubagent(id = "sub-1", role = "Researcher", state = "DONE", updated = now - 5_000)
        val runningSub = LiveSubagent(id = "sub-2", role = "Coder", state = "RUNNING", updated = now - 5_000)
        val staleAliveSub = LiveSubagent(id = "sub-3", role = "Old Daemon Subagent", state = "ALIVE", updated = now - 3_600_000)

        assertFalse(doneSub.isWorking(sessionRunning = true, nowMs = now))
        assertEquals("done", doneSub.displayState(sessionRunning = true, nowMs = now))

        assertTrue(runningSub.isWorking(sessionRunning = true, nowMs = now))
        assertEquals("working", runningSub.displayState(sessionRunning = true, nowMs = now))

        assertFalse(staleAliveSub.isWorking(sessionRunning = false, nowMs = now))
        assertEquals("done", staleAliveSub.displayState(sessionRunning = false, nowMs = now))

        val live = LiveState(running = false, subagents = listOf(doneSub, staleAliveSub))
        assertTrue(live.activeAgents.isEmpty())
    }

    @Test
    fun notificationFilteringAndSessionNotes_workAsConfigured() {
        val defaults = AppSettings()
        assertTrue(defaults.allowsNotificationKind("permission"))
        assertTrue(defaults.allowsNotificationKind("question"))
        assertTrue(defaults.allowsNotificationKind("done"))
        assertTrue(defaults.allowsNotificationKind("error"))
        assertFalse("Minor progress updates must be OFF by default", defaults.allowsNotificationKind("progress"))
        assertEquals(listOf("input", "done", "halted"), defaults.enabledPushEvents())

        val inputOnly = defaults.copy(notifyOnDone = false, notifyOnHalted = false, notifyOnProgress = false)
        assertTrue(inputOnly.allowsNotificationKind("permission"))
        assertFalse(inputOnly.allowsNotificationKind("done"))
        assertFalse(inputOnly.allowsNotificationKind("error"))
        assertEquals(listOf("input"), inputOnly.enabledPushEvents())

        // Session client-side notes & checklist
        val withNote = defaults.withSessionNote("host-a", "sess-1", "- [ ] Port T3 search\n- [ ] Test Cloudflare tunnel")
        val note = withNote.noteFor("host-a", "sess-1")
        assertNotNull(note)
        assertEquals("Port T3 search", nextNoteSummary(note!!.text))
        val toggled = toggleNoteChecklistLine(note.text, 0)
        assertEquals("Test Cloudflare tunnel", nextNoteSummary(toggled))
        assertEquals(2, parseNoteChecklist(toggled).size)

        // Quoted exact phrase + Turkish dotted İ search
        val sess = SessionSummary(id = "sess-1", title = "İstanbul Tunnel Setup", cwd = "/home/user/project")
        assertTrue(matchesSessionSearch("istanbul", sess, toggled))
        assertTrue(matchesSessionSearch("\"Cloudflare tunnel\"", sess, toggled))
        assertFalse(matchesSessionSearch("\"tunnel Cloudflare\"", sess, toggled))
    }

    @Test
    fun resolveLocalFileTarget_extractsLineNumbersFromFragmentsAndColons() {
        val t1 = resolveLocalFileTarget("file:///home/siddharth/duck_physics.py#L123-L145")
        assertNotNull(t1)
        assertEquals("/home/siddharth/duck_physics.py", t1!!.path)
        assertEquals(123, t1.line)

        val t2 = resolveLocalFileTarget("/home/siddharth/app/Main.kt:42")
        assertNotNull(t2)
        assertEquals("/home/siddharth/app/Main.kt", t2!!.path)
        assertEquals(42, t2.line)
    }
}
