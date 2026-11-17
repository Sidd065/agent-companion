package io.github.sidd065.agentcompanion.ui.components

import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import com.mikepenz.markdown.compose.components.markdownComponents
import com.mikepenz.markdown.compose.elements.MarkdownHighlightedCodeBlock
import com.mikepenz.markdown.compose.elements.MarkdownHighlightedCodeFence
import com.mikepenz.markdown.compose.elements.MarkdownTable
import com.mikepenz.markdown.compose.elements.MarkdownTableHeader
import com.mikepenz.markdown.compose.elements.MarkdownTableRow
import androidx.compose.ui.text.style.TextOverflow
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
 */
@Composable
fun MarkdownText(text: String, modifier: Modifier = Modifier, selectable: Boolean = true, immediate: Boolean = false) {
    val ex = LocalExtra.current
    val t = MaterialTheme.typography
    // immediate: parse during composition, so the first layout already has the real height
    // (lists that scroll to an item right away need that).
    val state = rememberMarkdownState(text, retainState = true, immediate = immediate)
    val body = @Composable {
        Markdown(
            markdownState = state,
            colors = markdownColor(codeBackground = ex.codeBg, inlineCodeBackground = ex.codeBg),
            typography = markdownTypography(
                text = t.bodyMedium,
                paragraph = t.bodyMedium,
                code = codeStyle,
                inlineCode = t.bodyMedium.copy(fontFamily = FontFamily.Monospace),
                h1 = t.titleLarge,
                h2 = t.titleMedium,
                h3 = t.titleSmall,
                h4 = t.bodyLarge,
                h5 = t.bodyMedium,
                h6 = t.bodyMedium,
                quote = t.bodyMedium.copy(color = ex.muted),
                bullet = t.bodyMedium,
                list = t.bodyMedium,
                ordered = t.bodyMedium,
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

@Suppress("unused")
private val unusedStyle = TextStyle()
