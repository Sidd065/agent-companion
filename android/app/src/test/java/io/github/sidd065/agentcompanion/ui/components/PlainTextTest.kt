package io.github.sidd065.agentcompanion.ui.components

import io.github.sidd065.agentcompanion.ui.chat.plainPreview
import org.junit.Assert.assertEquals
import org.junit.Test

class PlainTextTest {
    @Test fun keepsWordsWithUnderscores() {
        assertEquals("implementation_plan.md is ready", plainPreview("**implementation_plan.md** is ready"))
        assertEquals("snake_case and 2 * 3 * 4", plainPreview("snake_case and 2 * 3 * 4"))
    }

    @Test fun stripsMarks() {
        assertEquals("Added the meal plan on agy/meal-plan-procs (0402c96).\n\nSummary\n• TeamTab.kt: added MealPlanCard",
            markdownToPlain("Added the meal plan on `agy/meal-plan-procs` (`0402c96`).\n\n### Summary\n- [TeamTab.kt](file:///home/x/TeamTab.kt): added `MealPlanCard`"))
        assertEquals("a b c d", plainPreview("_a_ __b__ *c* ~~d~~"))
        assertEquals("quoted", markdownToPlain("> quoted"))
        assertEquals("code_with_marks *x*", markdownToPlain("`code_with_marks *x*`"))
        assertEquals("x\ny", markdownToPlain("```kotlin\nx\n```\ny"))
    }

    @Test fun math() {
        val d = "$"
        assertEquals("Let n be k, O(n log k) and costs ${d}5 or ${d}10", markdownToPlain("Let ${d}n${d} be ${d}k${d}, ${d}O(n \\log k)${d} and costs ${d}5 or ${d}10"))
        assertEquals("T(n) ≤ n · k", markdownToPlain("${d}${d}T(n) \\le n \\cdot k${d}${d}"))
    }
}
