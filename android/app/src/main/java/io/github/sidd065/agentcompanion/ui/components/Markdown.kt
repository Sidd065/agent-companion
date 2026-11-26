package io.github.sidd065.agentcompanion.ui.components

import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.MaterialTheme
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.outlined.CheckBox
import androidx.compose.material.icons.outlined.CheckBoxOutlineBlank
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.isSpecified
import com.mikepenz.markdown.compose.components.markdownComponents
import com.mikepenz.markdown.compose.elements.MarkdownCheckBox
import com.mikepenz.markdown.compose.elements.MarkdownHighlightedCodeBlock
import com.mikepenz.markdown.compose.elements.MarkdownHighlightedCodeFence
import com.mikepenz.markdown.compose.elements.MarkdownTable
import com.mikepenz.markdown.compose.elements.MarkdownTableHeader
import com.mikepenz.markdown.compose.elements.MarkdownTableRow
import com.mikepenz.markdown.m3.Markdown
import com.mikepenz.markdown.m3.markdownColor
import com.mikepenz.markdown.m3.markdownTypography
import com.mikepenz.markdown.model.markdownAnnotator
import com.mikepenz.markdown.model.markdownPadding
import androidx.compose.ui.unit.dp
import com.mikepenz.markdown.model.rememberMarkdownState
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import io.github.sidd065.agentcompanion.ui.theme.codeStyle
import org.intellij.markdown.MarkdownElementTypes
import org.intellij.markdown.MarkdownTokenTypes
import org.intellij.markdown.ast.ASTNode
import org.intellij.markdown.flavours.gfm.GFMElementTypes
import org.intellij.markdown.flavours.gfm.GFMTokenTypes

/** HTML tags a Markdown renderer may hide; any other `<Name>` is text, like a generic type. */
private val knownHtml = setOf(
    "a", "abbr", "b", "bdi", "bdo", "big", "blockquote", "br", "center", "cite", "code", "dd", "del", "details", "dfn",
    "div", "dl", "dt", "em", "font", "h1", "h2", "h3", "h4", "h5", "h6", "hr", "i", "img", "input", "ins", "kbd", "li",
    "mark", "ol", "p", "picture", "pre", "q", "rp", "rt", "ruby", "s", "samp", "small", "source", "span", "strike",
    "strong", "sub", "summary", "sup", "table", "tbody", "td", "tfoot", "th", "thead", "time", "tr", "tt", "u", "ul",
    "var", "video", "wbr",
)
private val tagName = Regex("^</?([A-Za-z][A-Za-z0-9-]*)")
private val unescapedDollar = Regex("(?<!\\\\)\\$")

private fun ASTNode.sibling(delta: Int): ASTNode? {
    val kids = parent?.children ?: return null
    val i = kids.indexOfFirst { it === this }
    return if (i < 0) null else kids.getOrNull(i + delta)
}

/** The text between a node's first and last child (its delimiters), or null. */
private fun ASTNode.between(content: String): String? {
    val open = children.firstOrNull() ?: return null
    val close = children.lastOrNull() ?: return null
    if (open === close || close.startOffset < open.endOffset) return null
    return content.substring(open.endOffset, close.startOffset)
}

/** The TeX inside `$…$` when it reads as math, not as prices ("$5 and $10") or shell variables. */
internal fun inlineMath(content: String, node: ASTNode): String? {
    val tex = node.between(content) ?: return null
    if (tex.isEmpty() || tex.first().isWhitespace() || tex.last().isWhitespace()) return null
    if (unescapedDollar.containsMatchIn(tex)) return null
    if (content.getOrNull(node.endOffset)?.isDigit() == true) return null
    return tex
}

/**
 * What the renderer gets wrong for agents' text: code spans lose `<…>` (taken
 * for HTML), unknown tags like `List<String>` vanish, and math shows as TeX.
 * Returns true when it wrote [node] itself.
 */
