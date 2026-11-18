package io.github.sidd065.agentcompanion.ui.components

import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.nestedscroll.NestedScrollConnection
import androidx.compose.ui.input.nestedscroll.NestedScrollSource
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.unit.Velocity

/**
 * Keeps scroll that's left over at the end of a list (the finger keeps pushing
 * up) away from the bottom sheet around it. The sheet would otherwise try to
 * move with it, re-measure the list and make the content bounce up and down.
 * Pulling down at the top still reaches the sheet, so it can be dragged closed.
 */
private object EndOverflowGuard : NestedScrollConnection {
    override fun onPostScroll(consumed: Offset, available: Offset, source: NestedScrollSource): Offset =
        if (available.y < 0f) Offset(0f, available.y) else Offset.Zero

    override suspend fun onPostFling(consumed: Velocity, available: Velocity): Velocity =
        if (available.y < 0f) Velocity(0f, available.y) else Velocity.Zero
}

/** Apply to scrollable content inside a bottom sheet (before its scroll modifier). */
fun Modifier.sheetScrollGuard(): Modifier = nestedScroll(EndOverflowGuard)

/**
 * The app's bottom sheet: a Material sheet whose content can't make it bounce
 * when a list inside is scrolled past its end (see [sheetScrollGuard]).
 */
@androidx.compose.material3.ExperimentalMaterial3Api
@androidx.compose.runtime.Composable
fun AppSheet(
    onDismissRequest: () -> Unit,
    sheetState: androidx.compose.material3.SheetState = androidx.compose.material3.rememberModalBottomSheetState(),
    content: @androidx.compose.runtime.Composable androidx.compose.foundation.layout.ColumnScope.() -> Unit,
) {
    androidx.compose.material3.ModalBottomSheet(onDismissRequest = onDismissRequest, sheetState = sheetState) {
        androidx.compose.foundation.layout.Column(Modifier.sheetScrollGuard()) { content() }
    }
}
