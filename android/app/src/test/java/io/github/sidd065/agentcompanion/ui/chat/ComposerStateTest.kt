package io.github.sidd065.agentcompanion.ui.chat

import org.junit.Assert.assertEquals
import org.junit.Assert.assertSame
import org.junit.Test

class ComposerStateTest {
    @Test fun quoteGoesAfterTheDraftWithRoomToReply() {
        val c = ComposerState()
        c.setText("Please do this:  ")
        c.appendQuote("Use a mutex.\n\n```go\nmu.Lock()\n```", heading = "Side answer to: How to fix the race?")
        assertEquals(
            "Please do this:\n\n> Side answer to: How to fix the race?\n>\n> Use a mutex.\n>\n> ```go\n> mu.Lock()\n> ```\n\n",
            c.value.text,
        )
        assertEquals(c.value.text.length, c.value.selection.start) // cursor ready below the quote
    }

    @Test fun quoteIntoAnEmptyDraft() {
        val c = ComposerState()
        c.appendQuote("One line")
        assertEquals("> One line\n\n", c.value.text)
    }

    @Test fun draftsArePerHostAndSession() {
        val a = Drafts.of("h1", "s1")
        a.setText("draft")
        assertSame(a, Drafts.of("h1", "s1"))
        assertEquals("", Drafts.of("h2", "s1").value.text)
    }
}