internal fun AnnotatedString.Builder.annotateExtras(content: String, node: ASTNode, code: SpanStyle, math: SpanStyle): Boolean =
    when (node.type) {
        MarkdownElementTypes.CODE_SPAN -> {
            val kids = node.children
            val open = kids.firstOrNull()?.takeIf { it.type == MarkdownTokenTypes.BACKTICK }
            val close = kids.lastOrNull()?.takeIf { it.type == MarkdownTokenTypes.BACKTICK }
            if (open == null || close == null || open === close) false
            else {
                var inner = content.substring(open.endOffset, close.startOffset).replace('\n', ' ')
                if (inner.length >= 2 && inner.first() == ' ' && inner.last() == ' ' && inner.isNotBlank()) inner = inner.substring(1, inner.length - 1)
                withStyle(code) { append(' '); append(inner); append(' ') }
                true
            }
        }
        MarkdownTokenTypes.HTML_TAG -> {
            val raw = content.substring(node.startOffset, node.endOffset)
            val name = tagName.find(raw)?.groupValues?.get(1)?.lowercase()
            when {
                name == "br" -> { append('\n'); true }
                name == null || name in knownHtml -> false // comments and real tags stay hidden
                else -> { append(raw); true }
            }
        }
        GFMElementTypes.INLINE_MATH -> {
            val tex = inlineMath(content, node)
            if (tex == null) false else { withStyle(math) { Tex.append(this, tex) }; true }
        }
        GFMElementTypes.BLOCK_MATH -> {
            val tex = node.between(content)?.trim()
            if (tex.isNullOrEmpty()) false
            else {
                // Its own line: the line breaks around it come from the EOLs (below) or here.
                val before = node.sibling(-1)
                if (before != null && before.type != MarkdownTokenTypes.EOL) append('\n')
                withStyle(math) { Tex.append(this, tex) }
                val after = node.sibling(1)
                if (after != null && after.type != MarkdownTokenTypes.EOL) append('\n')
                true
            }
        }
        MarkdownTokenTypes.EOL -> {
            if (node.sibling(-1)?.type == GFMElementTypes.BLOCK_MATH || node.sibling(1)?.type == GFMElementTypes.BLOCK_MATH) {
                append('\n'); true
            } else false
        }
        GFMTokenTypes.DOLLAR -> false
        else -> false
    }

/**
 * Markdown with syntax-highlighted code; keeps the previous render while
 * re-parsing streamed text. Code blocks get a header with the language and
 * a copy button.
 *
 * [style] replaces the body text style (headings scale from it) and [color]
 * tints all text, e.g. for muted, italic thoughts. [immediate] parses during
 * composition, so the first layout already has the real height (lists that
 * scroll to an item right away need that).
 */
@Composable
fun MarkdownText(
    text: String,
    modifier: Modifier = Modifier,
    selectable: Boolean = true,
    immediate: Boolean = false,
    style: TextStyle? = null,
    color: Color = Color.Unspecified,
    compact: Boolean = false,
) {
    val segments = remember(text) { splitRichMarkdownSegments(text) }
    if (segments.size > 1 || segments.firstOrNull() !is RichMarkdownSegment.Standard) {
        val hostId = io.github.sidd065.agentcompanion.ui.LocalHostId.current
        androidx.compose.foundation.layout.Column(
            modifier = modifier,
            verticalArrangement = androidx.compose.foundation.layout.Arrangement.spacedBy(if (compact) 4.dp else 8.dp),
        ) {
            segments.forEach { seg ->
                when (seg) {
                    is RichMarkdownSegment.Standard -> MarkdownLeaf(seg.markdown, selectable = selectable, immediate = immediate, style = style, color = color, compact = compact)
                    is RichMarkdownSegment.Mermaid -> MermaidDiagram(seg.source)
                    is RichMarkdownSegment.Carousel -> CarouselView(seg.source, selectable = selectable)
                    is RichMarkdownSegment.Alert -> GitHubAlertCard(seg.data, selectable = selectable)
                    is RichMarkdownSegment.Media -> {
                        if (isVideoPath(seg.path)) {
                            RemoteVideoPlayerCard(hostId = hostId, remotePath = seg.path, caption = seg.caption)
                        } else {
                            coil3.compose.AsyncImage(
                                model = if (hostId != null) io.github.sidd065.agentcompanion.net.RemoteImage(hostId, path = seg.path) else seg.path,
                                contentDescription = seg.caption,
                                contentScale = androidx.compose.ui.layout.ContentScale.FillWidth,
                                modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp),
                            )
                        }
                    }
                }
            }
        }
        return
    }
    MarkdownLeaf(text, modifier = modifier, selectable = selectable, immediate = immediate, style = style, color = color, compact = compact)
}

