package io.github.sidd065.agentcompanion.ui.sessions

import io.github.sidd065.agentcompanion.core.GitLog
import org.junit.Assert.assertEquals
import org.junit.Test

class MineLabelTest {
    @Test fun namesTheBase() {
        assertEquals("On main · not pushed to origin/main", mineLabel(GitLog(repo = true, branch = "main", base = "origin/main", upstream = true)))
        assertEquals("On claude/fix · not on main", mineLabel(GitLog(repo = true, branch = "claude/fix", base = "main")))
        // Older daemons send neither field.
        assertEquals("On feature", mineLabel(GitLog(repo = true, branch = "feature")))
        assertEquals("On this branch · not on master", mineLabel(GitLog(repo = true, branch = "HEAD", base = "master")))
    }
}
