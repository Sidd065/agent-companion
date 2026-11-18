package io.github.sidd065.agentcompanion.ui.components

import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.style.BaselineShift
import org.intellij.markdown.MarkdownTokenTypes
import org.intellij.markdown.ast.ASTNode
import org.intellij.markdown.flavours.gfm.GFMFlavourDescriptor
import org.intellij.markdown.parser.MarkdownParser
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class MarkdownExtrasTest {
    private val d = "$"

    /** A paragraph's text as rendered: annotateExtras where it applies, the raw source elsewhere. */
    private fun render(md: String): AnnotatedString {
        val tree = MarkdownParser(GFMFlavourDescriptor()).buildMarkdownTreeFromString(md)
        val b = AnnotatedString.Builder()
        fun walk(n: ASTNode) {
            for (c in n.children) {
                if (b.annotateExtras(md, c, SpanStyle(), SpanStyle())) continue
                if (c.type == MarkdownTokenTypes.HTML_TAG) continue // the renderer hides tags
                if (c.children.isEmpty()) b.append(md.substring(c.startOffset, c.endOffset)) else walk(c)
            }
        }
        walk(tree)
        return b.toAnnotatedString()
    }

    @Test fun codeSpansKeepAngleBrackets() {
        assertEquals("Add  team: List<PokemonInstance> = emptyList()  to it", render("Add `team: List<PokemonInstance> = emptyList()` to it").text)
        assertEquals("x  a `b` c  y", render("x `` a `b` c `` y").text)
    }

    @Test fun unknownTagsAreText() {
        assertEquals("Returns List<String>, a\nb and <T>", render("Returns List<String>, a<br>b and <T>").text)
        // Real formatting tags stay hidden (their contents show).
        assertEquals("a bold c", render("a <b>bold</b> c").text)
    }

    @Test fun inlineMath() {
        assertEquals("At meal k, done", render("At meal ${d}k${d}, done").text)
        val r = render("prob ${d}c = \\min(1.0,\\; c_{\\text{base}}(k))${d}")
        assertEquals("prob c = min(1.0, cbase(k))", r.text)
        val base = r.text.indexOf("base")
        assertTrue(r.spanStyles.any { it.start <= base && it.end >= base + 4 && it.item.baselineShift == BaselineShift.Subscript })
    }

    @Test fun pricesAndVariablesAreNotMath() {
        assertEquals("costs ${d}5 and ${d}10 total", render("costs ${d}5 and ${d}10 total").text)
        assertEquals("price ${d}5. Then ${d}x${d} ok", render("price ${d}5. Then ${d}x${d} ok").text)
        assertEquals("use ${d}HOME and ${d}PATH", render("use ${d}HOME and ${d}PATH").text)
    }

    @Test fun blockMathGetsItsOwnLine() {
        assertEquals("Formula:\npot(k) = x × 2\nafter", render("Formula:\n${d}${d}\\text{pot}(k) = x \\times 2${d}${d}\nafter").text)
        val r = render("a ${d}${d}x^2${d}${d} b")
        assertEquals("a\nx2\nb", r.text.replace(" \n", "\n").replace("\n ", "\n"))
        val two = r.text.indexOf('2')
        assertTrue(r.spanStyles.any { it.start <= two && it.end > two && it.item.baselineShift == BaselineShift.Superscript })
    }

    @Test fun tex() {
        assertEquals("α + β ≤ γ", Tex.plain("\\alpha + \\beta \\le \\gamma"))
        assertEquals("(a+b)/2", Tex.plain("\\frac{a+b}{2}"))
        assertEquals("√x · √(x+1)", Tex.plain("\\sqrt{x} \\cdot \\sqrt{x+1}"))
        assertEquals("pot = jsRound((potSize × sundayMult(k) + min(200, bonus)) × campMult)",
            Tex.plain("\\text{pot} = \\text{jsRound}\\!\\Big(\\big(\\text{potSize} \\times \\text{sundayMult}(k) + \\min(200,\\; \\text{bonus})\\big) \\times \\text{campMult}\\Big)"))
        assertEquals("x ∈ ℝ, n → ∞", Tex.plain("x \\in \\mathbb{R}, n \\to \\infty"))
        assertEquals("x̂", Tex.plain("\\hat{x}"))
        assertEquals("50%", Tex.plain("50\\%"))
        assertEquals("a\nb", Tex.plain("a \\\\ b").replace(" \n", "\n").replace("\n ", "\n"))
        assertEquals("unknowncmd", Tex.plain("\\unknowncmd"))
        assertEquals("c_base", Tex.plain("\\text{c\\_base}"))
    }

    @Test fun texSurvivesNesting() {
        val deep = "{".repeat(5000) + "x" + "}".repeat(5000)
        assertTrue(Tex.plain(deep).contains("x"))
        assertTrue(Tex.plain("\\frac{" + "\\frac{".repeat(3000) + "a}{b}").isNotEmpty())
    }
}