@Composable
private fun MarkdownLeaf(
    text: String,
    modifier: Modifier = Modifier,
    selectable: Boolean = true,
    immediate: Boolean = false,
    style: TextStyle? = null,
    color: Color = Color.Unspecified,
    compact: Boolean = false,
) {
    val ex = LocalExtra.current
    val t = MaterialTheme.typography
    val state = rememberMarkdownState(text, retainState = true, immediate = immediate)
    val base = (style ?: t.bodyMedium).let { if (color != Color.Unspecified) it.copy(color = color) else it }
    // With a custom style, headings stay close to the body size (a thought's
    // "**Planning**" line, not a page title).
    val heading = { scale: Float ->
        base.copy(fontSize = if (base.fontSize.isSpecified) base.fontSize * scale else base.fontSize, fontWeight = FontWeight.SemiBold)
    }
    val inlineCode = base.copy(fontFamily = FontFamily.Monospace, fontStyle = FontStyle.Normal)
    val annotator = remember(inlineCode, ex.codeBg) {
        val code = inlineCode.copy(background = ex.codeBg).toSpanStyle()
        val math = SpanStyle(fontFamily = FontFamily.Serif)
        markdownAnnotator(annotate = { content, node -> annotateExtras(content, node, code, math) })
    }
    val body = @Composable {
        Markdown(
            markdownState = state,
            colors = if (color != Color.Unspecified) markdownColor(text = color, codeBackground = ex.codeBg, inlineCodeBackground = ex.codeBg)
            else markdownColor(codeBackground = ex.codeBg, inlineCodeBackground = ex.codeBg),
            typography = markdownTypography(
                text = base,
                paragraph = base,
                code = codeStyle,
                inlineCode = inlineCode,
                h1 = if (style == null) t.titleLarge else heading(1.3f),
                h2 = if (style == null) t.titleMedium else heading(1.2f),
                h3 = if (style == null) t.titleSmall else heading(1.1f),
                h4 = if (style == null) t.bodyLarge else heading(1.05f),
                h5 = if (style == null) t.bodyMedium else heading(1f),
                h6 = if (style == null) t.bodyMedium else heading(1f),
                quote = base.copy(color = ex.muted),
                bullet = base,
                list = base,
                ordered = base,
                table = base,
            ),
            components = markdownComponents(
                // Task lists ("- [ ] step") get boxes, not "[ ]" text.
                checkbox = { m ->
                    MarkdownCheckBox(m.content, m.node, m.typography.text, checkedIndicator = { checked, mod ->
                        androidx.compose.material3.Icon(
                            if (checked) androidx.compose.material.icons.Icons.Outlined.CheckBox else androidx.compose.material.icons.Icons.Outlined.CheckBoxOutlineBlank,
                            if (checked) "done" else "to do",
                            mod.padding(end = 4.dp).size(18.dp),
                            tint = if (checked) ex.success else MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    })
                },
                codeBlock = { MarkdownHighlightedCodeBlock(content = it.content, node = it.node, style = it.typography.code, showHeader = true) },
                codeFence = { m ->
                    val rawFence = m.content.substring(m.node.startOffset, m.node.endOffset)
                    val (lang, inner) = extractFencedCodeContent(rawFence)
                    when (lang) {
                        "mermaid" -> MermaidDiagram(inner)
                        "carousel" -> CarouselView(inner, selectable = selectable)
                        else -> MarkdownHighlightedCodeFence(content = m.content, node = m.node, style = m.typography.code, showHeader = true)
                    }
                },
                // Cells wrap instead of being cut off after one line.
                table = { m ->
                    MarkdownTable(
                        content = m.content, node = m.node, style = m.typography.table,
                        headerBlock = { content, header, tableWidth, style ->
                            MarkdownTableHeader(content = content, header = header, tableWidth = tableWidth, style = style,
                                maxLines = Int.MAX_VALUE, overflow = TextOverflow.Clip)
                        },
                        rowBlock = { content, row, tableWidth, style ->
                            MarkdownTableRow(content = content, header = row, tableWidth = tableWidth, style = style,
                                maxLines = Int.MAX_VALUE, overflow = TextOverflow.Clip)
                        },
                    )
                },
            ),
            annotator = annotator,
            // Compact: no gaps around a one-item list (artifact blocks stack their own).
            padding = if (compact) markdownPadding(block = 0.dp, list = 0.dp, listItemTop = 0.dp, listItemBottom = 0.dp) else markdownPadding(),
            modifier = modifier,
        )
    }
    if (selectable) SelectionContainer { body() } else body()
}
