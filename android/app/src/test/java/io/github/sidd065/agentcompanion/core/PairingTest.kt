package io.github.sidd065.agentcompanion.core

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.Base64

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class PairingTest {

    private val key32 = ByteArray(32) { (it + 1).toByte() }
    private val sec32 = ByteArray(32) { (0xA0 + it).toByte() }
    private val keyB64 = Base64.getUrlEncoder().withoutPadding().encodeToString(key32)
    private val secB64 = Base64.getUrlEncoder().withoutPadding().encodeToString(sec32)

    @Test
    fun parsesHttpsQrLink() {
        val url = "https://dev-box.example.com/p/pair-abc123#k=$keyB64&s=$secB64&n=Dev%20Box"
        val link = Pairing.parse(url)
        assertNotNull(link)
        assertEquals("https://dev-box.example.com", link!!.base)
        assertEquals("pair-abc123", link.pairId)
        assertEquals(keyB64, link.hostKey)
        assertEquals(secB64, link.secret)
        assertEquals("Dev Box", link.name)
        assertArrayEquals(key32, link.hostKeyBytes)
        assertTrue(link.fingerprint.matches(Regex("[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}")))
        assertEquals(16, link.hostId.length)
    }

    @Test
    fun parsesCustomSchemeDeepLinkAndTrimsTrailingSlash() {
        val url = "agentc://pair?u=https%3A%2F%2Frpi.local%3A8443%2F&p=pid9&k=$keyB64&s=$secB64&n=rpi"
        val link = Pairing.parse(url)
        assertNotNull(link)
        assertEquals("https://rpi.local:8443", link!!.base)
        assertEquals("pid9", link.pairId)
        assertEquals("rpi", link.name)
    }

    @Test
    fun rejectsMalformedOrWrongLengthKeys() {
        val shortKey = Base64.getUrlEncoder().withoutPadding().encodeToString(ByteArray(16))
        assertNull(Pairing.parse("https://host/p/abc#k=$shortKey&s=$secB64"))
        assertNull(Pairing.parse("https://host/p/abc#k=$keyB64&s=$shortKey"))
        assertNull(Pairing.parse("https://host/not-p/abc#k=$keyB64&s=$secB64"))
        assertNull(Pairing.parse("ftp://host/p/abc#k=$keyB64&s=$secB64"))
        assertNull(Pairing.parse("not a url at all"))
    }
}
