package io.github.sidd065.agentcompanion.ui.components

import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.isSpecified
import com.mikepenz.markdown.compose.components.markdownComponents
import com.mikepenz.markdown.compose.elements.MarkdownHighlightedCodeBlock
import com.mikepenz.markdown.compose.elements.MarkdownHighlightedCodeFence
import com.mikepenz.markdown.compose.elements.MarkdownTable
import com.mikepenz.markdown.compose.elements.MarkdownTableHeader
import com.mikepenz.markdown.compose.elements.MarkdownTableRow
import com.mikepenz.markdown.m3.Markdown
import com.mikepenz.markdown.m3.markdownColor
import com.mikepenz.markdown.m3.markdownTypography
import com.mikepenz.markdown.model.rememberMarkdownState
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import io.github.sidd065.agentcompanion.ui.theme.codeStyle

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
    val body = @Composable {
        Markdown(
            markdownState = state,
            colors = if (color != Color.Unspecified) markdownColor(text = color, codeBackground = ex.codeBg, inlineCodeBackground = ex.codeBg)
            else markdownColor(codeBackground = ex.codeBg, inlineCodeBackground = ex.codeBg),
            typography = markdownTypography(
                text = base,
                paragraph = base,
                code = codeStyle,
                inlineCode = base.copy(fontFamily = FontFamily.Monospace, fontStyle = FontStyle.Normal),
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
                codeBlock = { MarkdownHighlightedCodeBlock(content = it.content, node = it.node, style = it.typography.code, showHeader = true) },
                codeFence = { MarkdownHighlightedCodeFence(content = it.content, node = it.node, style = it.typography.code, showHeader = true) },
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
            modifier = modifier,
        )
    }
    if (selectable) SelectionContainer { body() } else body()
}
