package io.github.sidd065.agentcompanion.ui.theme

import android.os.Build
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

@Immutable
data class Extra(
    val success: Color,
    val warning: Color,
    val danger: Color,
    val running: Color,
    val muted: Color,
    val codeBg: Color,
    val diffAdd: Color,
    val diffAddBg: Color,
    val diffDel: Color,
    val diffDelBg: Color,
    val userBubble: Color,
    val onUserBubble: Color,
    val cardBorder: Color,
    val isDark: Boolean,
)

val LocalExtra = staticCompositionLocalOf {
    Extra(Color.Green, Color.Yellow, Color.Red, Color.Blue, Color.Gray, Color.DarkGray, Color.Green, Color.Green, Color.Red, Color.Red, Color.Blue, Color.White, Color.Gray, true)
}

private val DarkColors = darkColorScheme(
    primary = Color(0xFF8AB4FF),
    onPrimary = Color(0xFF08214D),
    primaryContainer = Color(0xFF243A66),
    onPrimaryContainer = Color(0xFFDCE6FF),
    secondary = Color(0xFFB9A6FF),
    onSecondary = Color(0xFF221455),
    secondaryContainer = Color(0xFF332A5C),
    onSecondaryContainer = Color(0xFFE6DEFF),
    tertiary = Color(0xFFFFC48A),
    onTertiary = Color(0xFF3D2300),
    tertiaryContainer = Color(0xFF5A3A12),
    onTertiaryContainer = Color(0xFFFFE2C6),
    background = Color(0xFF101114),
    onBackground = Color(0xFFE6E7EB),
    surface = Color(0xFF101114),
    onSurface = Color(0xFFE6E7EB),
    surfaceVariant = Color(0xFF23262E),
    onSurfaceVariant = Color(0xFFB4B8C3),
    surfaceContainerLowest = Color(0xFF0B0C0E),
    surfaceContainerLow = Color(0xFF15171B),
    surfaceContainer = Color(0xFF1A1C21),
    surfaceContainerHigh = Color(0xFF212329),
    surfaceContainerHighest = Color(0xFF292C33),
    outline = Color(0xFF4A4E59),
    outlineVariant = Color(0xFF30333B),
    error = Color(0xFFFF8A80),
    onError = Color(0xFF4A0D08),
    errorContainer = Color(0xFF5C1A15),
    onErrorContainer = Color(0xFFFFDAD5),
)

private val LightColors = lightColorScheme(
    primary = Color(0xFF3557C9),
    onPrimary = Color.White,
    primaryContainer = Color(0xFFDCE4FF),
    onPrimaryContainer = Color(0xFF0B1E57),
    secondary = Color(0xFF6650C8),
    onSecondary = Color.White,
    secondaryContainer = Color(0xFFE8E0FF),
    onSecondaryContainer = Color(0xFF221455),
    tertiary = Color(0xFF9A5A00),
    onTertiary = Color.White,
    tertiaryContainer = Color(0xFFFFE2C2),
    onTertiaryContainer = Color(0xFF331B00),
    background = Color(0xFFF7F6F3),
    onBackground = Color(0xFF1B1C1F),
    surface = Color(0xFFF7F6F3),
    onSurface = Color(0xFF1B1C1F),
    surfaceVariant = Color(0xFFE6E5E0),
    onSurfaceVariant = Color(0xFF4B4D55),
    surfaceContainerLowest = Color(0xFFFFFFFF),
    surfaceContainerLow = Color(0xFFF2F1ED),
    surfaceContainer = Color(0xFFEDECE8),
    surfaceContainerHigh = Color(0xFFE7E6E2),
    surfaceContainerHighest = Color(0xFFE1E0DC),
    outline = Color(0xFF8D8F98),
    outlineVariant = Color(0xFFD4D3CF),
)

val Mono = FontFamily.Monospace

@Composable
fun AppTheme(theme: String = "system", dynamic: Boolean = false, textScale: Float = 1f, content: @Composable () -> Unit) {
    val dark = when (theme) {
        "dark" -> true
        "light" -> false
        else -> isSystemInDarkTheme()
    }
    val ctx = LocalContext.current
    val colors: ColorScheme = when {
        dynamic && Build.VERSION.SDK_INT >= 31 -> if (dark) dynamicDarkColorScheme(ctx) else dynamicLightColorScheme(ctx)
        dark -> DarkColors
        else -> LightColors
    }
    val extra = if (dark) {
        Extra(
            success = Color(0xFF5BD69A), warning = Color(0xFFFFC48A), danger = Color(0xFFFF8A80), running = Color(0xFF8AB4FF),
            muted = Color(0xFF8C909B), codeBg = Color(0xFF181A1F),
            diffAdd = Color(0xFF7EE2A8), diffAddBg = Color(0x2A2EA043), diffDel = Color(0xFFFF9C94), diffDelBg = Color(0x2AF85149),
            userBubble = Color(0xFF263048), onUserBubble = Color(0xFFE6EBFA), cardBorder = Color(0xFF2A2D35), isDark = true,
        )
    } else {
        Extra(
            success = Color(0xFF1B8A50), warning = Color(0xFFB36200), danger = Color(0xFFC62828), running = Color(0xFF3557C9),
            muted = Color(0xFF6E717A), codeBg = Color(0xFFF0EFEA),
            diffAdd = Color(0xFF14672F), diffAddBg = Color(0x332EA043), diffDel = Color(0xFFA4161A), diffDelBg = Color(0x29F85149),
            userBubble = Color(0xFFE3E9FB), onUserBubble = Color(0xFF131A2D), cardBorder = Color(0xFFDDDCD7), isDark = false,
        )
    }
    val base = Typography()
    fun TextStyle.s() = copy(fontSize = fontSize * textScale, lineHeight = lineHeight * textScale)
    val typography = Typography(
        displayLarge = base.displayLarge, displayMedium = base.displayMedium, displaySmall = base.displaySmall,
        headlineLarge = base.headlineLarge, headlineMedium = base.headlineMedium, headlineSmall = base.headlineSmall.copy(fontWeight = FontWeight.SemiBold),
        titleLarge = base.titleLarge.copy(fontWeight = FontWeight.SemiBold), titleMedium = base.titleMedium.copy(fontWeight = FontWeight.SemiBold), titleSmall = base.titleSmall,
        bodyLarge = base.bodyLarge.s(), bodyMedium = base.bodyMedium.s(), bodySmall = base.bodySmall.s(),
        labelLarge = base.labelLarge, labelMedium = base.labelMedium, labelSmall = base.labelSmall,
    )
    CompositionLocalProvider(LocalExtra provides extra) {
        MaterialTheme(colorScheme = colors, typography = typography, content = content)
    }
}

val codeStyle = TextStyle(fontFamily = Mono, fontSize = 12.5.sp, lineHeight = 17.sp)
