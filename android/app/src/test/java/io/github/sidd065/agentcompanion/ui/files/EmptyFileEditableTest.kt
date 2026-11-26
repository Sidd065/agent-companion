package io.github.sidd065.agentcompanion.ui.files

import io.github.sidd065.agentcompanion.core.ReadResult
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class EmptyFileEditableTest {
    @Test
    fun zeroByteFileWithNullOrEmptyTextIsEditableWhileBinaryReturnsNull() {
        val zeroByteOmittedText = ReadResult(
            path = "/home/dev/empty.txt",
            size = 0L,
            mtime = 1000L,
            text = null,
            binary = false,
            truncated = false,
            mime = "text/plain; charset=utf-8",
        )
        assertEquals("", zeroByteOmittedText.editableTextOrNull())

        val zeroByteExplicitEmpty = zeroByteOmittedText.copy(text = "")
        assertEquals("", zeroByteExplicitEmpty.editableTextOrNull())

        val nonEmptyText = zeroByteOmittedText.copy(size = 5L, text = "hello")
        assertEquals("hello", nonEmptyText.editableTextOrNull())

        val binaryBlob = ReadResult(
            path = "/home/dev/blob.bin",
            size = 8L,
            mtime = 1000L,
            text = null,
            base64 = "AAEC",
            binary = true,
            truncated = false,
            mime = "application/octet-stream",
        )
        assertNull(binaryBlob.editableTextOrNull())
    }
}
