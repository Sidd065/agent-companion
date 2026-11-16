package io.github.sidd065.agentcompanion.ui.components

import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import com.mikepenz.markdown.compose.components.markdownComponents
import com.mikepenz.markdown.compose.elements.highlightedCodeBlock
import com.mikepenz.markdown.compose.elements.highlightedCodeFence
import com.mikepenz.markdown.m3.Markdown
import com.mikepenz.markdown.m3.markdownColor
import com.mikepenz.markdown.m3.markdownTypography
import com.mikepenz.markdown.model.rememberMarkdownState
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import io.github.sidd065.agentcompanion.ui.theme.codeStyle

/** Markdown with syntax-highlighted code; keeps the previous render while re-parsing streamed text. */
@Composable
fun MarkdownText(text: String, modifier: Modifier = Modifier, selectable: Boolean = true) {
    val ex = LocalExtra.current
    val t = MaterialTheme.typography
    val state = rememberMarkdownState(text, retainState = true)
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
            components = markdownComponents(codeBlock = highlightedCodeBlock, codeFence = highlightedCodeFence),
            modifier = modifier,
        )
    }
    if (selectable) SelectionContainer { body() } else body()
}

@Suppress("unused")
private val unusedStyle = TextStyle()
