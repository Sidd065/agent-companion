package io.github.sidd065.agentcompanion.core

import io.github.sidd065.agentcompanion.ui.chat.parseMarkdownBlocks
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class ArtifactBlocksTest {
    @Test fun keepsFencedCodeInsideListItems() {
        val md = """
            # Plan

            Intro paragraph
            continues here.

            - Create `hello.py`.
            - Add code:
              ```python
              def main():
                  print("hi")

              if __name__ == "__main__":
                  main()
              ```
            ### Step 2
            - Run it
            1. numbered
               nested text

               more of the same item
            2. next

            | a | b |
            |---|---|
            | 1 | 2 |
        """.trimIndent()
        val b = parseMarkdownBlocks(md)
        val kinds = b.map { it.kind }
        assertEquals(listOf("heading", "para", "item", "item", "heading", "item", "item", "item", "table"), kinds)
        val code = b[3]
        assertTrue(code.text, code.text.contains("if __name__") && code.text.trimEnd().endsWith("```"))
        assertEquals(7, code.start)
        assertEquals(14, code.end)
        assertEquals("### Step 2", b[4].text)
        assertTrue(b[6].text.contains("more of the same item"))
        assertEquals(3, b[1].start); assertEquals(4, b[1].end)
    }

    @Test fun nestedItemsAreTheirOwnBlocks() {
        val md = """
            - **MealPlanner.kt**:
              - Accept `estimator`.
              - When `team` is non-empty, run the estimator
                and keep going on this line.
                1. a numbered sub-step
            - Next top item

            After the list.
        """.trimIndent()
        val b = parseMarkdownBlocks(md)
        assertEquals(listOf("item", "item", "item", "item", "item", "para"), b.map { it.kind })
        assertEquals(listOf(0, 1, 1, 2, 0, 0), b.map { it.depth })
        assertEquals("- When `team` is non-empty, run the estimator\n  and keep going on this line.", b[2].text)
        assertEquals(3, b[2].start); assertEquals(4, b[2].end)
        assertEquals("1. a numbered sub-step", b[3].text)
    }
}
